package usbcam

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// StreamState represents the streaming state
type StreamState string

const (
	StateIdle      StreamState = "idle"
	StateStarting  StreamState = "starting"
	StateStreaming StreamState = "streaming"
	StateStopping  StreamState = "stopping"
	StateError     StreamState = "error"
)

// StreamConfig holds configuration for USB camera streaming
type StreamConfig struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Bitrate int    `json:"bitrate"` // kbps
	Encoder string `json:"encoder"` // libx264, h264_vaapi, h264_nvenc, copy
}

// CameraState tracks the state of a USB camera
type CameraState struct {
	Camera       *USBCamera   `json:"camera"`
	State        StreamState  `json:"state"`
	StreamConfig *StreamConfig `json:"stream_config,omitempty"`
	LastError    string       `json:"last_error,omitempty"`
	StartTime    time.Time    `json:"start_time,omitempty"`
}

// Controller manages USB camera streaming
type Controller struct {
	mu           sync.RWMutex
	scanner      *Scanner
	cameraStates map[string]*CameraState
	activeCamera string // ID of currently streaming camera (only one at a time)
	updateChan   chan *CameraState

	// Callback for starting FFmpeg capture
	startCapture func(config CaptureConfig) error
	stopCapture  func() error
}

// CaptureConfig is passed to the FFmpeg handler
type CaptureConfig struct {
	DevicePath  string
	Width       int
	Height      int
	FPS         int
	Encoder     string
	Bitrate     int
	InputFormat string // e.g., "mjpeg", "h264", "yuyv422"
	SRTPort     int
	HLSDir      string
}

// NewController creates a new USB camera controller
func NewController(scanner *Scanner) *Controller {
	return &Controller{
		scanner:      scanner,
		cameraStates: make(map[string]*CameraState),
		updateChan:   make(chan *CameraState, 10),
	}
}

// SetCaptureHandlers sets the callbacks for starting/stopping FFmpeg capture
func (c *Controller) SetCaptureHandlers(start func(CaptureConfig) error, stop func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startCapture = start
	c.stopCapture = stop
}

// GetScanner returns the scanner instance
func (c *Controller) GetScanner() *Scanner {
	return c.scanner
}

// ScanCameras triggers a camera scan
func (c *Controller) ScanCameras() ([]*USBCamera, error) {
	cameras, err := c.scanner.Scan()
	if err != nil {
		return nil, err
	}

	// Initialize states for new cameras
	c.mu.Lock()
	for _, cam := range cameras {
		if _, exists := c.cameraStates[cam.ID]; !exists {
			c.cameraStates[cam.ID] = &CameraState{
				Camera: cam,
				State:  StateIdle,
			}
		} else {
			// Update camera info
			c.cameraStates[cam.ID].Camera = cam
		}
	}
	c.mu.Unlock()

	return cameras, nil
}

// GetCameras returns all known cameras with their states
func (c *Controller) GetCameras() []*CameraState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	states := make([]*CameraState, 0, len(c.cameraStates))
	for _, state := range c.cameraStates {
		states = append(states, state)
	}
	return states
}

// GetCameraState returns the state of a specific camera
func (c *Controller) GetCameraState(id string) *CameraState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cameraStates[id]
}

// GetActiveCamera returns the ID of the currently streaming camera
func (c *Controller) GetActiveCamera() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeCamera
}

// StartStreaming starts streaming from a USB camera
func (c *Controller) StartStreaming(cameraID string, config *StreamConfig, srtPort int, hlsDir string) error {
	c.mu.Lock()

	// Check if camera exists
	state, exists := c.cameraStates[cameraID]
	if !exists {
		c.mu.Unlock()
		return fmt.Errorf("camera not found: %s", cameraID)
	}

	// Check if another camera is streaming
	if c.activeCamera != "" && c.activeCamera != cameraID {
		c.mu.Unlock()
		return fmt.Errorf("another camera is already streaming: %s", c.activeCamera)
	}

	// Check if already streaming
	if state.State == StateStreaming || state.State == StateStarting {
		c.mu.Unlock()
		return fmt.Errorf("camera is already streaming")
	}

	if c.startCapture == nil {
		c.mu.Unlock()
		return fmt.Errorf("capture handler not configured")
	}

	camera := state.Camera
	c.mu.Unlock()

	// Determine input format based on encoder choice
	inputFormat := "mjpeg" // default
	if config.Encoder == "copy" && camera.HasH264Support() {
		inputFormat = "h264"
	}

	// Find matching format from camera capabilities
	var selectedFormat *VideoFormat
	for i := range camera.Formats {
		f := &camera.Formats[i]
		if f.Width == config.Width && f.Height == config.Height {
			// Check if format matches our input requirement
			if inputFormat == "h264" && f.PixelFormat == "H264" {
				selectedFormat = f
				break
			}
			if inputFormat == "mjpeg" && f.PixelFormat == "MJPG" {
				selectedFormat = f
				break
			}
			if f.PixelFormat == "YUYV" {
				selectedFormat = f
				// Keep looking for better format
			}
		}
	}

	if selectedFormat == nil {
		// Fall back to best available format
		selectedFormat = camera.GetBestFormat(config.Width, config.Height)
		if selectedFormat != nil {
			config.Width = selectedFormat.Width
			config.Height = selectedFormat.Height
			if selectedFormat.PixelFormat == "H264" {
				inputFormat = "h264"
			} else if selectedFormat.PixelFormat == "MJPG" {
				inputFormat = "mjpeg"
			} else {
				inputFormat = "yuyv422"
			}
		}
	}

	// Update state
	c.mu.Lock()
	state.State = StateStarting
	state.StreamConfig = config
	state.LastError = ""
	c.activeCamera = cameraID
	c.mu.Unlock()

	c.notifyUpdate(state)

	// Build capture config
	captureConfig := CaptureConfig{
		DevicePath:  camera.DevicePath,
		Width:       config.Width,
		Height:      config.Height,
		FPS:         config.FPS,
		Encoder:     config.Encoder,
		Bitrate:     config.Bitrate,
		InputFormat: inputFormat,
		SRTPort:     srtPort,
		HLSDir:      hlsDir,
	}

	log.Printf("[USBCam] Starting capture: device=%s, %dx%d@%dfps, encoder=%s, input=%s\n",
		captureConfig.DevicePath, captureConfig.Width, captureConfig.Height,
		captureConfig.FPS, captureConfig.Encoder, captureConfig.InputFormat)

	// Start FFmpeg capture
	if err := c.startCapture(captureConfig); err != nil {
		c.mu.Lock()
		state.State = StateError
		state.LastError = err.Error()
		c.activeCamera = ""
		c.mu.Unlock()
		c.notifyUpdate(state)
		return fmt.Errorf("failed to start capture: %w", err)
	}

	c.mu.Lock()
	state.State = StateStreaming
	state.StartTime = time.Now()
	c.mu.Unlock()

	c.notifyUpdate(state)
	log.Printf("[USBCam] Streaming started for camera %s\n", cameraID)

	return nil
}

// StopStreaming stops streaming from the active camera
func (c *Controller) StopStreaming(cameraID string) error {
	c.mu.Lock()

	// Check if this camera is the active one
	if c.activeCamera != cameraID {
		c.mu.Unlock()
		return fmt.Errorf("camera %s is not streaming", cameraID)
	}

	state, exists := c.cameraStates[cameraID]
	if !exists {
		c.mu.Unlock()
		return fmt.Errorf("camera not found: %s", cameraID)
	}

	if c.stopCapture == nil {
		c.mu.Unlock()
		return fmt.Errorf("capture handler not configured")
	}

	state.State = StateStopping
	c.mu.Unlock()

	c.notifyUpdate(state)

	// Stop FFmpeg
	if err := c.stopCapture(); err != nil {
		log.Printf("[USBCam] Error stopping capture: %v\n", err)
	}

	c.mu.Lock()
	state.State = StateIdle
	state.StreamConfig = nil
	state.StartTime = time.Time{}
	c.activeCamera = ""
	c.mu.Unlock()

	c.notifyUpdate(state)
	log.Printf("[USBCam] Streaming stopped for camera %s\n", cameraID)

	return nil
}

// StopActiveCamera stops the currently streaming camera
func (c *Controller) StopActiveCamera() error {
	c.mu.RLock()
	active := c.activeCamera
	c.mu.RUnlock()

	if active == "" {
		return nil // No active camera
	}

	return c.StopStreaming(active)
}

// SubscribeUpdates returns a channel for camera state updates
func (c *Controller) SubscribeUpdates() <-chan *CameraState {
	return c.updateChan
}

func (c *Controller) notifyUpdate(state *CameraState) {
	select {
	case c.updateChan <- state:
	default:
		// Channel full, skip notification
	}
}

// DefaultStreamConfig returns default streaming configuration
func DefaultStreamConfig() *StreamConfig {
	return &StreamConfig{
		Width:   1920,
		Height:  1080,
		FPS:     30,
		Bitrate: 6000,
		Encoder: "libx264",
	}
}

// ValidateEncoder checks if the encoder is valid
func ValidateEncoder(encoder string) bool {
	valid := map[string]bool{
		"libx264":      true,
		"libopenh264":  true,
		"h264_vaapi":   true,
		"h264_nvenc":   true,
		"copy":         true,
	}
	return valid[encoder]
}
