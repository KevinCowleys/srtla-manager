package system

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type DependencyStatus struct {
	Name           string `json:"name"`
	Installed      bool   `json:"installed"`
	Path           string `json:"path"`
	Version        string `json:"version"`
	InstallCommand string `json:"install_command"`
}

type NetworkInterface struct {
	Name       string   `json:"name"`
	IPs        []string `json:"ips"`
	IsUp       bool     `json:"is_up"`
	IsLoopback bool     `json:"is_loopback"`
}

func CheckFFmpeg() DependencyStatus {
	status := DependencyStatus{
		Name:           "ffmpeg",
		InstallCommand: getFFmpegInstallCommand(),
	}

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return status
	}

	status.Installed = true
	status.Path = path

	cmd := exec.Command("ffmpeg", "-version")
	output, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(output), "\n")
		if len(lines) > 0 {
			parts := strings.Fields(lines[0])
			if len(parts) >= 3 {
				status.Version = parts[2]
			}
		}
	}

	return status
}

func CheckSRTLA(binaryPath string) DependencyStatus {
	status := DependencyStatus{
		Name:           "srtla_send",
		InstallCommand: getSRTLAInstallCommand(),
	}

	searchPaths := []string{binaryPath}
	if binaryPath == "" || binaryPath == "srtla_send" {
		if runtime.GOOS == "windows" {
			if p, err := exec.LookPath("srtla_send.exe"); err == nil {
				searchPaths = append(searchPaths, p)
			}
		} else {
			if p, err := exec.LookPath("srtla_send"); err == nil {
				searchPaths = append(searchPaths, p)
			}
			searchPaths = append(searchPaths, "/usr/local/bin/srtla_send", "/usr/bin/srtla_send")
		}
	}

	for _, p := range searchPaths {
		if p == "" {
			continue
		}
		absPath, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if _, err := os.Stat(absPath); err == nil {
			status.Installed = true
			status.Path = absPath

			cmd := exec.Command(absPath, "-v")
			output, err := cmd.Output()
			if err == nil {
				status.Version = strings.TrimSpace(string(output))
			}
			break
		}
	}

	return status
}

func ListNetworkInterfaces() []NetworkInterface {
	var interfaces []NetworkInterface

	ifaces, err := net.Interfaces()
	if err != nil {
		return interfaces
	}

	for _, iface := range ifaces {
		ni := NetworkInterface{
			Name:       iface.Name,
			IsUp:       iface.Flags&net.FlagUp != 0,
			IsLoopback: iface.Flags&net.FlagLoopback != 0,
			IPs:        []string{},
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil {
				continue
			}

			if ip4 := ip.To4(); ip4 != nil {
				ni.IPs = append(ni.IPs, ip4.String())
			}
		}

		if len(ni.IPs) > 0 || ni.IsUp {
			interfaces = append(interfaces, ni)
		}
	}

	return interfaces
}

// GetFirstNonLoopbackIP returns the first non-loopback IPv4 address found on the system.
// Prefers non-private IPs, but will return private IPs if no public IP is available.
func GetFirstNonLoopbackIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var privateIP string

	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil {
				continue
			}

			// Get IPv4 only
			if ip4 := ip.To4(); ip4 != nil {
				ipStr := ip4.String()

				// Check if it's a private IP (10.x, 172.16-31.x, 192.168.x)
				isPrivate := ip4[0] == 10 ||
					(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
					(ip4[0] == 192 && ip4[1] == 168)

				if isPrivate {
					// Save first private IP as fallback
					if privateIP == "" {
						privateIP = ipStr
					}
				} else {
					// Return first non-private IP immediately
					return ipStr
				}
			}
		}
	}

	// Return private IP if no public IP was found
	if privateIP != "" {
		return privateIP
	}

	return ""
}

func getFFmpegInstallCommand() string {
	switch detectOS() {
	case "windows":
		return "winget install ffmpeg"
	case "debian", "ubuntu":
		return "sudo apt install ffmpeg"
	case "fedora":
		return "sudo dnf install ffmpeg"
	case "arch":
		return "sudo pacman -S ffmpeg"
	case "darwin":
		return "brew install ffmpeg"
	case "alpine":
		return "sudo apk add ffmpeg"
	default:
		return "# Install ffmpeg using your package manager"
	}
}

func getSRTLAInstallCommand() string {
	switch detectOS() {
	case "debian", "ubuntu":
		return `# Download .deb from https://github.com/irlserver/srtla_send/releases
sudo dpkg -i srtla_*.deb`
	case "windows":
		return `# Download from https://github.com/irlserver/srtla_send/releases`
	default:
		return `# Download from https://github.com/irlserver/srtla_send/releases`
	}
}

func detectOS() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "darwin"
	}

	if runtime.GOOS != "linux" {
		return runtime.GOOS
	}

	releaseFile := "/etc/os-release"
	file, err := os.Open(releaseFile)
	if err != nil {
		return "linux"
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ID=") {
			id := strings.TrimPrefix(line, "ID=")
			id = strings.Trim(id, "\"")
			return strings.ToLower(id)
		}
	}

	if _, err := os.Stat("/etc/debian_version"); err == nil {
		return "debian"
	}
	if _, err := os.Stat("/etc/fedora-release"); err == nil {
		return "fedora"
	}
	if _, err := os.Stat("/etc/arch-release"); err == nil {
		return "arch"
	}

	return "linux"
}

func GetOSInfo() string {
	return detectOS()
}
