package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"srtla-manager/internal/config"
	"srtla-manager/internal/dji"
	"srtla-manager/internal/modem"
	"srtla-manager/internal/process"
	"srtla-manager/internal/stats"
	"srtla-manager/internal/system"
	"srtla-manager/internal/usbcam"
	"srtla-manager/internal/usbnet"
	"srtla-manager/internal/wifi"
)

type PipelineMode string

const (
	PipelineModeIdle      PipelineMode = "idle"
	PipelineModeReceiving PipelineMode = "receiving"
	PipelineModeStreaming PipelineMode = "streaming"
)

type Handler struct {
	config        *config.Manager
	ffmpeg        *process.FFmpegHandler
	srtla         *process.SRTLAHandler
	modem         *modem.Manager
	usb           *usbnet.Service
	stats         *stats.Collector
	logs          *stats.LogBuffer
	wsHub         *Hub
	startTime     time.Time
	wifiMgr       *wifi.Manager
	pipelineMu    sync.RWMutex
	pipelineMode  PipelineMode
	activeBindIPs []string
	previewDir    string
	appVersion    string

	djiScanner    *dji.Scanner
	djiController *dji.Controller

	usbCamScanner    *usbcam.Scanner
	usbCamController *usbcam.Controller

	restartTrackerMu sync.RWMutex
	ffmpegRestarts   *RestartTracker
	srtlaRestarts    *RestartTracker
}

type RestartTracker struct {
	latestFailTime  time.Time
	failureCount    int
	backoffDuration time.Duration
}

const (
	FFmpegStaleThreshold = 6 * time.Second
	SRTLAStaleThreshold  = 12 * time.Second

	InitialBackoff         = 2 * time.Second
	MaxBackoff             = 30 * time.Second
	BackoffMultiplier      = 1.5
	ResetFailureCountAfter = 60 * time.Second
)

func NewHandler(cfg *config.Manager, ff *process.FFmpegHandler, sr *process.SRTLAHandler, mm *modem.Manager, un *usbnet.Service, st *stats.Collector, lg *stats.LogBuffer, hub *Hub, wm *wifi.Manager) *Handler {
	djiScanner := dji.NewScanner()
	usbCamScanner := usbcam.NewScanner()
	usbCamController := usbcam.NewController(usbCamScanner)

	h := &Handler{
		config:           cfg,
		ffmpeg:           ff,
		srtla:            sr,
		modem:            mm,
		usb:              un,
		stats:            st,
		logs:             lg,
		wsHub:            hub,
		startTime:        time.Now(),
		wifiMgr:          wm,
		djiScanner:       djiScanner,
		djiController:    dji.NewController(djiScanner),
		usbCamScanner:    usbCamScanner,
		usbCamController: usbCamController,
		previewDir:       "/tmp/srtla-preview",
		ffmpegRestarts:   &RestartTracker{backoffDuration: InitialBackoff},
		srtlaRestarts:    &RestartTracker{backoffDuration: InitialBackoff},
	}

	// Initialize USB camera controller with FFmpeg handlers
	h.initUSBCamController()

	return h
}

// ========== Types ==========

type StatusResponse struct {
	Uptime       int64             `json:"uptime"`
	PipelineMode PipelineMode      `json:"pipeline_mode"`
	FFmpeg       FFmpegStatus      `json:"ffmpeg"`
	SRTLA        SRTLAStatus       `json:"srtla"`
	History      []stats.DataPoint `json:"history"`
}

type FFmpegStatus struct {
	ProcessState string              `json:"process_state"`
	State        process.FFmpegState `json:"state"`
	Bitrate      float64             `json:"bitrate"`
	FPS          float64             `json:"fps"`
	Stale        bool                `json:"stale"`
}

type SRTLAStatus struct {
	ProcessState string                    `json:"process_state"`
	State        process.SRTLAState        `json:"state"`
	Bitrate      float64                   `json:"bitrate"`
	Connections  []process.ConnectionStats `json:"connections"`
	Stale        bool                      `json:"stale"`
}

type CameraListResponse struct {
	Cameras  []*CameraInfo `json:"cameras"`
	Scanning bool          `json:"scanning"`
}

type CameraInfo struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Model       string               `json:"model"`
	RSSI        int                  `json:"rssi"`
	Paired      bool                 `json:"paired"`
	Connected   bool                 `json:"connected"`
	State       string               `json:"state"`
	LastError   string               `json:"last_error,omitempty"`
	LastUpdate  int64                `json:"last_update"`
	SavedConfig *config.CameraConfig `json:"saved_config,omitempty"`
}

type CameraConfigRequest struct {
	CameraName    string `json:"camera_name,omitempty"`
	WiFiSSID      string `json:"wifi_ssid"`
	WiFiPassword  string `json:"wifi_password"`
	RTMPURL       string `json:"rtmp_url"`
	Resolution    string `json:"resolution,omitempty"`
	FPS           int    `json:"fps,omitempty"`
	BitrateKbps   uint16 `json:"bitrate_kbps,omitempty"`
	Stabilization string `json:"stabilization,omitempty"`
}

type ModemsResponse struct {
	Available bool              `json:"available"`
	Modems    []modem.ModemInfo `json:"modems"`
}

type USBNetResponse struct {
	Devices []usbnet.DeviceStatus `json:"devices"`
}

type IPsFileResponse struct {
	FilePath string   `json:"file_path"`
	IPs      []string `json:"ips"`
}

type WiFiNetworksResponse struct {
	Available bool               `json:"available"`
	Networks  []wifi.NetworkInfo `json:"networks"`
}

type WiFiStatusResponse struct {
	Connection *wifi.ConnectionInfo `json:"connection"`
}

type WiFiConnectRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

type WiFiHotspotRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Band     string `json:"band"`
	Channel  int    `json:"channel"`
}

type WiFiActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ========== Helper Methods ==========

func jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

func (h *Handler) uptime() time.Duration {
	return time.Since(h.startTime)
}

func (h *Handler) PreviewDir() string {
	return h.previewDir
}

// SetVersion sets the application version
func (h *Handler) SetVersion(version string) {
	h.appVersion = version
}

// GetVersion returns the application version
func (h *Handler) GetVersion() string {
	return h.appVersion
}

// GetDJIController returns the DJI controller instance
func (h *Handler) GetDJIController() *dji.Controller {
	return h.djiController
}

// GetPipelineMode returns the current pipeline mode
func (h *Handler) GetPipelineMode() PipelineMode {
	h.pipelineMu.RLock()
	defer h.pipelineMu.RUnlock()
	return h.pipelineMode
}

// SetPipelineMode sets the pipeline mode
func (h *Handler) SetPipelineMode(mode PipelineMode) {
	h.pipelineMu.Lock()
	defer h.pipelineMu.Unlock()
	h.pipelineMode = mode
}

// IsStreaming returns true if the pipeline is in streaming mode (backward compat)
func (h *Handler) IsStreaming() bool {
	return h.GetPipelineMode() == PipelineModeStreaming
}

// StartReceiveMode starts FFmpeg in receive-only mode (RTMP listen + HLS preview, no SRT output)
func (h *Handler) StartReceiveMode() error {
	cfg := h.config.Get()

	bindAddr := "0.0.0.0"
	if hotspotIP := h.wifiMgr.GetHotspotIP(); hotspotIP != "" {
		bindAddr = hotspotIP
	}

	h.cleanPreviewDir()

	// srtPort=0 means no SRT output — receive-only mode
	if err := h.ffmpeg.StartWithPreview(cfg.RTMP.ListenPort, cfg.RTMP.StreamKey, 0, bindAddr, h.previewDir); err != nil {
		return fmt.Errorf("failed to start FFmpeg in receive mode: %w", err)
	}

	h.SetPipelineMode(PipelineModeReceiving)
	h.logOutput("manager", "[FFmpeg] Started in receive-only mode (RTMP + HLS preview)")

	// Start receive-mode health monitor
	go h.monitorReceiveHealth(bindAddr)

	return nil
}

// getBindAddr returns the appropriate bind address for FFmpeg
func (h *Handler) getBindAddr() string {
	if hotspotIP := h.wifiMgr.GetHotspotIP(); hotspotIP != "" {
		return hotspotIP
	}
	return "0.0.0.0"
}

func (h *Handler) cleanPreviewDir() {
	if h.previewDir == "" {
		return
	}
	_ = os.RemoveAll(h.previewDir)
}

func (h *Handler) logOutput(source string, line string) {
	if h.logs != nil {
		h.logs.Add(source, line)
	}
	if h.wsHub != nil {
		h.wsHub.Broadcast("log", map[string]string{
			"source": source,
			"line":   line,
		})
	}
}

func (h *Handler) getAvailableBindIPs(cfg *config.Config) []string {
	interfaces := system.ListNetworkInterfaces()
	systemIPs := make(map[string]bool)
	for _, iface := range interfaces {
		for _, ip := range iface.IPs {
			systemIPs[ip] = true
		}
	}

	var available []string
	for _, ip := range cfg.SRTLA.BindIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" && systemIPs[ip] {
			available = append(available, ip)
		}
	}
	return available
}

func (h *Handler) shouldRestartWithBackoff(tracker *RestartTracker, reason, processName string) bool {
	h.restartTrackerMu.Lock()
	defer h.restartTrackerMu.Unlock()

	now := time.Now()

	if tracker.latestFailTime.IsZero() {
		return true
	}

	if now.Sub(tracker.latestFailTime) > ResetFailureCountAfter {
		h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] %s recovered - resetting backoff counter", processName))
		tracker.failureCount = 0
		tracker.backoffDuration = InitialBackoff
		return true
	}

	if now.Sub(tracker.latestFailTime) >= tracker.backoffDuration {
		h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] %s %s, backoff elapsed, attempting restart (attempt %d, next backoff: %v)",
			processName, reason, tracker.failureCount+1, h.nextBackoffDuration(tracker)))
		return true
	}

	return false
}

func (h *Handler) recordRestartFailure(tracker *RestartTracker) {
	h.restartTrackerMu.Lock()
	defer h.restartTrackerMu.Unlock()

	tracker.latestFailTime = time.Now()
	tracker.failureCount++

	newBackoff := time.Duration(float64(tracker.backoffDuration) * BackoffMultiplier)
	if newBackoff > MaxBackoff {
		newBackoff = MaxBackoff
	}
	tracker.backoffDuration = newBackoff
}

func (h *Handler) recordRestartSuccess(tracker *RestartTracker) {
	h.restartTrackerMu.Lock()
	defer h.restartTrackerMu.Unlock()

	tracker.latestFailTime = time.Time{}
	tracker.failureCount = 0
	tracker.backoffDuration = InitialBackoff
}

func (h *Handler) nextBackoffDuration(tracker *RestartTracker) time.Duration {
	h.restartTrackerMu.RLock()
	defer h.restartTrackerMu.RUnlock()

	newBackoff := time.Duration(float64(tracker.backoffDuration) * BackoffMultiplier)
	if newBackoff > MaxBackoff {
		newBackoff = MaxBackoff
	}
	return newBackoff
}

func (h *Handler) startSRTLA(cfg *config.Config, bindIPs []string) error {
	if h.srtla.ProcessState() == process.StateRunning {
		return nil
	}

	if len(bindIPs) == 0 {
		return fmt.Errorf("no bind IPs provided for SRTLA")
	}

	binaryPath := cfg.SRTLA.BinaryPath
	if binaryPath == "" {
		binaryPath = "srtla_send"
	}
	srtlaStatus := system.CheckSRTLA(binaryPath)
	if !srtlaStatus.Installed {
		return fmt.Errorf("SRTLA binary not found. Please install srtla_send or set the correct binary path in configuration. %s", srtlaStatus.InstallCommand)
	}

	h.logOutput("manager", fmt.Sprintf("[SRTLA] Starting with %d bind IPs: %s", len(bindIPs), strings.Join(bindIPs, ", ")))

	if err := h.srtla.Start(
		cfg.SRTLA.BinaryPath,
		cfg.SRT.LocalPort,
		cfg.SRTLA.RemoteHost,
		cfg.SRTLA.RemotePort,
		bindIPs,
		cfg.SRTLA.Classic,
		cfg.SRTLA.NoQuality,
		cfg.SRTLA.Exploration,
	); err != nil {
		return fmt.Errorf("failed to start SRTLA: %w", err)
	}

	ready := false
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		state := h.srtla.ProcessState()
		if state == process.StateRunning {
			ready = true
			break
		}
		if state == process.StateError {
			h.srtla.Stop()
			return fmt.Errorf("SRTLA failed to start - check logs for details")
		}
	}

	if !ready {
		h.srtla.Stop()
		return fmt.Errorf("SRTLA timed out during startup (5 seconds)")
	}

	time.Sleep(time.Second)
	return nil
}

// ========== Camera Helper Methods ==========

func getDeviceIP(h *Handler) string {
	if hotspotIP := h.wifiMgr.GetHotspotIP(); hotspotIP != "" {
		return hotspotIP
	}
	if ip := system.GetFirstNonLoopbackIP(); ip != "" {
		return ip
	}
	return ""
}

func (h *Handler) buildPreviewConfig(ssid, password, rtmpURL string) *dji.StreamConfig {
	return &dji.StreamConfig{
		WiFiSSID:      strings.TrimSpace(ssid),
		WiFiPassword:  strings.TrimSpace(password),
		RTMPURL:       strings.TrimSpace(rtmpURL),
		Resolution:    dji.Resolution720p,
		FPS:           15,
		BitrateKbps:   1000,
		Stabilization: dji.StabilizationOff,
	}
}

func (h *Handler) buildStreamConfig(req CameraConfigRequest) *dji.StreamConfig {
	config := &dji.StreamConfig{
		WiFiSSID:      strings.TrimSpace(req.WiFiSSID),
		WiFiPassword:  strings.TrimSpace(req.WiFiPassword),
		RTMPURL:       strings.TrimSpace(req.RTMPURL),
		Resolution:    dji.StreamResolution(req.Resolution),
		FPS:           req.FPS,
		BitrateKbps:   req.BitrateKbps,
		Stabilization: dji.ImageStabilization(req.Stabilization),
	}

	if config.Resolution == "" {
		config.Resolution = dji.Resolution1080p
	}
	if config.FPS == 0 {
		config.FPS = 30
	}
	if config.BitrateKbps == 0 {
		config.BitrateKbps = 6000
	}
	if config.Stabilization == "" {
		config.Stabilization = dji.StabilizationOff
	}

	return config
}

func (h *Handler) configFromRequest(req CameraConfigRequest) config.CameraConfig {
	return config.CameraConfig{
		Name:         req.CameraName,
		RTMPUrl:      req.RTMPURL,
		WiFiSSID:     req.WiFiSSID,
		WiFiPassword: req.WiFiPassword,
	}
}

func (h *Handler) buildCameraInfo(device *dji.DiscoveredDevice) *CameraInfo {
	state := h.djiController.GetDeviceState(device.ID)
	stateStr := string(dji.StateIdle)
	var lastError string
	var lastUpdate int64

	if state != nil {
		stateStr = string(state.ConnectionState)
		lastError = state.LastError
		lastUpdate = state.LastUpdate.Unix()
	}

	var savedConfig *config.CameraConfig
	if cfg, ok := h.config.LoadCameraConfig(device.ID); ok {
		savedConfig = &cfg
	}

	return &CameraInfo{
		ID:          device.ID,
		Name:        device.Name,
		Model:       string(device.Model),
		RSSI:        device.RSSI,
		Paired:      device.Paired,
		Connected:   device.Connected,
		State:       stateStr,
		LastError:   lastError,
		LastUpdate:  lastUpdate,
		SavedConfig: savedConfig,
	}
}

func (h *Handler) buildSavedCameraInfo(id string, cfg config.CameraConfig) *CameraInfo {
	state := h.djiController.GetDeviceState(id)
	stateStr := string(dji.StateIdle)
	var lastError string
	var lastUpdate int64

	if state != nil {
		stateStr = string(state.ConnectionState)
		lastError = state.LastError
		lastUpdate = state.LastUpdate.Unix()
	}

	return &CameraInfo{
		ID:          id,
		Name:        cfg.Name,
		Model:       "Unknown",
		RSSI:        -100,
		Paired:      false,
		Connected:   false,
		State:       stateStr,
		LastError:   lastError,
		LastUpdate:  lastUpdate,
		SavedConfig: &cfg,
	}
}

// ========== Utility Helpers ==========

func parseTimeout(timeoutStr string) time.Duration {
	timeout := 60 * time.Second
	if timeoutStr != "" {
		if t, err := time.ParseDuration(timeoutStr); err == nil && t > 0 && t < 5*time.Minute {
			timeout = t
		}
	}
	return timeout
}

func (h *Handler) GetUSBNetStatus() USBNetResponse {
	var devices []usbnet.DeviceStatus
	if h.usb != nil {
		devices = h.usb.Status()
	}
	if devices == nil {
		devices = []usbnet.DeviceStatus{}
	}
	return USBNetResponse{Devices: devices}
}

func (h *Handler) GetModemStatus() ModemsResponse {
	modems, _ := h.modem.ListModems()
	resp := ModemsResponse{
		Available: h.modem.IsAvailable(),
		Modems:    modems,
	}
	if resp.Modems == nil {
		resp.Modems = []modem.ModemInfo{}
	}
	return resp
}
