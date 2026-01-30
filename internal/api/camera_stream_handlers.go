package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"srtla-manager/internal/process"
)

// HandleCameraPreview starts a preview stream on a camera before full configuration
func (h *Handler) HandleCameraPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract camera ID from URL path
	parts := ParseCameraPath(r.URL.Path, "/preview")
	if len(parts) < 1 || parts[0] == "" {
		jsonError(w, "Camera ID required", http.StatusBadRequest)
		return
	}

	cameraID := parts[0]

	// Parse preview request (WiFi details needed to connect)
	var previewReq struct {
		WiFiSSID     string `json:"wifi_ssid"`
		WiFiPassword string `json:"wifi_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&previewReq); err != nil {
		jsonError(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if previewReq.WiFiSSID == "" {
		jsonError(w, "WiFi SSID is required for preview", http.StatusBadRequest)
		return
	}

	// Determine bind address for FFmpeg and RTMP URL for camera
	deviceIP := getDeviceIP(h)
	if deviceIP == "" {
		jsonError(w, "Cannot start preview: no network interface with an IP address found", http.StatusBadRequest)
		return
	}

	// Build preview RTMP URL with the device's actual IP that the camera can reach
	// Use application "live" and stream key "live" (standard RTMP pattern)
	previewRTMPURL := fmt.Sprintf("rtmp://%s:9999/live/live", deviceIP)

	// Configure preview stream with low bitrate
	// Use a different port (9999) for preview to avoid conflicts with main streaming
	previewConfig := h.buildPreviewConfig(previewReq.WiFiSSID, previewReq.WiFiPassword, previewRTMPURL)

	// Ensure device is connected before configuring preview
	if h.djiController.GetDeviceState(cameraID) == nil {
		if err := h.djiController.ConnectDevice(cameraID); err != nil {
			jsonError(w, fmt.Sprintf("Failed to connect to camera for preview: %v", err), http.StatusBadRequest)
			return
		}
		time.Sleep(2 * time.Second)
	}

	// Block preview if the main stream is actively running
	if h.GetPipelineMode() == PipelineModeStreaming {
		jsonError(w, "Cannot start preview while main stream is running. Please stop streaming first.", http.StatusConflict)
		return
	}

	// Stop FFmpeg (receive-only mode) so preview can bind to a different port
	if h.ffmpeg.ProcessState() == process.StateRunning {
		h.SetPipelineMode(PipelineModeIdle)
		_ = h.ffmpeg.Stop()
		time.Sleep(300 * time.Millisecond)
	}

	// Start receiving preview stream and convert to HLS
	previewDir := "/tmp/srtla-preview-temp"
	os.RemoveAll(previewDir)
	if err := os.MkdirAll(previewDir, 0777); err != nil {
		jsonError(w, fmt.Sprintf("Failed to create preview directory: %v", err), http.StatusBadRequest)
		return
	}

	// Start ffmpeg to receive preview on port 9999 and output to HLS only (no SRT leg for preview)
	// Use application "live" and stream key "live" to match the RTMP URL we give the camera
	if err := h.ffmpeg.StartWithPreview(9999, "live/live", 0, deviceIP, previewDir); err != nil {
		jsonError(w, fmt.Sprintf("Failed to start preview stream receiver: %v", err), http.StatusBadRequest)
		return
	}

	// Now start preview stream on camera
	if err := h.djiController.ConfigureStreaming(cameraID, previewConfig); err != nil {
		h.ffmpeg.Stop()
		jsonError(w, fmt.Sprintf("Failed to start preview: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "preview_streaming",
		"camera":      cameraID,
		"preview_url": "/preview-temp/playlist.m3u8",
	})
}

// HandleCameraConfigure configures streaming on a camera
func (h *Handler) HandleCameraConfigure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := ParseCameraPath(r.URL.Path, "")
	if len(parts) < 1 || parts[0] == "" {
		jsonError(w, "Camera ID required", http.StatusBadRequest)
		return
	}

	cameraID := parts[0]

	var configReq CameraConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&configReq); err != nil {
		jsonError(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if configReq.WiFiSSID == "" || configReq.RTMPURL == "" {
		jsonError(w, "WiFi SSID and RTMP URL are required", http.StatusBadRequest)
		return
	}

	streamConfig := h.buildStreamConfig(configReq)

	// Ensure device is connected
	if h.djiController.GetDeviceState(cameraID) == nil {
		if err := h.djiController.ConnectDevice(cameraID); err != nil {
			jsonError(w, fmt.Sprintf("Failed to connect to camera before configuring: %v", err), http.StatusBadRequest)
			return
		}
		time.Sleep(2 * time.Second)
	}

	if err := h.djiController.ConfigureStreaming(cameraID, streamConfig); err != nil {
		jsonError(w, fmt.Sprintf("Failed to configure: %v", err), http.StatusBadRequest)
		return
	}

	// FFmpeg should already be running in receive mode — camera will connect to it.
	// If somehow idle, try to restore receive mode.
	if h.GetPipelineMode() == PipelineModeIdle {
		log.Printf("[DJI] Camera configured but FFmpeg not running, starting receive mode\n")
		if err := h.StartReceiveMode(); err != nil {
			log.Printf("[DJI] Warning: Failed to start receive mode: %v\n", err)
		}
	}

	// Save camera configuration
	cameraConfig := h.configFromRequest(configReq)
	if err := h.config.SaveCameraConfig(cameraID, cameraConfig); err != nil {
		fmt.Printf("[ERROR] Failed to save camera config: %v\n", err)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "configuring",
		"camera": cameraID,
		"config": configReq,
	})
}

// HandleCameraStop stops streaming on a camera
func (h *Handler) HandleCameraStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := ParseCameraPath(r.URL.Path, "")
	if len(parts) < 1 || parts[0] == "" {
		jsonError(w, "Camera ID required", http.StatusBadRequest)
		return
	}

	cameraID := parts[0]

	if err := h.djiController.StopStreaming(cameraID); err != nil {
		jsonError(w, fmt.Sprintf("Failed to stop: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stopping",
		"camera": cameraID,
	})
}
