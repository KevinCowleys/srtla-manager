package wifi

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Manager handles WiFi operations via nmcli.
type Manager struct {
	log Logger
}

// Logger is a minimal logging interface.
type Logger interface {
	Printf(format string, v ...interface{})
}

// NetworkInfo represents a WiFi network.
type NetworkInfo struct {
	SSID      string `json:"ssid"`
	Signal    int    `json:"signal"`
	Security  string `json:"security"`
	Connected bool   `json:"connected"`
	DualBand  bool   `json:"dual_band"`
}

// ConnectionInfo represents current WiFi connection status.
type ConnectionInfo struct {
	Connected bool   `json:"connected"`
	SSID      string `json:"ssid"`
	IP        string `json:"ip"`
	Signal    int    `json:"signal"`
	Frequency string `json:"frequency"`
	Interface string `json:"interface"`
	LastError string `json:"last_error,omitempty"`
}

// HotspotConfig represents hotspot/AP mode settings.
type HotspotConfig struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Band     string `json:"band"` // 2.4 or 5
	Channel  int    `json:"channel"`
}

// NewManager creates a WiFi manager.
func NewManager(log Logger) *Manager {
	return &Manager{log: log}
}

// IsAvailable checks if nmcli is available.
func (m *Manager) IsAvailable() bool {
	cmd := exec.Command("nmcli", "--version")
	return cmd.Run() == nil
}

// ListNetworks returns available WiFi networks.
func (m *Manager) ListNetworks() ([]NetworkInfo, error) {
	cmd := exec.Command("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY,ACTIVE,FREQ", "dev", "wifi", "list")
	output, err := cmd.Output()
	if err != nil {
		m.log.Printf("failed to list networks: %v", err)
		return nil, err
	}

	// Track bands per SSID to detect dual-band networks
	type bandInfo struct {
		net    NetworkInfo
		band24 bool // 2.4GHz band found
		band5  bool // 5GHz band found
	}

	networkMap := make(map[string]*bandInfo)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) < 5 {
			continue
		}

		ssid := strings.TrimSpace(parts[0])
		if ssid == "" {
			continue // Skip hidden networks for now
		}

		signal := 0
		fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &signal)

		security := strings.TrimSpace(parts[2])
		active := strings.TrimSpace(parts[3]) == "yes"

		// Parse frequency to determine band
		freq := 0
		fmt.Sscanf(strings.TrimSpace(parts[4]), "%d", &freq)

		isBand24 := freq >= 2400 && freq < 3000
		isBand5 := freq >= 5000 && freq < 6000

		// Get or create entry for this SSID
		if _, found := networkMap[ssid]; !found {
			networkMap[ssid] = &bandInfo{
				net: NetworkInfo{
					SSID:      ssid,
					Signal:    signal,
					Security:  security,
					Connected: active,
					DualBand:  false,
				},
			}
		}

		// Update with strongest signal
		if signal > networkMap[ssid].net.Signal {
			networkMap[ssid].net.Signal = signal
			networkMap[ssid].net.Connected = active
		}

		// Track which bands this SSID appears on
		if isBand24 {
			networkMap[ssid].band24 = true
		}
		if isBand5 {
			networkMap[ssid].band5 = true
		}
	}

	// Convert map to slice and set DualBand flag
	var networks []NetworkInfo
	for _, info := range networkMap {
		if info.band24 && info.band5 {
			info.net.DualBand = true
		}
		networks = append(networks, info.net)
	}

	return networks, nil
}

// GetConnectionStatus returns current WiFi connection status.
func (m *Manager) GetConnectionStatus() *ConnectionInfo {
	info := &ConnectionInfo{
		Connected: false,
	}

	// Check if any WiFi connection is active
	cmd := exec.Command("nmcli", "-t", "-f", "ACTIVE,NAME,TYPE", "con", "show", "--active")
	output, err := cmd.Output()
	if err != nil {
		info.LastError = fmt.Sprintf("failed to get active connections: %v", err)
		return info
	}

	var wifiConnName string
	var isHotspot bool
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) >= 3 && strings.Contains(parts[2], "802-11-wireless") {
			wifiConnName = strings.TrimSpace(parts[1])
			// Check if this is a hotspot connection
			if strings.HasPrefix(wifiConnName, "srtla-hotspot-") {
				isHotspot = true
			}
			break
		}
	}

	if wifiConnName == "" {
		return info
	}

	info.Connected = true

	// If this is a hotspot, get hotspot-specific info
	if isHotspot {
		// Get SSID from the connection
		cmd = exec.Command("nmcli", "-t", "-f", "802-11-wireless.ssid", "con", "show", wifiConnName)
		output, err = cmd.Output()
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "ssid") {
					parts := strings.Split(line, ":")
					if len(parts) >= 2 {
						info.SSID = strings.TrimSpace(parts[1])
					}
				}
			}
		}

		// Get hotspot IP address
		info.IP = m.GetHotspotIP()
		if info.IP == "" {
			info.IP = "10.42.0.1" // Default hotspot IP
		}

		// Get interface name
		cmd = exec.Command("nmcli", "-t", "-f", "DEVICE", "con", "show", "--active", wifiConnName)
		output, err = cmd.Output()
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "DEVICE") {
					parts := strings.Split(line, ":")
					if len(parts) >= 2 {
						info.Interface = strings.TrimSpace(parts[1])
					}
				}
			}
		}

		return info
	}

	info.Connected = true

	// Get details about the WiFi connection
	cmd = exec.Command("nmcli", "-t", "-f", "connection.id,802-11-wireless-properties.ssid,ipv4.addresses,signal", "con", "show", wifiConnName)
	output, err = cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "ssid") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					info.SSID = strings.TrimSpace(parts[1])
				}
			} else if strings.Contains(line, "addresses") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					addr := strings.TrimSpace(parts[1])
					// Extract IP from CIDR format
					if idx := strings.Index(addr, "/"); idx > 0 {
						info.IP = addr[:idx]
					}
				}
			}
		}
	}

	// Get interface info for signal strength
	cmd = exec.Command("nmcli", "-t", "-f", "DEVICE,SIGNAL", "dev", "wifi")
	output, err = cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				iface := strings.TrimSpace(parts[0])
				signal := 0
				fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &signal)
				if signal > 0 {
					info.Interface = iface
					info.Signal = signal
					break
				}
			}
		}
	}

	return info
}

// Connect connects to a WiFi network.
func (m *Manager) Connect(ssid, password string) error {
	if !m.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	m.log.Printf("connecting to WiFi network: %s", ssid)

	// Try to connect (will create connection if it doesn't exist)
	args := []string{"dev", "wifi", "connect", ssid}
	if password != "" {
		args = append(args, "password", password)
	}

	cmd := exec.Command("nmcli", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to connect: %v: %s", err, string(output))
		return fmt.Errorf("failed to connect to %s: %w", ssid, err)
	}

	m.log.Printf("connected to WiFi: %s", ssid)
	return nil
}

// Disconnect disconnects from WiFi.
func (m *Manager) Disconnect() error {
	if !m.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	// Find WiFi device
	cmd := exec.Command("nmcli", "-t", "-f", "DEVICE,TYPE", "dev")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	var wifiDevice string
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) >= 2 && strings.Contains(parts[1], "wifi") {
			wifiDevice = strings.TrimSpace(parts[0])
			break
		}
	}

	if wifiDevice == "" {
		return fmt.Errorf("no WiFi device found")
	}

	m.log.Printf("disconnecting WiFi device: %s", wifiDevice)

	cmd = exec.Command("nmcli", "dev", "disconnect", wifiDevice)
	output, err = cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to disconnect: %v: %s", err, string(output))
		return fmt.Errorf("failed to disconnect: %w", err)
	}

	m.log.Printf("disconnected from WiFi")
	return nil
}

// CreateHotspot creates a WiFi hotspot (access point mode).
func (m *Manager) CreateHotspot(config HotspotConfig) error {
	if !m.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	if config.SSID == "" {
		return fmt.Errorf("SSID required")
	}

	if config.Password == "" {
		return fmt.Errorf("password required (minimum 8 characters)")
	}

	if len(config.Password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	m.log.Printf("creating hotspot: %s", config.SSID)

	// Find WiFi device
	cmd := exec.Command("nmcli", "-t", "-f", "DEVICE,TYPE", "dev")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	var wifiDevice string
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) >= 2 && strings.Contains(parts[1], "wifi") {
			wifiDevice = strings.TrimSpace(parts[0])
			break
		}
	}

	if wifiDevice == "" {
		return fmt.Errorf("no WiFi device found")
	}

	// Create hotspot connection
	connName := fmt.Sprintf("srtla-hotspot-%d", time.Now().Unix())

	args := []string{
		"con", "add",
		"type", "wifi",
		"con-name", connName,
		"ifname", wifiDevice,
		"ssid", config.SSID,
	}

	cmd = exec.Command("nmcli", args...)
	output, err = cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to create hotspot connection: %v: %s", err, string(output))
		return fmt.Errorf("failed to create hotspot: %w", err)
	}

	m.log.Printf("created hotspot connection: %s", connName)

	// Now configure the WiFi settings
	wifiArgs := []string{
		"con", "modify", connName,
		"802-11-wireless.mode", "ap",
		"802-11-wireless-security.key-mgmt", "wpa-psk",
		"802-11-wireless-security.psk", config.Password,
	}

	cmd = exec.Command("nmcli", wifiArgs...)
	output, err = cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to set WiFi security: %v: %s", err, string(output))
		// Continue anyway, try to activate
	}

	// Configure IPv4 for shared mode
	ipv4Args := []string{
		"con", "modify", connName,
		"ipv4.method", "shared",
		"connection.autoconnect", "no",
	}

	cmd = exec.Command("nmcli", ipv4Args...)
	output, err = cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to set IPv4 method: %v: %s", err, string(output))
		// Continue anyway
	}

	// Activate it
	cmd = exec.Command("nmcli", "con", "up", connName)
	output, err = cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to activate hotspot: %v: %s", err, string(output))
		return fmt.Errorf("failed to activate hotspot: %w", err)
	}

	m.log.Printf("hotspot activated: %s", config.SSID)
	return nil
}

// StopHotspot stops the hotspot and cleans up the connection.
func (m *Manager) StopHotspot() error {
	if !m.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	// Find and remove hotspot connections (both active and inactive)
	cmd := exec.Command("nmcli", "-t", "-f", "NAME,TYPE", "con", "show")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	var lastErr error
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) >= 2 {
			connName := strings.TrimSpace(parts[0])
			connType := strings.TrimSpace(parts[1])
			if strings.Contains(connType, "802-11-wireless") && strings.HasPrefix(connName, "srtla-hotspot-") {
				m.log.Printf("removing hotspot connection: %s", connName)
				// First deactivate if active
				downCmd := exec.Command("nmcli", "con", "down", connName)
				downCmd.Run() // Ignore errors - connection might not be active

				// Then delete the connection to clean up
				deleteCmd := exec.Command("nmcli", "con", "delete", connName)
				if output, err := deleteCmd.CombinedOutput(); err != nil {
					m.log.Printf("failed to delete hotspot %s: %v: %s", connName, err, string(output))
					lastErr = err
				}
			}
		}
	}

	return lastErr
}

// ForgetNetwork removes a saved WiFi network.
func (m *Manager) ForgetNetwork(ssid string) error {
	if !m.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	m.log.Printf("forgetting network: %s", ssid)

	cmd := exec.Command("nmcli", "con", "delete", ssid)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.log.Printf("failed to forget network: %v: %s", err, string(output))
		return fmt.Errorf("failed to forget network: %w", err)
	}

	m.log.Printf("forgot network: %s", ssid)
	return nil
}
