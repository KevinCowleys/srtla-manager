package usbnet

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// NMClient manages NetworkManager connections via nmcli.
type NMClient struct {
	log Logger
}

// NewNMClient creates a NetworkManager client.
func NewNMClient(log Logger) *NMClient {
	return &NMClient{log: log}
}

// IsAvailable checks if nmcli is installed and working.
func (c *NMClient) IsAvailable() bool {
	// nmcli binary must exist
	if err := exec.Command("nmcli", "--version").Run(); err != nil {
		return false
	}

	// NetworkManager must be running
	cmd := exec.Command("nmcli", "-t", "-f", "running", "general")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "running"
}

// EnsureConnection creates or updates a connection for a device with a unique DHCP client ID.
// This prevents multiple devices from getting the same DHCP lease.
func (c *NMClient) EnsureConnection(dev *DeviceStatus) error {
	if !c.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	connName := c.getConnectionName(dev)

	// Check if connection exists
	if c.connectionExists(connName) {
		c.log.Printf("connection %s already exists, skipping creation", connName)
		return nil
	}

	// Create a new connection with unique DHCP client ID
	clientID := c.getClientID(dev)
	c.log.Printf("creating connection %s for device %s (client-id: %s)", connName, dev.Serial, clientID)

	// Create connection with unique DHCP client ID
	cmd := exec.Command("nmcli", "connection", "add",
		"type", "ethernet",
		"ifname", dev.Interface,
		"con-name", connName,
		"connection.autoconnect", "yes",
		"ipv4.method", "auto",
		"ipv4.dhcp-client-id", clientID,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create connection: %w: %s", err, string(output))
	}

	c.log.Printf("created connection %s: %s", connName, strings.TrimSpace(string(output)))
	return nil
}

// ActivateConnection brings up a connection.
func (c *NMClient) ActivateConnection(dev *DeviceStatus) error {
	if !c.IsAvailable() {
		return fmt.Errorf("nmcli not available")
	}

	connName := c.getConnectionName(dev)

	// Try to activate the connection
	cmd := exec.Command("nmcli", "connection", "up", connName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to activate connection %s: %w: %s", connName, err, string(output))
	}

	c.log.Printf("activated connection %s: %s", connName, strings.TrimSpace(string(output)))
	return nil
}

// BringUpInterface brings up an interface with DHCP.
// It prefers using NetworkManager's nmcli, falling back to dhclient only when necessary.
func (c *NMClient) BringUpInterface(iface string) error {
	// First, bring up the link
	cmd := exec.Command("ip", "link", "set", iface, "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring up %s: %w: %s", iface, err, string(output))
	}
	c.log.Printf("brought up interface %s", iface)

	// Brief delay after link up
	time.Sleep(500 * time.Millisecond)

	// Try nmcli first if available (preferred - works with NetworkManager)
	if c.IsAvailable() {
		// Use nmcli to request DHCP via device reapply
		nmCmd := exec.Command("nmcli", "device", "reapply", iface)
		if nmOutput, nmErr := nmCmd.CombinedOutput(); nmErr == nil {
			c.log.Printf("nmcli reapply succeeded for %s", iface)
			// Give NetworkManager time to get DHCP
			time.Sleep(2 * time.Second)
		} else {
			c.log.Printf("nmcli reapply failed for %s: %v: %s", iface, nmErr, string(nmOutput))
		}
	}

	// Check if we already have an IP (NetworkManager may have already configured it)
	if c.hasIPv4(iface) {
		return nil
	}

	// Fallback to dhclient if nmcli didn't work
	c.log.Printf("falling back to dhclient for %s", iface)

	// Kill any existing dhclient for this interface to avoid conflicts
	killCmd := exec.Command("pkill", "-f", fmt.Sprintf("dhclient.*%s", iface))
	killCmd.Run() // Ignore errors - might not be running

	time.Sleep(200 * time.Millisecond)

	dhcpCmd := exec.Command("dhclient", "-1", "-v", iface) // -1 = try once, -v = verbose
	dhcpOutput, dhcpErr := dhcpCmd.CombinedOutput()
	if dhcpErr != nil {
		return fmt.Errorf("dhclient for %s failed: %v (output: %s)", iface, dhcpErr, string(dhcpOutput))
	}

	// Verify we actually obtained an IPv4 address
	if c.hasIPv4(iface) {
		return nil
	}

	return fmt.Errorf("no IPv4 lease on %s after dhclient", iface)
}

// hasIPv4 checks if an interface has an IPv4 address.
func (c *NMClient) hasIPv4(iface string) bool {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return false
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return true
		}
	}
	return false
}

// connectionExists checks if a connection with the given name exists.
func (c *NMClient) connectionExists(connName string) bool {
	cmd := exec.Command("nmcli", "connection", "show", connName)
	err := cmd.Run()
	return err == nil
}

// getConnectionName returns the NM connection name for a device.
func (c *NMClient) getConnectionName(dev *DeviceStatus) string {
	// Use format: srtla-{serial} or fallback to interface name
	if dev.Serial != "" && !strings.HasPrefix(dev.Serial, "unknown-") {
		return fmt.Sprintf("srtla-%s", dev.Serial)
	}
	return fmt.Sprintf("srtla-%s", dev.Interface)
}

// getClientID returns a unique DHCP client ID for a device.
// This prevents DHCP servers from assigning the same lease to multiple devices.
func (c *NMClient) getClientID(dev *DeviceStatus) string {
	// Use serial + MAC for uniqueness
	// Format: srtla-{serial}_{mac_short}
	if dev.Serial != "" && !strings.HasPrefix(dev.Serial, "unknown-") {
		// Abbreviate MAC to last 4 chars for brevity
		macShort := dev.MAC
		if idx := strings.LastIndex(dev.MAC, ":"); idx >= 0 {
			macShort = dev.MAC[idx-5:]
		}
		return fmt.Sprintf("srtla-%s-%s", dev.Serial, macShort)
	}
	// Fallback: use MAC
	return fmt.Sprintf("srtla-%s", dev.MAC)
}

// GetConnectionStatus returns detailed info about a connection.
func (c *NMClient) GetConnectionStatus(connName string) (map[string]string, error) {
	cmd := exec.Command("nmcli", "--terse", "connection", "show", connName)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	status := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			status[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}

	return status, nil
}
