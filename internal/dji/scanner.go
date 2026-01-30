package dji

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// DeviceModel represents DJI camera model type
type DeviceModel string

const (
	ModelOsmoAction3    DeviceModel = "OA3"
	ModelOsmoAction4    DeviceModel = "OA4"
	ModelOsmoAction5Pro DeviceModel = "OA5P"
	ModelOsmoAction6    DeviceModel = "OA6"
	ModelOsmoPocket3    DeviceModel = "OP3"
	ModelUnknown        DeviceModel = "Unknown"
)

// DiscoveredDevice represents a DJI camera found via Bluetooth
type DiscoveredDevice struct {
	ID           string
	Name         string
	Model        DeviceModel
	RSSI         int
	Paired       bool
	Connected    bool
	DiscoveredAt time.Time
	LastSeen     time.Time
	Address      bluetooth.Address // Store the actual address for connection
}

// Scanner handles BLE device discovery using tinygo-org/bluetooth
type Scanner struct {
	mu                sync.RWMutex
	adapter           *bluetooth.Adapter
	discoveredDevices map[string]*DiscoveredDevice
	scanning          bool
	stopChan          chan struct{}
}

var defaultAdapter = bluetooth.DefaultAdapter

// NewScanner creates a new DJI device scanner
func NewScanner() *Scanner {
	return &Scanner{
		adapter:           defaultAdapter,
		discoveredDevices: make(map[string]*DiscoveredDevice),
		stopChan:          make(chan struct{}),
	}
}

// StartScanning begins BLE discovery for DJI devices
func (s *Scanner) StartScanning(timeout time.Duration) error {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return fmt.Errorf("scan already in progress")
	}
	s.scanning = true
	s.stopChan = make(chan struct{})
	s.discoveredDevices = make(map[string]*DiscoveredDevice)
	s.mu.Unlock()

	log.Println("[DJI] Starting BLE scan...")

	// Enable adapter if not already enabled
	if err := s.adapter.Enable(); err != nil {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
		return fmt.Errorf("failed to enable BLE adapter: %w", err)
	}

	go s.scanBLE(timeout)
	return nil
}

// StopScanning stops the current BLE discovery
func (s *Scanner) StopScanning() error {
	s.mu.Lock()
	if !s.scanning {
		s.mu.Unlock()
		return fmt.Errorf("no scan in progress")
	}
	close(s.stopChan)
	s.scanning = false
	s.mu.Unlock()

	// Stop the scan
	if err := s.adapter.StopScan(); err != nil {
		log.Printf("[DJI] Warning: failed to stop scan: %v\n", err)
	}

	log.Println("[DJI] BLE scan stopped")
	return nil
}

// GetDiscoveredDevices returns all discovered DJI devices
func (s *Scanner) GetDiscoveredDevices() []*DiscoveredDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()

	devices := make([]*DiscoveredDevice, 0, len(s.discoveredDevices))
	for _, dev := range s.discoveredDevices {
		devices = append(devices, dev)
	}
	return devices
}

// GetDevice returns a specific discovered device by ID
func (s *Scanner) GetDevice(id string) *DiscoveredDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discoveredDevices[id]
}

// RemoveDevice deletes a discovered device by ID
func (s *Scanner) RemoveDevice(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.discoveredDevices[id]; ok {
		delete(s.discoveredDevices, id)
		log.Printf("[DJI] Removed device: %s\n", id)
		return true
	}
	return false
}

// GetAdapter returns the BLE adapter for external use
func (s *Scanner) GetAdapter() *bluetooth.Adapter {
	return s.adapter
}

// scanBLE performs the actual BLE scanning
func (s *Scanner) scanBLE(timeout time.Duration) {
	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}()

	log.Println("[DJI] BLE scan listening for devices...")

	// Create timeout timer
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Channel to signal scan completion
	done := make(chan struct{})

	// Start scanning
	go func() {
		err := s.adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
			s.processDiscoveredDevice(result)
		})
		if err != nil {
			log.Printf("[DJI] BLE scan error: %v\n", err)
		}
		close(done)
	}()

	// Wait for stop signal or timeout
	select {
	case <-s.stopChan:
		log.Println("[DJI] BLE scan stopped by user")
		s.adapter.StopScan()
	case <-timer.C:
		log.Println("[DJI] BLE scan timeout")
		s.adapter.StopScan()
	case <-done:
		log.Println("[DJI] BLE scan completed")
	}
}

// processDiscoveredDevice checks if a device is a DJI camera and adds it to discovered list
func (s *Scanner) processDiscoveredDevice(result bluetooth.ScanResult) {
	name := result.LocalName()
	addr := result.Address.String()

	if addr == "" {
		return
	}

	// Log all devices with names for debugging
	if name != "" {
		log.Printf("[DJI] BLE device found: addr=%s name=%q rssi=%d\n", addr, name, result.RSSI)
	}

	// Only accept devices with DJI-like names
	if !isDJIName(name) {
		return
	}

	model := detectModelFromName(name)
	rssi := int(result.RSSI)

	s.AddDiscoveredDevice(&DiscoveredDevice{
		ID:      addr,
		Name:    name,
		Model:   model,
		RSSI:    rssi,
		Address: result.Address,
	})
}

// AddDiscoveredDevice adds or updates a device in the discovered list
func (s *Scanner) AddDiscoveredDevice(device *DiscoveredDevice) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if existing, ok := s.discoveredDevices[device.ID]; ok {
		existing.Name = device.Name
		existing.RSSI = device.RSSI
		existing.Model = device.Model
		existing.LastSeen = now
		if device.Address != (bluetooth.Address{}) {
			existing.Address = device.Address
		}
	} else {
		device.DiscoveredAt = now
		device.LastSeen = now
		s.discoveredDevices[device.ID] = device
		log.Printf("[DJI] Discovered device: %s (%s) RSSI: %d\n", device.Name, device.Model, device.RSSI)
	}
}

// detectModelFromName attempts to identify DJI model from device name
func detectModelFromName(name string) DeviceModel {
	upper := strings.ToUpper(name)

	switch {
	case strings.Contains(upper, "ACTION6"):
		return ModelOsmoAction6
	case strings.Contains(upper, "ACTION5"):
		return ModelOsmoAction5Pro
	case strings.Contains(upper, "ACTION4") || strings.Contains(upper, "OA4"):
		return ModelOsmoAction4
	case strings.Contains(upper, "ACTION3") || strings.Contains(upper, "OA3"):
		return ModelOsmoAction3
	case strings.Contains(upper, "POCKET3") || strings.Contains(upper, "OP3"):
		return ModelOsmoPocket3
	default:
		return ModelUnknown
	}
}

// IsScanning returns whether a scan is currently in progress
func (s *Scanner) IsScanning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanning
}

// isDJIName returns true if the device name looks like a DJI camera
func isDJIName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	return strings.Contains(upper, "DJI") ||
		strings.Contains(upper, "ACTION") ||
		strings.Contains(upper, "OSMO") ||
		strings.Contains(upper, "POCKET") ||
		strings.Contains(upper, "OA3") ||
		strings.Contains(upper, "OA4") ||
		strings.Contains(upper, "OA5") ||
		strings.Contains(upper, "OA6") ||
		strings.Contains(upper, "OP3")
}

// GetPairedDJIDevices returns DJI devices from discovered list that are marked as paired
// Note: tinygo-org/bluetooth doesn't directly expose pairing info during scan,
// so this returns devices we've connected to before
func (s *Scanner) GetPairedDJIDevices() ([]*DiscoveredDevice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var pairedDevices []*DiscoveredDevice
	for _, dev := range s.discoveredDevices {
		if dev.Paired {
			pairedDevices = append(pairedDevices, dev)
		}
	}
	return pairedDevices, nil
}

// RefreshDevice updates information for a specific device
// Note: With tinygo-org/bluetooth, we can't query a specific device without scanning
func (s *Scanner) RefreshDevice(deviceID string) (*DiscoveredDevice, error) {
	s.mu.RLock()
	dev, exists := s.discoveredDevices[deviceID]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("device not found: %s", deviceID)
	}

	return dev, nil
}

// ParseMACAddress parses a MAC address string into a bluetooth.Address
func ParseMACAddress(mac string) (bluetooth.Address, error) {
	// MAC addresses are in format XX:XX:XX:XX:XX:XX
	var addr bluetooth.Address
	parts := strings.Split(strings.ToUpper(mac), ":")
	if len(parts) != 6 {
		return addr, fmt.Errorf("invalid MAC address format: %s", mac)
	}

	var bytes [6]byte
	for i, part := range parts {
		var b byte
		_, err := fmt.Sscanf(part, "%02X", &b)
		if err != nil {
			return addr, fmt.Errorf("invalid MAC address byte: %s", part)
		}
		bytes[5-i] = b // Reverse order for bluetooth.Address
	}

	addr.Set(fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		bytes[5], bytes[4], bytes[3], bytes[2], bytes[1], bytes[0]))
	return addr, nil
}
