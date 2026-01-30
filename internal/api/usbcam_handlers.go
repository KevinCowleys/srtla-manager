package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"srtla-manager/internal/process"
	"srtla-manager/internal/usbcam"
)

// USBCameraListResponse is the response for listing USB cameras
type USBCameraListResponse struct {
	Cameras []*usbcam.CameraState `json:"cameras"`
}

// USBCameraStartRequest is the request to start USB camera streaming
type USBCameraStartRequest struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Bitrate int    `json:"bitrate"` // kbps
	Encoder string `json:"encoder"` // libx264, h264_vaapi, h264_nvenc, copy
}

// HandleUSBCameraList returns all detected USB cameras
func (h *Handler) HandleUSBCameraList(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		jsonError(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	cameras := h.usbCamController.GetCameras()
	if cameras == nil {
		cameras = []*usbcam.CameraState{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(USBCameraListResponse{Cameras: cameras})
}

// HandleUSBCameraScan triggers a scan for USB cameras
func (h *Handler) HandleUSBCameraScan(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		jsonError(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	cameras, err := h.usbCamController.ScanCameras()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Convert to camera states
	states := h.usbCamController.GetCameras()
	if states == nil {
		states = []*usbcam.CameraState{}
	}

	h.logOutput("usbcam", fmt.Sprintf("[USBCam] Scan complete, found %d cameras", len(cameras)))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(USBCameraListResponse{Cameras: states})
}

// HandleUSBCameraGet returns details for a specific USB camera
func (h *Handler) HandleUSBCameraGet(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		jsonError(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	// Extract camera ID from URL path
	cameraID := r.PathValue("id")
	if cameraID == "" {
		jsonError(w, "camera ID required", http.StatusBadRequest)
		return
	}

	state := h.usbCamController.GetCameraState(cameraID)
	if state == nil {
		jsonError(w, "camera not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// HandleUSBCameraStart starts streaming from a USB camera
func (h *Handler) HandleUSBCameraStart(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		jsonError(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	// Extract camera ID from URL path
	cameraID := r.PathValue("id")
	if cameraID == "" {
		jsonError(w, "camera ID required", http.StatusBadRequest)
		return
	}

	// Parse request body
	var req USBCameraStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply defaults
	if req.Width == 0 {
		req.Width = 1920
	}
	if req.Height == 0 {
		req.Height = 1080
	}
	if req.FPS == 0 {
		req.FPS = 30
	}
	if req.Bitrate == 0 {
		req.Bitrate = 6000
	}
	if req.Encoder == "" {
		req.Encoder = "libx264"
	}

	// Validate encoder
	if !usbcam.ValidateEncoder(req.Encoder) {
		jsonError(w, "invalid encoder: "+req.Encoder, http.StatusBadRequest)
		return
	}

	// Check if we're already streaming
	if h.GetPipelineMode() == PipelineModeStreaming {
		jsonError(w, "another stream is already active", http.StatusConflict)
		return
	}

	// Stop FFmpeg (receive-only mode) so USB capture can take over
	h.SetPipelineMode(PipelineModeIdle)
	_ = h.ffmpeg.Stop()
	_ = h.srtla.Stop()

	cfg := h.config.Get()

	// Build stream config
	streamConfig := &usbcam.StreamConfig{
		Width:   req.Width,
		Height:  req.Height,
		FPS:     req.FPS,
		Bitrate: req.Bitrate,
		Encoder: req.Encoder,
	}

	// Start SRTLA if enabled and bind IPs are available
	srtPort := 0
	srtlaStarted := false
	if cfg.SRTLA.Enabled {
		bindIPs := h.getAvailableBindIPs(&cfg)
		if len(bindIPs) > 0 {
			if err := h.startSRTLA(&cfg, bindIPs); err != nil {
				h.logOutput("usbcam", fmt.Sprintf("[USBCam] Warning: Failed to start SRTLA: %v (continuing without outbound streaming)", err))
			} else {
				srtPort = cfg.SRT.LocalPort
				srtlaStarted = true
				h.activeBindIPs = bindIPs
			}
		} else {
			h.logOutput("usbcam", "[USBCam] No bind IPs available, starting capture without outbound streaming")
		}
	}

	// Start USB camera capture (srtPort=0 means capture + preview only, no outbound SRT)
	if err := h.usbCamController.StartStreaming(cameraID, streamConfig, srtPort, h.previewDir); err != nil {
		if srtlaStarted {
			_ = h.srtla.Stop()
		}
		_ = h.StartReceiveMode() // restore receive mode
		jsonError(w, "failed to start streaming: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if srtlaStarted {
		h.SetPipelineMode(PipelineModeStreaming)
	} else {
		h.SetPipelineMode(PipelineModeReceiving)
	}

	h.logOutput("usbcam", "[USBCam] Started streaming from camera "+cameraID)

	state := h.usbCamController.GetCameraState(cameraID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// HandleUSBCameraStop stops streaming from a USB camera
func (h *Handler) HandleUSBCameraStop(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		jsonError(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	// Extract camera ID from URL path
	cameraID := r.PathValue("id")
	if cameraID == "" {
		jsonError(w, "camera ID required", http.StatusBadRequest)
		return
	}

	// Stop the camera streaming
	if err := h.usbCamController.StopStreaming(cameraID); err != nil {
		jsonError(w, "failed to stop streaming: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Stop FFmpeg and SRTLA
	_ = h.ffmpeg.Stop()
	_ = h.srtla.Stop()

	h.logOutput("usbcam", "[USBCam] Stopped streaming from camera "+cameraID)

	// Restore receive mode
	if err := h.StartReceiveMode(); err != nil {
		h.logOutput("usbcam", fmt.Sprintf("[USBCam] Warning: Failed to restore receive mode: %v", err))
		h.SetPipelineMode(PipelineModeIdle)
	}

	state := h.usbCamController.GetCameraState(cameraID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (h *Handler) initUSBCamController() {
	if h.usbCamController == nil {
		return
	}

	// Set capture handlers
	h.usbCamController.SetCaptureHandlers(
		// Start capture
		func(config usbcam.CaptureConfig) error {
			return h.ffmpeg.StartUSBCapture(process.USBCaptureConfig{
				DevicePath:  config.DevicePath,
				Width:       config.Width,
				Height:      config.Height,
				FPS:         config.FPS,
				Encoder:     config.Encoder,
				Bitrate:     config.Bitrate,
				InputFormat: config.InputFormat,
				SRTPort:     config.SRTPort,
				HLSDir:      config.HLSDir,
			})
		},
		// Stop capture
		func() error {
			return h.ffmpeg.Stop()
		},
	)
}

// HandleUSBCameraPreview starts an MJPEG HTTP stream from a USB camera
// This replaces the old HLS-based preview with instant HTTP MJPEG
func (h *Handler) HandleUSBCameraPreview(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		jsonError(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	// Extract camera ID from URL path
	cameraID := r.PathValue("id")
	if cameraID == "" {
		jsonError(w, "camera ID required", http.StatusBadRequest)
		return
	}

	cameraState := h.usbCamController.GetCameraState(cameraID)
	if cameraState == nil || cameraState.Camera == nil {
		jsonError(w, "camera not found", http.StatusNotFound)
		return
	}

	camera := cameraState.Camera

	// Stop any existing preview before starting a new one
	if h.ffmpeg.GetPreviewPort(cameraID) > 0 {
		_ = h.ffmpeg.StopPreview(cameraID)
	}

	// Parse request body to get desired resolution and bitrate
	type PreviewRequest struct {
		Width   int `json:"width"`
		Height  int `json:"height"`
		FPS     int `json:"fps"`
		Bitrate int `json:"bitrate"` // kbps, optional
	}
	var req PreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Width > 0 && req.Height > 0 {
		// User provided specific resolution - validate it
		foundFormat := false
		for i := range camera.Formats {
			if camera.Formats[i].Width == req.Width &&
				camera.Formats[i].Height == req.Height {
				foundFormat = true
				break
			}
		}
		if !foundFormat {
			// Format not found - allow anyway and let FFmpeg handle it
		}
	}

	// Select resolution and format - prioritize MJPEG for reliable previews
	width := 1280
	height := 720
	inputFormat := "mjpeg"

	if len(camera.Formats) > 0 {
		// If user specified a resolution, try to find it - PREFER MJPEG
		var selectedFormat *usbcam.VideoFormat
		if req.Width > 0 && req.Height > 0 {
			// First pass: look for MJPEG at requested resolution
			for i := range camera.Formats {
				if camera.Formats[i].Width == req.Width &&
					camera.Formats[i].Height == req.Height &&
					camera.Formats[i].PixelFormat == "MJPG" {
					selectedFormat = &camera.Formats[i]
					break
				}
			}
			// Second pass: accept any format at requested resolution
			if selectedFormat == nil {
				for i := range camera.Formats {
					if camera.Formats[i].Width == req.Width &&
						camera.Formats[i].Height == req.Height {
						selectedFormat = &camera.Formats[i]
						break
					}
				}
			}
		}

		// If user didn't specify or format not found, fall back to auto-select
		if selectedFormat == nil {
			selectedFormat = &camera.Formats[0]
			for i := range camera.Formats {
				if camera.Formats[i].PixelFormat == "MJPG" {
					selectedFormat = &camera.Formats[i]
					break
				}
			}
		}

		width = selectedFormat.Width
		height = selectedFormat.Height

		switch {
		case selectedFormat.PixelFormat == "MJPG":
			inputFormat = "mjpeg"
		case selectedFormat.PixelFormat == "H264":
			inputFormat = "h264"
		case selectedFormat.PixelFormat == "NV12":
			inputFormat = "nv12"
		case selectedFormat.PixelFormat == "YUYV":
			inputFormat = "yuyv422"
		case strings.HasPrefix(selectedFormat.PixelFormat, "YU"):
			inputFormat = "yuyv422"
		default:
			inputFormat = strings.ToLower(selectedFormat.PixelFormat)
		}
	}

	// Start HTTP preview streaming with MJPEG (always use MJPEG for preview)
	// Use provided settings or defaults
	fps := req.FPS
	if fps == 0 {
		fps = 30
	}
	bitrate := req.Bitrate
	if bitrate == 0 {
		bitrate = 3000
	}

	port, err := h.ffmpeg.StartUSBCameraHTTPPreview(cameraID, process.USBCaptureConfig{
		DevicePath:  camera.DevicePath,
		Width:       width,
		Height:      height,
		FPS:         fps,
		Encoder:     "mjpeg", // Force MJPEG for HTTP preview streaming
		Bitrate:     bitrate,
		InputFormat: inputFormat,
		SRTPort:     0,
		HLSDir:      "",
	})
	if err != nil {
		jsonError(w, "failed to start preview: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.logOutput("usbcam", fmt.Sprintf("[USBCam] HTTP preview started for camera %s on port %d with MJPEG encoder at %d fps", cameraID, port, fps))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "preview_streaming",
		"camera":      cameraID,
		"preview_url": fmt.Sprintf("/api/usbcams/%s/preview-stream", cameraID),
		"port":        port,
	})
}

// HandleUSBCameraPreviewStream proxies the MJPEG stream from FFmpeg's broadcast HTTP server
func (h *Handler) HandleUSBCameraPreviewStream(w http.ResponseWriter, r *http.Request) {
	cameraID := r.PathValue("id")
	if cameraID == "" {
		http.Error(w, "camera ID required", http.StatusBadRequest)
		return
	}

	// Get the port where the broadcast server is running for this camera
	port := h.ffmpeg.GetPreviewPort(cameraID)
	if port == 0 {
		http.Error(w, "preview not active", http.StatusNotFound)
		return
	}

	// Proxy to the broadcast HTTP server
	targetURL := fmt.Sprintf("http://127.0.0.1:%d/stream-%d", port, port)
	resp, err := http.Get(targetURL)
	if err != nil {
		http.Error(w, "failed to connect to preview stream: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Copy headers
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=ffmpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")

	// Stream the data
	io.Copy(w, resp.Body)
}

// HandleUSBCameraPreviewStop stops the HTTP preview stream
func (h *Handler) HandleUSBCameraPreviewStop(w http.ResponseWriter, r *http.Request) {
	cameraID := r.PathValue("id")
	if cameraID == "" {
		jsonError(w, "camera ID required", http.StatusBadRequest)
		return
	}

	// Stop the preview for this specific camera
	err := h.ffmpeg.StopPreview(cameraID)

	h.logOutput("usbcam", fmt.Sprintf("[USBCam] Preview stopped for camera %s", cameraID))

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
	} else {
		json.NewEncoder(w).Encode(map[string]string{
			"status": "stopped",
		})
	}
}

// OLD HandleUSBCameraMJPEG - deprecated, kept for reference
func (h *Handler) HandleUSBCameraMJPEGOld(w http.ResponseWriter, r *http.Request) {
	if h.usbCamController == nil {
		http.Error(w, "USB camera support not initialized", http.StatusServiceUnavailable)
		return
	}

	// Extract camera ID from URL path
	cameraID := r.PathValue("id")
	if cameraID == "" {
		http.Error(w, "camera ID required", http.StatusBadRequest)
		return
	}

	cameraState := h.usbCamController.GetCameraState(cameraID)
	if cameraState == nil || cameraState.Camera == nil {
		http.Error(w, "camera not found", http.StatusNotFound)
		return
	}

	camera := cameraState.Camera

	// Select resolution and format
	width := 1280
	height := 720
	inputFormat := "mjpeg"

	if len(camera.Formats) > 0 {
		selectedFormat := &camera.Formats[0]
		width = selectedFormat.Width
		height = selectedFormat.Height

		switch {
		case selectedFormat.PixelFormat == "MJPG":
			inputFormat = "mjpeg"
		case selectedFormat.PixelFormat == "H264":
			inputFormat = "h264"
		case strings.HasPrefix(selectedFormat.PixelFormat, "YU"):
			inputFormat = "yuyv422"
		default:
			inputFormat = strings.ToLower(selectedFormat.PixelFormat)
		}
	}

	// Set headers for MJPEG streaming
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=--jpgboundary")
	w.Header().Set("Connection", "close")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")

	h.logOutput("usbcam", fmt.Sprintf("[USBCam] MJPEG stream started for camera %s (%dx%d, %s)", cameraID, width, height, inputFormat))

	// Stream MJPEG directly from FFmpeg to HTTP response
	err := h.streamMJPEGDirect(w, camera.DevicePath, width, height, inputFormat)
	if err != nil {
		h.logOutput("usbcam", fmt.Sprintf("[USBCam] MJPEG stream error: %v", err))
	}
}

// streamMJPEGDirect streams MJPEG frames directly from FFmpeg to HTTP response
func (h *Handler) streamMJPEGDirect(w http.ResponseWriter, devicePath string, width, height int, inputFormat string) error {
	// Build FFmpeg command to stream MJPEG to stdout
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-f", "v4l2",
		"-input_format", inputFormat,
		"-framerate", "30",
		"-video_size", fmt.Sprintf("%dx%d", width, height),
		"-i", devicePath,
		"-vf", "format=yuvj420p",
		"-c:v", "mjpeg",
		"-q:v", "5",
		"-f", "mjpeg",
		"pipe:1",
	}

	cmd := exec.Command("ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Set headers for MJPEG motion-jpeg streaming (simpler than multipart)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Connection", "close")

	// Stream raw MJPEG directly from FFmpeg
	// Each frame is a complete JPEG (starts with FFD8, ends with FFD9)
	buffer := make([]byte, 65536)
	for {
		n, err := stdout.Read(buffer)
		if err != nil && err != io.EOF {
			cmd.Process.Kill()
			return err
		}

		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				cmd.Process.Kill()
				return err
			}
			// Flush to client for low-latency streaming
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}

		if err == io.EOF {
			break
		}
	}

	_ = cmd.Wait()
	return nil
}
