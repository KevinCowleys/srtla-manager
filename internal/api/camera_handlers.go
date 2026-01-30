package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"srtla-manager/internal/dji"
)

// HandleCameraList returns list of discovered cameras
func (h *Handler) HandleCameraList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	devices := h.djiScanner.GetDiscoveredDevices()
	cameras := make([]*CameraInfo, 0, len(devices))
	discoveredIDs := make(map[string]bool)

	for _, device := range devices {
		discoveredIDs[device.ID] = true
		info := h.buildCameraInfo(device)
		cameras = append(cameras, info)
	}

	// Also check for paired DJI devices that might not be actively advertising
	pairedDevices, err := h.djiScanner.GetPairedDJIDevices()
	if err == nil {
		for _, device := range pairedDevices {
			if !discoveredIDs[device.ID] {
				discoveredIDs[device.ID] = true
				info := h.buildCameraInfo(device)
				cameras = append(cameras, info)
			}
		}
	}

	// Add saved cameras that weren't discovered
	savedCameras := h.config.GetAllCameraConfigs()
	for id, cfg := range savedCameras {
		if !discoveredIDs[id] {
			info := h.buildSavedCameraInfo(id, cfg)
			cameras = append(cameras, info)
		}
	}

	json.NewEncoder(w).Encode(CameraListResponse{
		Cameras:  cameras,
		Scanning: h.djiScanner.IsScanning(),
	})
}

// HandleCameraScan starts BLE scanning for DJI devices
func (h *Handler) HandleCameraScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	timeout := parseTimeout(r.URL.Query().Get("timeout"))

	if err := h.djiScanner.StartScanning(timeout); err != nil {
		jsonError(w, fmt.Sprintf("Failed to start scan: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "scanning",
		"timeout": timeout.String(),
	})
}

// HandleCameraScanStop stops BLE scanning
func (h *Handler) HandleCameraScanStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.djiScanner.StopScanning(); err != nil {
		jsonError(w, fmt.Sprintf("Failed to stop scan: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stopped",
	})
}

// HandleCameraConnect initiates connection to a discovered camera
func (h *Handler) HandleCameraConnect(w http.ResponseWriter, r *http.Request) {
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

	if err := h.djiController.ConnectDevice(cameraID); err != nil {
		jsonError(w, fmt.Sprintf("Failed to connect: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "connecting",
		"camera": cameraID,
	})
}

// HandleCameraDisconnect closes connection to a camera
func (h *Handler) HandleCameraDisconnect(w http.ResponseWriter, r *http.Request) {
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

	if err := h.djiController.DisconnectDevice(cameraID); err != nil {
		jsonError(w, fmt.Sprintf("Failed to disconnect: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "disconnected",
		"camera": cameraID,
	})
}

// HandleCameraForget removes a camera from the discovered list
func (h *Handler) HandleCameraForget(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := ParseCameraPath(r.URL.Path, "")
	if len(parts) < 1 || parts[0] == "" {
		jsonError(w, "Camera ID required", http.StatusBadRequest)
		return
	}

	cameraID := parts[0]

	removed := h.djiScanner.RemoveDevice(cameraID)
	h.djiController.RemoveDevice(cameraID)

	if !removed {
		jsonError(w, "Camera not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "removed",
		"camera": cameraID,
	})
}

// HandleCameraRefresh refreshes information for a specific camera from BlueZ
func (h *Handler) HandleCameraRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := ParseCameraPath(r.URL.Path, "/refresh")
	if len(parts) < 1 || parts[0] == "" {
		jsonError(w, "Camera ID required", http.StatusBadRequest)
		return
	}

	cameraID := parts[0]

	device, err := h.djiScanner.RefreshDevice(cameraID)
	if err != nil {
		jsonError(w, fmt.Sprintf("Failed to refresh: %v", err), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(h.buildCameraInfo(device))
}

// HandleDebugAddTestCamera adds a test camera (for development/testing)
func (h *Handler) HandleDebugAddTestCamera(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Model string `json:"model"`
		RSSI  int    `json:"rssi"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	model := parseDeviceModel(req.Model)
	device := &dji.DiscoveredDevice{
		ID:        req.ID,
		Name:      req.Name,
		Model:     model,
		RSSI:      req.RSSI,
		Paired:    false,
		Connected: false,
	}

	h.djiScanner.AddDiscoveredDevice(device)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "added",
		"id":     req.ID,
	})
}

// Helpers

func parseDeviceModel(modelStr string) dji.DeviceModel {
	switch modelStr {
	case "OA3":
		return dji.ModelOsmoAction3
	case "OA4":
		return dji.ModelOsmoAction4
	case "OA5P":
		return dji.ModelOsmoAction5Pro
	case "OA6":
		return dji.ModelOsmoAction6
	case "OP3":
		return dji.ModelOsmoPocket3
	default:
		return dji.ModelUnknown
	}
}

// ParseCameraPath extracts camera ID from URL path
func ParseCameraPath(path string, suffix string) []string {
	path = strings.TrimPrefix(path, "/api/cameras/")
	if suffix != "" {
		path = strings.TrimSuffix(path, suffix)
	}
	path = strings.TrimSuffix(path, "/")
	return strings.Split(path, "/")
}
