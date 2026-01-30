package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	RTMP       RTMPConfig                 `yaml:"rtmp" json:"rtmp"`
	SRT        SRTConfig                  `yaml:"srt" json:"srt"`
	SRTLA      SRTLAConfig                `yaml:"srtla" json:"srtla"`
	Web        WebConfig                  `yaml:"web" json:"web"`
	Cameras    map[string]CameraConfig    `yaml:"cameras" json:"cameras"`
	USBCameras map[string]USBCameraConfig `yaml:"usb_cameras" json:"usb_cameras"`
}

type RTMPConfig struct {
	ListenPort int    `yaml:"listen_port" json:"listen_port"`
	StreamKey  string `yaml:"stream_key" json:"stream_key"`
}

type SRTConfig struct {
	LocalPort int `yaml:"local_port" json:"local_port"`
}

type SRTLAConfig struct {
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	BinaryPath  string   `yaml:"binary_path" json:"binary_path"`
	RemoteHost  string   `yaml:"remote_host" json:"remote_host"`
	RemotePort  int      `yaml:"remote_port" json:"remote_port"`
	BindIPs     []string `yaml:"bind_ips" json:"bind_ips"`
	BindIPsFile string   `yaml:"bind_ips_file" json:"bind_ips_file"`
	Classic     bool     `yaml:"classic" json:"classic"`
	NoQuality   bool     `yaml:"no_quality" json:"no_quality"`
	Exploration bool     `yaml:"exploration" json:"exploration"`
}

type WebConfig struct {
	Port int `yaml:"port" json:"port"`
}

type CameraConfig struct {
	Name         string `yaml:"name" json:"name"`
	RTMPUrl      string `yaml:"rtmp_url" json:"rtmp_url"`
	WiFiSSID     string `yaml:"wifi_ssid" json:"wifi_ssid"`
	WiFiPassword string `yaml:"wifi_password" json:"wifi_password"`
}

// USBCameraConfig stores configuration for USB webcams
type USBCameraConfig struct {
	Name    string `yaml:"name" json:"name"`
	Width   int    `yaml:"width" json:"width"`
	Height  int    `yaml:"height" json:"height"`
	FPS     int    `yaml:"fps" json:"fps"`
	Bitrate int    `yaml:"bitrate" json:"bitrate"` // kbps
	Encoder string `yaml:"encoder" json:"encoder"` // libx264, h264_vaapi, h264_nvenc, copy
}

type Manager struct {
	mu       sync.RWMutex
	config   *Config
	filePath string
}

func NewManager(filePath string) *Manager {
	return &Manager{
		filePath: filePath,
	}
}

func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			m.config = DefaultConfig()
			return m.saveUnsafe()
		}
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	m.config = &cfg
	return nil
}

func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveUnsafe()
}

func (m *Manager) saveUnsafe() error {
	data, err := yaml.Marshal(m.config)
	if err != nil {
		return err
	}
	// Use 0600 permissions since config may contain sensitive data like stream keys
	return os.WriteFile(m.filePath, data, 0600)
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.config
}

func (m *Manager) Update(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = &cfg
	return m.saveUnsafe()
}

func (m *Manager) UpdateBindIPs(ips []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.SRTLA.BindIPs = ips
	return m.saveUnsafe()
}

func (m *Manager) UpdateBindIPsFile(filePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.SRTLA.BindIPsFile = filePath
	return m.saveUnsafe()
}

func (m *Manager) LoadBindIPsFromFile() ([]string, error) {
	m.mu.RLock()
	filePath := m.config.SRTLA.BindIPsFile
	m.mu.RUnlock()

	if filePath == "" {
		return nil, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var ips []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			ips = append(ips, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.config.SRTLA.BindIPs = ips
	m.mu.Unlock()

	return ips, nil
}

func (m *Manager) SaveBindIPsToFile() error {
	m.mu.RLock()
	filePath := m.config.SRTLA.BindIPsFile
	ips := m.config.SRTLA.BindIPs
	m.mu.RUnlock()

	if filePath == "" {
		return nil
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	content := strings.Join(ips, "\n")
	if len(ips) > 0 {
		content += "\n"
	}

	return os.WriteFile(filePath, []byte(content), 0644)
}

// Validate checks if the configuration is valid and returns detailed errors
func (c *Config) Validate() error {
	var errors []string

	// Validate RTMP port
	if c.RTMP.ListenPort < 1 || c.RTMP.ListenPort > 65535 {
		errors = append(errors, fmt.Sprintf("RTMP port %d is invalid (must be 1-65535)", c.RTMP.ListenPort))
	}

	// Validate SRT port
	if c.SRT.LocalPort < 1 || c.SRT.LocalPort > 65535 {
		errors = append(errors, fmt.Sprintf("SRT port %d is invalid (must be 1-65535)", c.SRT.LocalPort))
	}

	// Validate Web port
	if c.Web.Port < 1 || c.Web.Port > 65535 {
		errors = append(errors, fmt.Sprintf("Web port %d is invalid (must be 1-65535)", c.Web.Port))
	}

	// Validate SRTLA configuration when enabled
	if c.SRTLA.Enabled {
		// Validate remote host
		if c.SRTLA.RemoteHost == "" {
			errors = append(errors, "SRTLA remote host is required when SRTLA is enabled")
		}

		// Validate remote port
		if c.SRTLA.RemotePort < 1 || c.SRTLA.RemotePort > 65535 {
			errors = append(errors, fmt.Sprintf("SRTLA remote port %d is invalid (must be 1-65535)", c.SRTLA.RemotePort))
		}
	}

	// Validate bind IPs if SRTLA is enabled and bind IPs are configured
	// Note: we don't validate the binary path here - it will be checked at runtime
	// when SRTLA is actually started
	if c.SRTLA.Enabled && len(c.SRTLA.BindIPs) > 0 {
		// Validate bind IP addresses
		for i, ip := range c.SRTLA.BindIPs {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			if net.ParseIP(ip) == nil {
				errors = append(errors, fmt.Sprintf("bind IP #%d '%s' is not a valid IP address", i+1, ip))
			}
		}

		// Check for duplicate bind IPs
		seenIPs := make(map[string]bool)
		for _, ip := range c.SRTLA.BindIPs {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			if seenIPs[ip] {
				errors = append(errors, fmt.Sprintf("duplicate bind IP: %s", ip))
			}
			seenIPs[ip] = true
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

// SaveCameraConfig saves or updates camera configuration by MAC address
func (m *Manager) SaveCameraConfig(address string, cfg CameraConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.Cameras == nil {
		m.config.Cameras = make(map[string]CameraConfig)
	}

	m.config.Cameras[address] = cfg
	return m.saveUnsafe()
}

// LoadCameraConfig retrieves camera configuration by MAC address
func (m *Manager) LoadCameraConfig(address string) (CameraConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.Cameras == nil {
		return CameraConfig{}, false
	}

	cfg, ok := m.config.Cameras[address]
	return cfg, ok
}

// GetAllCameraConfigs returns all saved camera configurations
func (m *Manager) GetAllCameraConfigs() map[string]CameraConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.Cameras == nil {
		return make(map[string]CameraConfig)
	}

	// Return a copy to avoid external modification
	cameras := make(map[string]CameraConfig)
	for k, v := range m.config.Cameras {
		cameras[k] = v
	}
	return cameras
}

// DeleteCameraConfig removes camera configuration by MAC address
func (m *Manager) DeleteCameraConfig(address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.Cameras != nil {
		delete(m.config.Cameras, address)
		return m.saveUnsafe()
	}
	return nil
}

// SaveUSBCameraConfig saves or updates USB camera configuration by device ID
func (m *Manager) SaveUSBCameraConfig(deviceID string, cfg USBCameraConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.USBCameras == nil {
		m.config.USBCameras = make(map[string]USBCameraConfig)
	}

	m.config.USBCameras[deviceID] = cfg
	return m.saveUnsafe()
}

// LoadUSBCameraConfig retrieves USB camera configuration by device ID
func (m *Manager) LoadUSBCameraConfig(deviceID string) (USBCameraConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.USBCameras == nil {
		return USBCameraConfig{}, false
	}

	cfg, ok := m.config.USBCameras[deviceID]
	return cfg, ok
}

// GetAllUSBCameraConfigs returns all saved USB camera configurations
func (m *Manager) GetAllUSBCameraConfigs() map[string]USBCameraConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config.USBCameras == nil {
		return make(map[string]USBCameraConfig)
	}

	// Return a copy to avoid external modification
	cameras := make(map[string]USBCameraConfig)
	for k, v := range m.config.USBCameras {
		cameras[k] = v
	}
	return cameras
}

// DeleteUSBCameraConfig removes USB camera configuration by device ID
func (m *Manager) DeleteUSBCameraConfig(deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.USBCameras != nil {
		delete(m.config.USBCameras, deviceID)
		return m.saveUnsafe()
	}
	return nil
}

func DefaultConfig() *Config {
	return &Config{
		RTMP: RTMPConfig{
			ListenPort: 1935,
			StreamKey:  "live",
		},
		SRT: SRTConfig{
			LocalPort: 6000,
		},
		SRTLA: SRTLAConfig{
			Enabled:    true,
			BinaryPath: "srtla_send",
			RemoteHost: "localhost",
			RemotePort: 5000,
			BindIPs:    []string{},
		},
		Web: WebConfig{
			Port: 8080,
		},
		Cameras:    make(map[string]CameraConfig),
		USBCameras: make(map[string]USBCameraConfig),
	}
}
