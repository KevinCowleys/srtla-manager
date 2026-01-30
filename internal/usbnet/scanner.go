package usbnet

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Scanner discovers USB RNDIS devices and their network interfaces.
type Scanner struct {
	log Logger
}

// Logger is a minimal logging interface.
type Logger interface {
	Printf(format string, v ...interface{})
}

// NewScanner creates a USB device scanner.
func NewScanner(log Logger) *Scanner {
	return &Scanner{log: log}
}

// Scan discovers connected USB RNDIS devices and returns their status.
func (s *Scanner) Scan() []DeviceStatus {
	var devices []DeviceStatus

	// List all network interfaces
	ifaces, err := net.Interfaces()
	if err != nil {
		s.log.Printf("failed to list interfaces: %v", err)
		return devices
	}

	// Scan for USB-related interfaces (usb*, enp*u*)
	for _, iface := range ifaces {
		if !s.isUSBInterface(iface.Name) {
			continue
		}

		dev := s.scanInterface(iface)
		if dev != nil {
			devices = append(devices, *dev)
		}
	}

	return devices
}

// isUSBInterface checks if an interface is likely a USB device.
func (s *Scanner) isUSBInterface(name string) bool {
	// Check for common USB interface naming patterns:
	// - usb0, usb1 (direct USB)
	// - enp102s0f3u1u1 (USB topology in name)
	// - enp*u* (USB RNDIS devices)
	// - enx... (MAC-based USB naming)
	// - wwan0/wwan1 (cellular data interfaces often exposed by modems)
	if strings.HasPrefix(name, "usb") {
		return true
	}
	if strings.HasPrefix(name, "enp") && strings.Contains(name, "u") {
		return true
	}
	if strings.HasPrefix(name, "enx") {
		return true
	}
	if strings.HasPrefix(name, "wwan") {
		return true
	}
	return false
}

// scanInterface gathers info about a single interface.
func (s *Scanner) scanInterface(iface net.Interface) *DeviceStatus {
	dev := &DeviceStatus{
		Interface: iface.Name,
		State:     "disconnected",
		LastSeen:  time.Now(),
	}

	// Get MAC address
	if len(iface.HardwareAddr) > 0 {
		dev.MAC = iface.HardwareAddr.String()
	}

	// Check if interface is up
	isUp := iface.Flags&net.FlagUp != 0
	if isUp {
		dev.State = "pending"
	}

	// Get IP addresses
	addrs, err := iface.Addrs()
	if err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				dev.IPv4 = ipnet.IP.String()
				if isUp {
					dev.State = "connected"
				}
				break
			}
		}
	}

	// Try to get device serial from sysfs/udev
	serial := s.getDeviceSerial(iface.Name)
	if serial != "" {
		dev.Serial = serial
	} else {
		// Fallback: use interface name as serial placeholder
		dev.Serial = fmt.Sprintf("unknown-%s", iface.Name)
	}

	return dev
}

// getDeviceSerial attempts to find the USB device serial via sysfs and udev.
func (s *Scanner) getDeviceSerial(ifname string) string {
	// Try multiple methods to get the serial

	// Method 1: Check sysfs for serial_number
	serial := s.getSerialFromSysfs(ifname)
	if serial != "" {
		return serial
	}

	// Method 2: Use udevadm to query device properties
	serial = s.getSerialFromUdevadm(ifname)
	if serial != "" {
		return serial
	}

	// Method 3: Try to find via adb (if connected as ADB)
	serial = s.getSerialFromADB(ifname)
	if serial != "" {
		return serial
	}

	return ""
}

// getSerialFromSysfs looks for ID_SERIAL_SHORT in sysfs.
func (s *Scanner) getSerialFromSysfs(ifname string) string {
	// /sys/class/net/ifname/device/serial
	paths := []string{
		filepath.Join("/sys/class/net", ifname, "device", "serial"),
		filepath.Join("/sys/class/net", ifname, "device", "uevent"),
		filepath.Join("/sys/class/net", ifname, "device"),
	}

	for _, p := range paths {
		// Try direct serial file first
		if strings.HasSuffix(p, "serial") {
			if data, err := os.ReadFile(p); err == nil {
				if content := strings.TrimSpace(string(data)); content != "" {
					return content
				}
			}
		}

		// For directories, list and find relevant files
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			entries, err := os.ReadDir(p)
			if err == nil {
				for _, entry := range entries {
					name := entry.Name()
					if strings.Contains(name, "serial") {
						fullPath := filepath.Join(p, name)
						if data, err := os.ReadFile(fullPath); err == nil {
							if content := strings.TrimSpace(string(data)); content != "" {
								s.log.Printf("found serial via %s: %s", fullPath, content)
								return content
							}
						}
					}
				}
			}
		}

		// Parse uevent file for ID_SERIAL fields
		if strings.HasSuffix(p, "uevent") {
			if data, err := os.ReadFile(p); err == nil {
				content := string(data)
				for _, line := range strings.Split(content, "\n") {
					if strings.HasPrefix(line, "ID_SERIAL_SHORT=") {
						val := strings.TrimPrefix(line, "ID_SERIAL_SHORT=")
						if val != "" {
							return val
						}
					}
					if strings.HasPrefix(line, "SERIAL=") {
						val := strings.TrimPrefix(line, "SERIAL=")
						if val != "" {
							return val
						}
					}
				}
			}
		}
	}

	return ""
}

// getSerialFromUdevadm uses udevadm to query device properties.
func (s *Scanner) getSerialFromUdevadm(ifname string) string {
	cmd := exec.Command("udevadm", "info", "-e")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse udev database output looking for this interface
	blocks := strings.Split(string(output), "\n\n")
	for _, block := range blocks {
		if !strings.Contains(block, ifname) {
			continue
		}

		// Extract ID_SERIAL_SHORT if present
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "E: ID_SERIAL_SHORT=") {
				return strings.TrimPrefix(line, "E: ID_SERIAL_SHORT=")
			}
			if strings.HasPrefix(line, "E: ID_SERIAL=") {
				val := strings.TrimPrefix(line, "E: ID_SERIAL=")
				// Extract just the short part (after last underscore/dash)
				if idx := strings.LastIndex(val, "_"); idx >= 0 {
					return val[idx+1:]
				}
				return val
			}
		}
	}

	return ""
}

// getSerialFromADB tries to match the interface to an ADB device.
func (s *Scanner) getSerialFromADB(ifname string) string {
	// If this interface is associated with an ADB device, try to get its serial
	cmd := exec.Command("adb", "devices")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse adb devices output
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == "device" {
			serial := parts[0]
			// Try to verify this device matches our interface by checking USB path
			if s.verifyADBDeviceForInterface(serial, ifname) {
				return serial
			}
		}
	}

	return ""
}

// verifyADBDeviceForInterface checks if an ADB device might be associated with this interface.
func (s *Scanner) verifyADBDeviceForInterface(adbSerial, ifname string) bool {
	// This is a heuristic check - in a full implementation we'd match USB bus paths
	// For now, just accept it if the ADB device exists
	cmd := exec.Command("adb", "-s", adbSerial, "shell", "echo", "ok")
	err := cmd.Run()
	return err == nil
}
