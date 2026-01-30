package process

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SRTLAState string

const (
	SRTLAStopped     SRTLAState = "stopped"
	SRTLAStarting    SRTLAState = "starting"
	SRTLARegistering SRTLAState = "registering"
	SRTLAConnected   SRTLAState = "connected"
	SRTLAError       SRTLAState = "error"
)

type ConnectionStats struct {
	IP      string  `json:"ip"`
	State   string  `json:"state"`
	Bitrate float64 `json:"bitrate"`
	Window  int     `json:"window"`
	RTT     float64 `json:"rtt"`
	Quality float64 `json:"quality"`
	Sent    int64   `json:"sent"`
	Acked   int64   `json:"acked"`
	NAKs    int64   `json:"naks"`
}

type SRTLAStats struct {
	State        SRTLAState        `json:"state"`
	Connections  []ConnectionStats `json:"connections"`
	TotalBitrate float64           `json:"total_bitrate"`
	LastUpdate   time.Time         `json:"last_update"`
}

type SRTLAHandler struct {
	proc        *Process
	mu          sync.RWMutex
	stats       SRTLAStats
	logCallback func(LogLine)
	ipsFile     string

	bitrateRegex *regexp.Regexp
	connRegex    *regexp.Regexp
	statusRegex  *regexp.Regexp
}

func NewSRTLAHandler() *SRTLAHandler {
	h := &SRTLAHandler{
		proc: New("srtla_send"),
		stats: SRTLAStats{
			State:       SRTLAStopped,
			Connections: []ConnectionStats{},
		},
		bitrateRegex: regexp.MustCompile(`(\d+\.?\d*)\s*Mbps`),
		connRegex:    regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)`),
		statusRegex:  regexp.MustCompile(`window=(\d+)|rtt=(\d+\.?\d*)|quality=(\d+\.?\d*)`),
	}

	h.proc.SetLogCallback(h.handleLog)
	return h
}

func (h *SRTLAHandler) SetLogCallback(cb func(LogLine)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logCallback = cb
}

func (h *SRTLAHandler) Start(binaryPath string, localPort int, remoteHost string, remotePort int, bindIPs []string, classic, noQuality, exploration bool) error {
	h.mu.Lock()
	h.stats = SRTLAStats{State: SRTLAStarting, Connections: []ConnectionStats{}}
	h.mu.Unlock()

	tmpDir := os.TempDir()
	h.ipsFile = filepath.Join(tmpDir, "srtla_ips.txt")

	if err := h.writeIPsFile(bindIPs); err != nil {
		h.mu.Lock()
		h.stats.State = SRTLAError
		h.mu.Unlock()
		return fmt.Errorf("failed to write IPs file: %w", err)
	}

	args := []string{
		strconv.Itoa(localPort),
		remoteHost,
		strconv.Itoa(remotePort),
		h.ipsFile,
	}

	if classic {
		args = append(args, "--classic")
	}
	if noQuality {
		args = append(args, "--no-quality")
	}
	if exploration {
		args = append(args, "--exploration")
	}

	// Log the startup command
	h.handleLog(LogLine{
		Timestamp: time.Now(),
		Source:    "srtla_send",
		Line:      fmt.Sprintf("[STARTING] %s %v", binaryPath, formatStartupArgs(args)),
	})

	return h.proc.Start(binaryPath, args...)
}

// formatStartupArgs formats arguments for display
func formatStartupArgs(args []string) string {
	formatted := make([]string, 0, len(args))
	for _, arg := range args {
		if len(arg) > 50 {
			formatted = append(formatted, arg[:47]+"...")
		} else {
			formatted = append(formatted, arg)
		}
	}
	return strings.Join(formatted, " ")
}

func (h *SRTLAHandler) Stop() error {
	h.mu.Lock()
	h.stats = SRTLAStats{State: SRTLAStopped, Connections: []ConnectionStats{}}
	h.mu.Unlock()

	if h.ipsFile != "" {
		os.Remove(h.ipsFile)
	}

	return h.proc.Stop()
}

// ReloadIPs is implemented in srtla_unix.go and srtla_windows.go

func (h *SRTLAHandler) Stats() SRTLAStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stats
}

func (h *SRTLAHandler) ProcessState() State {
	return h.proc.State()
}

func (h *SRTLAHandler) writeIPsFile(ips []string) error {
	var validIPs []string
	var invalidIPs []string

	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if net.ParseIP(ip) != nil {
			validIPs = append(validIPs, ip)
		} else {
			invalidIPs = append(invalidIPs, ip)
		}
	}

	if len(invalidIPs) > 0 {
		h.handleLog(LogLine{
			Timestamp: time.Now(),
			Source:    "srtla_send",
			Line:      fmt.Sprintf("[WARNING] Skipped invalid IPs: %s", strings.Join(invalidIPs, ", ")),
		})
	}

	if len(validIPs) == 0 {
		return fmt.Errorf("no valid bind IPs found - cannot start SRTLA without uplinks")
	}

	h.handleLog(LogLine{
		Timestamp: time.Now(),
		Source:    "srtla_send",
		Line:      fmt.Sprintf("[INFO] Using %d valid bind IP(s): %s", len(validIPs), strings.Join(validIPs, ", ")),
	})

	content := strings.Join(validIPs, "\n")
	if len(validIPs) > 0 {
		content += "\n"
	}
	return os.WriteFile(h.ipsFile, []byte(content), 0644)
}

func (h *SRTLAHandler) handleLog(log LogLine) {
	h.mu.Lock()
	cb := h.logCallback
	h.mu.Unlock()

	if cb != nil {
		cb(log)
	}

	h.parseLogLine(log.Line)
}

func (h *SRTLAHandler) parseLogLine(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	lower := strings.ToLower(line)

	if strings.Contains(lower, "registering") || strings.Contains(lower, "reg1") || strings.Contains(lower, "reg2") {
		h.stats.State = SRTLARegistering
		h.stats.LastUpdate = time.Now()
	}

	if strings.Contains(lower, "connected") || strings.Contains(lower, "reg3") || strings.Contains(lower, "registration complete") {
		h.stats.State = SRTLAConnected
		h.stats.LastUpdate = time.Now()
	}

	if match := h.bitrateRegex.FindStringSubmatch(line); len(match) > 1 {
		if v, err := strconv.ParseFloat(match[1], 64); err == nil {
			h.stats.TotalBitrate = v
			h.stats.LastUpdate = time.Now()
		}
	}

	if strings.Contains(line, "error") || strings.Contains(line, "failed") {
		if h.stats.State != SRTLAConnected {
			h.stats.State = SRTLAError
		}
	}
}

// IsStale returns true when the process is running but has not emitted
// any parsed status updates within the given threshold.
func (h *SRTLAHandler) IsStale(threshold time.Duration) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.proc.State() != StateRunning {
		return false
	}

	if h.stats.LastUpdate.IsZero() {
		return false
	}

	return time.Since(h.stats.LastUpdate) > threshold
}
