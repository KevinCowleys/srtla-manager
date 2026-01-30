package usbnet

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options configures the USB network reconciler.
type Options struct {
	PersistPath    string
	NMMethod       string
	EnableADBBlock bool
	Logger         *log.Logger
}

// Option mutates Options.
type Option func(*Options)

// DefaultOptions returns sane defaults.
func DefaultOptions() Options {
	return Options{
		PersistPath:    "/var/lib/srtla-manager/device_mappings.json",
		NMMethod:       "dbus",
		EnableADBBlock: false,
		Logger:         nil,
	}
}

// WithPersistPath sets the path used for device mapping persistence.
func WithPersistPath(path string) Option {
	return func(o *Options) {
		o.PersistPath = path
	}
}

// WithNMMethod selects the NetworkManager control path (e.g., "dbus" or "nmcli").
func WithNMMethod(method string) Option {
	return func(o *Options) {
		o.NMMethod = method
	}
}

// WithADBBlock toggles adb blocking behavior for matched devices.
func WithADBBlock(enabled bool) Option {
	return func(o *Options) {
		o.EnableADBBlock = enabled
	}
}

// WithLogger supplies a custom logger; defaults to a discard logger.
func WithLogger(l *log.Logger) Option {
	return func(o *Options) {
		o.Logger = l
	}
}

// Start launches the USB network reconciler and returns a stop function.
// This is a placeholder; full reconciliation logic will be added incrementally.
func Start(parent context.Context, opts ...Option) (*Service, error) {
	cfg := DefaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", log.LstdFlags)
	}

	// Ensure persist directory exists
	if cfg.PersistPath != "" {
		dir := filepath.Dir(cfg.PersistPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logger.Printf("warning: failed to create persist directory %s: %v", dir, err)
			// Continue anyway - persistence is optional
		}
	}

	ctx, cancel := context.WithCancel(parent)
	m := &manager{
		ctx:    ctx,
		cancel: cancel,
		opts:   cfg,
		log:    logger,
		done:   make(chan struct{}),
	}

	svc := &Service{m: m}
	go m.run()

	return svc, nil
}

// Service exposes reconciler status and lifecycle control.
type Service struct {
	m *manager
}

// Stop blocks until the reconciler exits.
func (s *Service) Stop() error {
	if s == nil || s.m == nil {
		return nil
	}
	s.m.cancel()
	<-s.m.done
	return nil
}

// Status returns the latest known device states.
func (s *Service) Status() []DeviceStatus {
	if s == nil || s.m == nil {
		return nil
	}
	return s.m.snapshot()
}

// DeviceStatus represents a USB RNDIS interface state.
type DeviceStatus struct {
	Serial    string    `json:"serial"`
	MAC       string    `json:"mac"`
	Interface string    `json:"interface"`
	IPv4      string    `json:"ipv4"`
	State     string    `json:"state"`
	Error     string    `json:"error"`
	LastSeen  time.Time `json:"last_seen"`
}

type manager struct {
	ctx    context.Context
	cancel context.CancelFunc
	opts   Options
	log    *log.Logger
	done   chan struct{}

	mu      sync.RWMutex
	devices []DeviceStatus

	scanner  *Scanner
	nmClient *NMClient
}

func (m *manager) run() {
	defer close(m.done)
	m.log.Printf("usbnet reconciler started (persist=%s, nm=%s, adb_block=%v)", m.opts.PersistPath, m.opts.NMMethod, m.opts.EnableADBBlock)

	m.scanner = NewScanner(m.log)
	m.nmClient = NewNMClient(m.log)

	if !m.nmClient.IsAvailable() {
		m.log.Printf("warning: nmcli not available; will use ip/dhclient fallback")
	}

	// Scan immediately on start
	m.scan()

	// Periodic scan every 3 seconds
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// Track retry state for pending devices (to avoid thrashing)
	lastRetry := make(map[string]time.Time)
	retryInterval := 10 * time.Second

	for {
		select {
		case <-ticker.C:
			m.scan()

			// Attempt to reconcile pending devices
			m.mu.RLock()
			devices := make([]DeviceStatus, len(m.devices))
			copy(devices, m.devices)
			m.mu.RUnlock()

			for i, dev := range devices {
				if dev.State == "pending" {
					// Throttle retries to avoid constant errors
					if lastTime, exists := lastRetry[dev.Interface]; exists && time.Since(lastTime) < retryInterval {
						continue
					}

					m.log.Printf("reconciling pending device %s on %s", dev.Serial, dev.Interface)
					lastRetry[dev.Interface] = time.Now()

					// Try to bring it up
					if err := m.reconcilePending(&devices[i]); err != nil {
						m.log.Printf("failed to reconcile %s: %v", dev.Interface, err)
						// Update device error state
						m.mu.Lock()
						if j := m.findDeviceIndex(dev.Interface); j >= 0 {
							m.devices[j].Error = err.Error()
						}
						m.mu.Unlock()
					}
				}
			}

		case <-m.ctx.Done():
			m.log.Printf("usbnet reconciler stopped")
			return
		}
	}
}

func (m *manager) scan() {
	devices := m.scanner.Scan()
	m.mu.Lock()
	m.devices = devices
	m.mu.Unlock()
	if len(devices) > 0 {
		m.log.Printf("found %d USB device(s)", len(devices))
	}
}

func (m *manager) snapshot() []DeviceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DeviceStatus, len(m.devices))
	copy(out, m.devices)
	return out
}

// reconcilePending attempts to bring up a pending interface and get it a DHCP lease.
func (m *manager) reconcilePending(dev *DeviceStatus) error {
	// Strategy 1: Try NetworkManager if available
	if m.nmClient.IsAvailable() {
		if err := m.nmClient.EnsureConnection(dev); err != nil {
			m.log.Printf("failed to ensure NM connection for %s: %v", dev.Interface, err)
		} else {
			if err := m.nmClient.ActivateConnection(dev); err != nil {
				m.log.Printf("failed to activate NM connection for %s: %v", dev.Interface, err)
			} else {
				// NM activation succeeded; also run a direct DHCP request to clear pending state fast
				if err := m.nmClient.BringUpInterface(dev.Interface); err != nil {
					m.log.Printf("post-activation dhcp for %s failed: %v", dev.Interface, err)
				}
				return nil // NM path completed
			}
		}
	}

	// Strategy 2: Fallback to ip + dhclient
	m.log.Printf("using ip/dhclient fallback for %s", dev.Interface)
	if err := m.nmClient.BringUpInterface(dev.Interface); err != nil {
		return fmt.Errorf("failed to bring up %s: %w", dev.Interface, err)
	}

	return nil
}

// findDeviceIndex returns the index of a device by interface name, or -1 if not found.
func (m *manager) findDeviceIndex(ifname string) int {
	for i, dev := range m.devices {
		if dev.Interface == ifname {
			return i
		}
	}
	return -1
}
