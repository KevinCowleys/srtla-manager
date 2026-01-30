package modem

import (
	"bufio"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type ADBProvider struct {
	available      bool
	usedInterfaces map[string]bool
}

func NewADBProvider() *ADBProvider {
	p := &ADBProvider{
		usedInterfaces: make(map[string]bool),
	}
	_, err := exec.LookPath("adb")
	p.available = err == nil
	return p
}

func (p *ADBProvider) IsAvailable() bool {
	return p.available
}

func (p *ADBProvider) ListDevices() ([]string, error) {
	if !p.available {
		return nil, nil
	}

	cmd := exec.Command("adb", "devices")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var devices []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "\tdevice") {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				devices = append(devices, parts[0])
			}
		}
	}
	return devices, nil
}

func (p *ADBProvider) GetModemInfo(deviceID string) (*ModemInfo, error) {
	if !p.available {
		return nil, nil
	}

	info := &ModemInfo{
		ID:    deviceID,
		State: "unknown",
	}

	// Get device model
	if model := p.getProp(deviceID, "ro.product.model"); model != "" {
		info.Model = model
	}
	if info.Model == "" {
		info.Model = p.getProp(deviceID, "ro.product.device")
	}

	// Get manufacturer
	info.Manufacturer = p.getProp(deviceID, "ro.product.manufacturer")

	// Get carrier/operator
	info.Carrier = p.getProp(deviceID, "gsm.operator.alpha")

	// Get network type
	info.NetworkType = p.normalizeNetworkType(p.getProp(deviceID, "gsm.network.type"))

	// Get IMEI (try multiple methods)
	info.IMEI = p.getIMEI(deviceID)

	// Get IMSI
	info.IMSI = p.getProp(deviceID, "gsm.sim.lte.imsi")
	if info.IMSI == "" {
		info.IMSI = p.getProp(deviceID, "gsm.sim.imsi")
	}

	// Get phone number if available (rarely populated on these dongles)
	info.PhoneNumber = p.getProp(deviceID, "gsm.sim.line1number")
	if info.PhoneNumber == "" {
		info.PhoneNumber = p.getProp(deviceID, "ril.msisdn")
	}

	// Get country
	info.Country = p.getProp(deviceID, "gsm.operator.iso-country")
	if info.Country == "" {
		info.Country = p.getProp(deviceID, "gsm.sim.operator.iso-country")
	}

	// Get SIM state
	simState := p.getProp(deviceID, "gsm.sim.state")

	// Get signal strength from telephony registry
	signalASU, networkFromSignal := p.getSignalStrength(deviceID)
	if signalASU >= 0 && signalASU < 99 {
		// Convert ASU to percentage (0-31 ASU range for GSM)
		info.SignalPercent = int((float64(signalASU) / 31.0) * 100)
		if info.SignalPercent > 100 {
			info.SignalPercent = 100
		}
		// Convert ASU to dBm: dBm = 2 * ASU - 113
		info.SignalDBm = 2*signalASU - 113
	}

	// Use network type from signal if main prop was unknown
	if info.NetworkType == "" || info.NetworkType == "Unknown" {
		info.NetworkType = networkFromSignal
	}

	// Get service state for more details
	serviceState := p.getServiceState(deviceID)

	// Determine overall state
	if simState == "READY" {
		if strings.Contains(serviceState, "home") || strings.Contains(serviceState, "roaming") {
			info.State = "registered"
		} else {
			info.State = "sim-ready"
		}
	} else if simState != "" {
		info.State = "sim-" + strings.ToLower(simState)
	}

	// Get IP address from device's rmnet interface
	info.IPAddress = p.getRMNetIP(deviceID)

	// Find the corresponding USB network interface on host
	info.Interface = p.findUSBInterface(deviceID)

	// Mark interface as used to prevent other devices from claiming it
	if info.Interface != "" {
		p.usedInterfaces[info.Interface] = true
	}

	// Get data usage if we have an interface
	if info.Interface != "" {
		p.getInterfaceStats(info.Interface, info)
	}

	return info, nil
}

func (p *ADBProvider) getProp(deviceID, prop string) string {
	cmd := exec.Command("adb", "-s", deviceID, "shell", "getprop", prop)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (p *ADBProvider) getIMEI(deviceID string) string {
	// Try getprop first
	imei := p.getProp(deviceID, "gsm.imei")
	if imei != "" && imei != "null" {
		return imei
	}

	// Try service call (may require root)
	cmd := exec.Command("adb", "-s", deviceID, "shell", "service", "call", "iphonesubinfo", "1")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse the service call output (looks like: Result: Parcel(00000000 00000010 00350033 ...)
	// The IMEI is encoded as UTF-16 characters
	re := regexp.MustCompile(`'([0-9.]+)'`)
	matches := re.FindAllStringSubmatch(string(output), -1)
	var imeiParts []string
	for _, m := range matches {
		if len(m) > 1 {
			// Remove dots and spaces
			part := strings.ReplaceAll(m[1], ".", "")
			part = strings.ReplaceAll(part, " ", "")
			imeiParts = append(imeiParts, part)
		}
	}
	if len(imeiParts) > 0 {
		return strings.Join(imeiParts, "")
	}

	return ""
}

func (p *ADBProvider) getSignalStrength(deviceID string) (int, string) {
	cmd := exec.Command("adb", "-s", deviceID, "shell", "dumpsys", "telephony.registry")
	output, err := cmd.Output()
	if err != nil {
		return -1, ""
	}

	var asu int = -1
	var networkType string

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "mSignalStrength=SignalStrength:") {
			// Format: SignalStrength: GSM_ASU BER CDMA_DBM ... gsm|lte
			parts := strings.Split(line, "SignalStrength:")
			if len(parts) > 1 {
				fields := strings.Fields(parts[1])
				if len(fields) > 0 {
					if v, err := strconv.Atoi(fields[0]); err == nil {
						asu = v
					}
				}
				// Last field often indicates network type
				if len(fields) > 0 {
					lastField := fields[len(fields)-1]
					networkType = p.normalizeNetworkType(lastField)
				}
			}
		}
	}

	return asu, networkType
}

func (p *ADBProvider) getServiceState(deviceID string) string {
	cmd := exec.Command("adb", "-s", deviceID, "shell", "dumpsys", "telephony.registry")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "mServiceState=") {
			return line
		}
	}
	return ""
}

func (p *ADBProvider) normalizeNetworkType(rawType string) string {
	rawType = strings.ToLower(rawType)

	switch {
	case strings.Contains(rawType, "lte"):
		return "LTE"
	case strings.Contains(rawType, "nr") || strings.Contains(rawType, "5g"):
		return "5G"
	case strings.Contains(rawType, "hsdpa") || strings.Contains(rawType, "hsupa") ||
		strings.Contains(rawType, "hspa") || strings.Contains(rawType, "umts"):
		return "3G"
	case strings.Contains(rawType, "edge"):
		return "EDGE"
	case strings.Contains(rawType, "gprs"):
		return "GPRS"
	case strings.Contains(rawType, "gsm"):
		return "2G"
	case rawType == "unknown" || rawType == "":
		return ""
	default:
		return strings.ToUpper(rawType)
	}
}

func (p *ADBProvider) getRMNetIP(deviceID string) string {
	cmd := exec.Command("adb", "-s", deviceID, "shell", "ip", "addr", "show", "rmnet0")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Look for inet line
	re := regexp.MustCompile(`inet\s+(\d+\.\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func (p *ADBProvider) findUSBInterface(deviceID string) string {
	// Try to find the USB network interface associated with this device
	// This is tricky because Linux names them based on USB path

	// First, get the USB path from adb devices -l
	cmd := exec.Command("adb", "devices", "-l")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	var usbPath string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, deviceID) {
			// Look for usb:X-Y.Z pattern
			re := regexp.MustCompile(`usb:(\S+)`)
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				usbPath = matches[1]
			}
			break
		}
	}

	if usbPath == "" {
		return ""
	}

	// Look for network interface matching this USB path
	// Interface names like enp102s0f3u1u1 contain USB topology info
	cmd = exec.Command("ls", "/sys/class/net")
	output, err = cmd.Output()
	if err != nil {
		return ""
	}

	// Try to match USB interface
	for _, iface := range strings.Fields(string(output)) {
		if strings.HasPrefix(iface, "enp") || strings.HasPrefix(iface, "usb") {
			// Skip if already used by another device
			if p.usedInterfaces[iface] {
				continue
			}

			// Check if this interface's device path contains our USB path
			linkPath := "/sys/class/net/" + iface + "/device"
			cmd = exec.Command("readlink", "-f", linkPath)
			linkOutput, err := cmd.Output()
			if err == nil {
				// USB path format: 1-1.1 -> look for usb1/1-1/1-1.1
				usbPathNorm := strings.ReplaceAll(usbPath, ".", "/")
				if strings.Contains(string(linkOutput), usbPath) ||
					strings.Contains(string(linkOutput), usbPathNorm) {
					return iface
				}
			}
		}
	}

	// No match found - return empty instead of guessing
	return ""
}

func (p *ADBProvider) getInterfaceStats(iface string, info *ModemInfo) {
	// Read RX bytes
	rxPath := "/sys/class/net/" + iface + "/statistics/rx_bytes"
	cmd := exec.Command("cat", rxPath)
	if output, err := cmd.Output(); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64); err == nil {
			info.DataRx = v
		}
	}

	// Read TX bytes
	txPath := "/sys/class/net/" + iface + "/statistics/tx_bytes"
	cmd = exec.Command("cat", txPath)
	if output, err := cmd.Output(); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64); err == nil {
			info.DataTx = v
		}
	}
}
