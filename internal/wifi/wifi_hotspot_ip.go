package wifi

import (
	"os/exec"
	"strings"
)

// GetHotspotIP returns the IP address of the active hotspot interface.
// Returns empty string if no hotspot is active.
func (m *Manager) GetHotspotIP() string {
	// Look for active hotspot connection
	cmd := exec.Command("nmcli", "-t", "-f", "NAME,DEVICE", "con", "show", "--active")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	var hotspotDevice string
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) >= 2 {
			connName := strings.TrimSpace(parts[0])
			device := strings.TrimSpace(parts[1])
			// Look for hotspot connections (starts with "srtla-hotspot-")
			if strings.HasPrefix(connName, "srtla-hotspot-") && device != "" {
				hotspotDevice = device
				break
			}
		}
	}

	if hotspotDevice == "" {
		return ""
	}

	// Get IP address - hotspots in shared mode typically use 10.42.0.1
	cmd = exec.Command("ip", "-4", "addr", "show", hotspotDevice)
	output, err = cmd.Output()
	if err != nil {
		return "10.42.0.1" // Default hotspot IP
	}

	// Parse output for inet address
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				addr := parts[1]
				if idx := strings.Index(addr, "/"); idx > 0 {
					return addr[:idx]
				}
				return addr
			}
		}
	}

	return "10.42.0.1" // Default hotspot IP
}
