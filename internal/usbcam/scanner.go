package usbcam

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// VideoFormat represents a supported video format for a camera
type VideoFormat struct {
	PixelFormat string `json:"pixel_format"` // MJPEG, YUYV, H264, etc.
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         []int  `json:"fps"`
}

// USBCamera represents a detected USB camera device
type USBCamera struct {
	ID         string        `json:"id"`          // Sanitized ID for API use (e.g., "video0")
	DevicePath string        `json:"device_path"` // Full path (e.g., "/dev/video0")
	Name       string        `json:"name"`        // Device name from v4l2
	Driver     string        `json:"driver"`      // Driver name
	BusInfo    string        `json:"bus_info"`    // USB bus info
	Formats    []VideoFormat `json:"formats"`
	IsElgato   bool          `json:"is_elgato"`
	IsCapture  bool          `json:"is_capture"` // True if this is a capture device (not metadata)
}

// Scanner handles USB camera discovery
type Scanner struct {
	mu      sync.RWMutex
	cameras map[string]*USBCamera
}

// NewScanner creates a new USB camera scanner
func NewScanner() *Scanner {
	return &Scanner{
		cameras: make(map[string]*USBCamera),
	}
}

// Scan detects all USB cameras on the system
func (s *Scanner) Scan() ([]*USBCamera, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cameras = make(map[string]*USBCamera)

	// Find all video devices
	devices, err := filepath.Glob("/dev/video*")
	if err != nil {
		return nil, fmt.Errorf("failed to glob video devices: %w", err)
	}

	// Get friendly device names from v4l2-ctl if available (optional)
	deviceNames := s.getDeviceNames()

	for _, devPath := range devices {
		cam, err := s.probeDevice(devPath, deviceNames)
		if err != nil {
			log.Printf("[USBCam] Failed to probe %s: %v\n", devPath, err)
			continue
		}
		if cam != nil && cam.IsCapture {
			s.cameras[cam.ID] = cam
			log.Printf("[USBCam] Found camera: %s (%s) - %s\n", cam.Name, cam.ID, cam.DevicePath)
		}
	}

	return s.getCameraList(), nil
}

// GetCameras returns all detected cameras
func (s *Scanner) GetCameras() []*USBCamera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getCameraList()
}

// GetCamera returns a specific camera by ID
func (s *Scanner) GetCamera(id string) *USBCamera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cameras[id]
}

func (s *Scanner) getCameraList() []*USBCamera {
	cameras := make([]*USBCamera, 0, len(s.cameras))
	for _, cam := range s.cameras {
		cameras = append(cameras, cam)
	}
	// Sort by device path for consistent ordering
	sort.Slice(cameras, func(i, j int) bool {
		return cameras[i].DevicePath < cameras[j].DevicePath
	})
	return cameras
}

// getDeviceNames uses v4l2-ctl to get friendly device names (optional, best-effort)
func (s *Scanner) getDeviceNames() map[string]string {
	names := make(map[string]string)

	cmd := exec.Command("v4l2-ctl", "--list-devices")
	output, err := cmd.Output()
	if err != nil {
		// v4l2-ctl not installed or failed — not critical, we use ioctls for detection
		return names
	}

	// Parse output like:
	// Logitech Webcam C930e (usb-0000:00:14.0-4):
	//     /dev/video0
	//     /dev/video1
	lines := strings.Split(string(output), "\n")
	var currentName string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "/dev/") {
			// This is a device name line
			// Extract name before the parentheses
			if idx := strings.Index(line, " ("); idx > 0 {
				currentName = line[:idx]
			} else if idx := strings.Index(line, ":"); idx > 0 {
				currentName = line[:idx]
			} else {
				currentName = line
			}
		} else {
			// This is a device path
			names[line] = currentName
		}
	}

	return names
}

// probeDevice probes a single video device using V4L2 ioctls
func (s *Scanner) probeDevice(devPath string, deviceNames map[string]string) (*USBCamera, error) {
	// Check if device exists and is a character device
	info, err := os.Stat(devPath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("not a character device")
	}

	cam := &USBCamera{
		ID:         filepath.Base(devPath),
		DevicePath: devPath,
		Name:       deviceNames[devPath], // from v4l2-ctl if available
		Formats:    []VideoFormat{},
	}

	// Query device capabilities using VIDIOC_QUERYCAP ioctl
	cap, err := queryCapability(devPath)
	if err != nil {
		log.Printf("[USBCam] Failed to query capabilities for %s: %v\n", devPath, err)
		return cam, nil // Return cam with IsCapture=false
	}

	// Extract device info from capability struct
	cam.Driver = bytesToString(cap.Driver[:])
	cam.BusInfo = bytesToString(cap.BusInfo[:])
	if cam.Name == "" {
		cam.Name = bytesToString(cap.Card[:])
	}

	// Check if this is a video capture device (using device_caps when available)
	cam.IsCapture = isVideoCaptureDevice(cap)

	// Enumerate formats using ioctls
	if cam.IsCapture {
		formats, err := enumFormats(devPath)
		if err != nil {
			log.Printf("[USBCam] Failed to enumerate formats for %s: %v\n", devPath, err)
		} else {
			cam.Formats = formats
		}
	}

	// Check if Elgato device
	cam.IsElgato = isElgatoDevice(cam.Name, cam.BusInfo)

	// Fallback: try sysfs for device name
	if cam.Name == "" {
		cam.Name = getDeviceNameFromSysfs(devPath)
	}

	// Final fallback
	if cam.Name == "" {
		cam.Name = fmt.Sprintf("Video Device %s", cam.ID)
	}

	return cam, nil
}

// getDeviceNameFromSysfs reads the device name from sysfs
func getDeviceNameFromSysfs(devPath string) string {
	base := filepath.Base(devPath)
	nameFile := filepath.Join("/sys/class/video4linux", base, "name")
	data, err := os.ReadFile(nameFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// isElgatoDevice checks if the device is an Elgato camera
func isElgatoDevice(name, busInfo string) bool {
	nameLower := strings.ToLower(name)
	return strings.Contains(nameLower, "elgato") ||
		strings.Contains(nameLower, "cam link") ||
		strings.Contains(nameLower, "facecam") ||
		strings.Contains(nameLower, "game capture")
}

// HasH264Support checks if the camera supports H.264 output (like Cam Link)
func (c *USBCamera) HasH264Support() bool {
	for _, f := range c.Formats {
		if f.PixelFormat == "H264" {
			return true
		}
	}
	return false
}

// GetBestFormat returns the best format for streaming
// Prefers H264 > MJPG > YUYV at the highest resolution
func (c *USBCamera) GetBestFormat(targetWidth, targetHeight int) *VideoFormat {
	// Priority order for formats
	formatPriority := map[string]int{
		"H264": 3,
		"MJPG": 2,
		"YUYV": 1,
	}

	var bestFormat *VideoFormat
	bestScore := -1

	for i := range c.Formats {
		f := &c.Formats[i]

		// Check if resolution matches or is close
		if targetWidth > 0 && f.Width != targetWidth {
			continue
		}
		if targetHeight > 0 && f.Height != targetHeight {
			continue
		}

		priority := formatPriority[f.PixelFormat]
		if priority > bestScore {
			bestScore = priority
			bestFormat = f
		}
	}

	// If no exact match, return highest priority format at any resolution
	if bestFormat == nil && len(c.Formats) > 0 {
		for i := range c.Formats {
			f := &c.Formats[i]
			priority := formatPriority[f.PixelFormat]
			if priority > bestScore {
				bestScore = priority
				bestFormat = f
			}
		}
	}

	return bestFormat
}

// probeAdditionalFormats probes for higher resolutions that might not be enumerated via V4L2
// but are actually supported by the device (common with capture cards like Elgato Cam Link 4K)
func probeAdditionalFormats(devPath string, cam *USBCamera) {
	// Only probe if we haven't already found any working formats
	// or if we only found very limited formats
	if len(cam.Formats) == 0 {
		return // Can't probe a device with no formats
	}

	// Check if the highest resolution is 1280x720 or lower
	// This is a sign the device might support higher resolutions
	maxRes := 0
	for _, fmt := range cam.Formats {
		res := fmt.Width * fmt.Height
		if res > maxRes {
			maxRes = res
		}
	}

	// Only probe if max resolution is 1280x720 or lower (implies capture card limitation)
	if maxRes > 1280*720 {
		return
	}

	log.Printf("[USBCam] Max resolution for %s is %d pixels, probing for 4K\n", devPath, maxRes)

	// Try 4K with common formats
	// Don't probe if device is actively in use - just add 4K as a hint
	// The user's system will use this and if it works, great - if not, it'll downscale
	probeRes := [][2]int{
		{3840, 2160}, // 4K UHD
	}

	for _, size := range probeRes {
		width, height := size[0], size[1]

		// Check if this resolution is already in formats
		found := false
		for _, fmt := range cam.Formats {
			if fmt.Width == width && fmt.Height == height {
				found = true
				break
			}
		}

		if !found {
			// Add 4K as a hint - use the first detected format
			// The user can try it and FFmpeg will auto-downscale if needed
			if len(cam.Formats) > 0 {
				firstFormat := cam.Formats[0]
				log.Printf("[USBCam] Adding 4K hint for %s: %s %dx%d\n", devPath, firstFormat.PixelFormat, width, height)
				cam.Formats = append(cam.Formats, VideoFormat{
					PixelFormat: firstFormat.PixelFormat,
					Width:       width,
					Height:      height,
					FPS:         firstFormat.FPS,
				})
			}
		}
	}

	// Re-sort formats after adding hints
	sort.Slice(cam.Formats, func(i, j int) bool {
		resI := cam.Formats[i].Width * cam.Formats[i].Height
		resJ := cam.Formats[j].Width * cam.Formats[j].Height
		if resI != resJ {
			return resI > resJ
		}
		return cam.Formats[i].PixelFormat < cam.Formats[j].PixelFormat
	})
}

// pixelFormatToFFmpegInput converts a V4L2 pixel format name to FFmpeg input format
func pixelFormatToFFmpegInput(pixFmt string) string {
	switch pixFmt {
	case "MJPG":
		return "mjpeg"
	case "NV12":
		return "nv12"
	case "YUYV":
		return "yuyv422"
	case "H264":
		return "h264"
	default:
		return strings.ToLower(pixFmt)
	}
}
