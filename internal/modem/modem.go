package modem

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type ModemInfo struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Model         string `json:"model"`
	Manufacturer  string `json:"manufacturer"`
	PhoneNumber   string `json:"phone_number"`
	IMEI          string `json:"imei"`
	IMSI          string `json:"imsi"`
	Carrier       string `json:"carrier"`
	Country       string `json:"country"`
	SignalPercent int    `json:"signal_percent"`
	SignalDBm     int    `json:"signal_dbm"`
	NetworkType   string `json:"network_type"`
	State         string `json:"state"`
	IPAddress     string `json:"ip_address"`
	Interface     string `json:"interface"`
	DataTx        int64  `json:"data_tx"`
	DataRx        int64  `json:"data_rx"`
}

type Manager struct {
	mu          sync.RWMutex
	mmcliAvail  bool
	adbProvider *ADBProvider
}

func NewManager() *Manager {
	m := &Manager{
		adbProvider: NewADBProvider(),
	}
	m.checkAvailable()
	return m
}

func (m *Manager) checkAvailable() {
	_, err := exec.LookPath("mmcli")
	m.mmcliAvail = err == nil
}

func (m *Manager) IsAvailable() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mmcliAvail || m.adbProvider.IsAvailable()
}

func (m *Manager) ListModems() ([]ModemInfo, error) {
	if !m.IsAvailable() {
		return nil, nil
	}

	var modems []ModemInfo
	seenIMEIs := make(map[string]bool) // Track IMEIs to avoid duplicates

	// Reset ADB interface tracking for fresh scan
	if m.adbProvider != nil {
		m.adbProvider.usedInterfaces = make(map[string]bool)
	}

	// Try mmcli first (prefer mmcli over ADB for same device)
	if m.mmcliAvail {
		cmd := exec.Command("mmcli", "-L", "-J")
		output, err := cmd.Output()
		if err == nil {
			var listResp struct {
				ModemList []string `json:"modem-list"`
			}
			if err := json.Unmarshal(output, &listResp); err == nil {
				for _, path := range listResp.ModemList {
					parts := strings.Split(path, "/")
					id := parts[len(parts)-1]

					info, err := m.getMMCLIModem(id)
					if err != nil {
						continue
					}
					info.ID = "mmcli:" + id
					modems = append(modems, *info)

					// Track this IMEI to avoid duplicates from ADB
					if info.IMEI != "" {
						seenIMEIs[info.IMEI] = true
					}
				}
			}
		}
	}

	// Also check ADB devices (skip if already seen via mmcli)
	if m.adbProvider.IsAvailable() {
		devices, err := m.adbProvider.ListDevices()
		if err == nil {
			for _, deviceID := range devices {
				info, err := m.adbProvider.GetModemInfo(deviceID)
				if err != nil || info == nil {
					continue
				}

				// Skip if we already have this device via mmcli (based on IMEI)
				if info.IMEI != "" && seenIMEIs[info.IMEI] {
					continue
				}

				info.ID = "adb:" + deviceID
				modems = append(modems, *info)

				if info.IMEI != "" {
					seenIMEIs[info.IMEI] = true
				}
			}
		}
	}

	return modems, nil
}

func (m *Manager) GetModem(id string) (*ModemInfo, error) {
	if !m.IsAvailable() {
		return nil, nil
	}

	// Handle prefixed IDs
	if strings.HasPrefix(id, "adb:") {
		deviceID := strings.TrimPrefix(id, "adb:")
		return m.adbProvider.GetModemInfo(deviceID)
	}

	// Strip mmcli: prefix if present
	if strings.HasPrefix(id, "mmcli:") {
		id = strings.TrimPrefix(id, "mmcli:")
	}

	return m.getMMCLIModem(id)
}

func (m *Manager) DialUSSD(id, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("ussd code is empty")
	}

	if strings.HasPrefix(id, "adb:") {
		return "", fmt.Errorf("ussd not supported for adb devices")
	}

	if strings.HasPrefix(id, "mmcli:") {
		id = strings.TrimPrefix(id, "mmcli:")
	}

	if !m.mmcliAvail {
		return "", fmt.Errorf("mmcli not available")
	}

	cmd := exec.Command("mmcli", "-m", id, "--3gpp-ussd-initiate="+code)
	output, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(output))
	if err != nil {
		return result, fmt.Errorf("mmcli ussd initiate: %w", err)
	}

	// Best-effort session cancel; ignore any failures.
	cancelCmd := exec.Command("mmcli", "-m", id, "--3gpp-ussd-cancel")
	_ = cancelCmd.Run()

	return result, nil
}

func (m *Manager) getMMCLIModem(id string) (*ModemInfo, error) {
	if !m.mmcliAvail {
		return nil, nil
	}

	cmd := exec.Command("mmcli", "-m", id, "-J")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var modemResp struct {
		Modem struct {
			ThreeGPP struct {
				IMEI         string `json:"imei"`
				OperatorName string `json:"operator-name"`
			} `json:"3gpp"`
			Generic struct {
				Model               string `json:"model"`
				Manufacturer        string `json:"manufacturer"`
				EquipmentIdentifier string `json:"equipment-identifier"`
				State               string `json:"state"`
				StateFailedReason   string `json:"state-failed-reason"`
				SignalQuality       struct {
					Value string `json:"value"`
				} `json:"signal-quality"`
				AccessTechnologies []string `json:"access-technologies"`
				CurrentModes       string   `json:"current-modes"`
				OwnNumbers         []string `json:"own-numbers"`
				Bearers            []string `json:"bearers"`
				PrimaryPort        string   `json:"primary-port"`
				Ports              []string `json:"ports"`
			} `json:"generic"`
			DBusPath string `json:"dbus-path"`
		} `json:"modem"`
	}

	if err := json.Unmarshal(output, &modemResp); err != nil {
		return nil, err
	}

	info := &ModemInfo{
		ID:           id,
		Path:         modemResp.Modem.DBusPath,
		Model:        modemResp.Modem.Generic.Model,
		Manufacturer: modemResp.Modem.Generic.Manufacturer,
		IMEI:         modemResp.Modem.ThreeGPP.IMEI,
		Carrier:      modemResp.Modem.ThreeGPP.OperatorName,
		State:        modemResp.Modem.Generic.State,
	}

	// Add failed reason to state if present
	if info.State == "failed" && modemResp.Modem.Generic.StateFailedReason != "" {
		info.State = modemResp.Modem.Generic.StateFailedReason
	}

	// Use equipment-identifier as fallback for IMEI
	if info.IMEI == "" {
		info.IMEI = modemResp.Modem.Generic.EquipmentIdentifier
	}

	if sq := modemResp.Modem.Generic.SignalQuality.Value; sq != "" {
		if v, err := strconv.Atoi(strings.TrimSuffix(sq, "%")); err == nil {
			info.SignalPercent = v
		}
	}

	// Parse network type from access technologies or current modes
	if len(modemResp.Modem.Generic.AccessTechnologies) > 0 {
		info.NetworkType = modemResp.Modem.Generic.AccessTechnologies[0]
	} else if modemResp.Modem.Generic.CurrentModes != "" {
		info.NetworkType = parseNetworkType(modemResp.Modem.Generic.CurrentModes)
	}

	if len(modemResp.Modem.Generic.OwnNumbers) > 0 {
		info.PhoneNumber = modemResp.Modem.Generic.OwnNumbers[0]
	}

	// Find network interface from ports
	for _, port := range modemResp.Modem.Generic.Ports {
		if strings.Contains(port, "(net)") {
			info.Interface = strings.Split(port, " ")[0]
			break
		}
	}

	// Get bearer info for IP and interface
	if len(modemResp.Modem.Generic.Bearers) > 0 {
		bearerPath := modemResp.Modem.Generic.Bearers[0]
		parts := strings.Split(bearerPath, "/")
		bearerID := parts[len(parts)-1]
		m.getBearerInfo(bearerID, info)
	}

	// Get data usage from interface
	if info.Interface != "" {
		m.getInterfaceStats(info.Interface, info)
	}

	return info, nil
}

func parseNetworkType(modes string) string {
	if strings.Contains(modes, "4g") || strings.Contains(modes, "lte") {
		return "LTE"
	}
	if strings.Contains(modes, "3g") || strings.Contains(modes, "umts") {
		return "3G"
	}
	if strings.Contains(modes, "2g") || strings.Contains(modes, "gsm") {
		return "2G"
	}
	return ""
}

func (m *Manager) getBearerInfo(bearerID string, info *ModemInfo) {
	cmd := exec.Command("mmcli", "-b", bearerID, "-J")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	var bearerResp struct {
		Bearer struct {
			Status struct {
				Interface string `json:"interface"`
			} `json:"status"`
			IPv4Config struct {
				Address string `json:"address"`
			} `json:"ipv4-config"`
		} `json:"bearer"`
	}

	if err := json.Unmarshal(output, &bearerResp); err != nil {
		return
	}

	info.Interface = bearerResp.Bearer.Status.Interface
	info.IPAddress = bearerResp.Bearer.IPv4Config.Address
}

func (m *Manager) getInterfaceStats(iface string, info *ModemInfo) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, iface+":") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 10 {
			continue
		}

		// Format: iface: rx_bytes rx_packets ... tx_bytes tx_packets ...
		// Index after "iface:": 0=rx_bytes, 8=tx_bytes
		rxBytes := strings.TrimPrefix(parts[0], iface+":")
		if rxBytes == "" && len(parts) > 1 {
			rxBytes = parts[1]
		}
		if v, err := strconv.ParseInt(strings.TrimSpace(rxBytes), 10, 64); err == nil {
			info.DataRx = v
		}

		txIdx := 9
		if len(parts) > txIdx {
			if v, err := strconv.ParseInt(parts[txIdx], 10, 64); err == nil {
				info.DataTx = v
			}
		}
		break
	}
}
