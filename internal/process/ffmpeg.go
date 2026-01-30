package process

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FFmpegState string

const (
	FFmpegWaiting   FFmpegState = "waiting"
	FFmpegConnected FFmpegState = "connected"
	FFmpegStreaming FFmpegState = "streaming"
	FFmpegStopped   FFmpegState = "stopped"
	FFmpegError     FFmpegState = "error"
)

type FFmpegMode string

const (
	FFmpegModeReceiveOnly FFmpegMode = "receive_only"
	FFmpegModeStreaming   FFmpegMode = "streaming"
)

type FFmpegStats struct {
	State      FFmpegState
	Bitrate    float64 // kbps
	FPS        float64
	Speed      float64
	TotalSize  int64
	Duration   time.Duration
	ClientIP   string
	LastUpdate time.Time
}

type FFmpegHandler struct {
	proc        *Process
	mu          sync.RWMutex
	stats       FFmpegStats
	mode        FFmpegMode
	logCallback func(LogLine)

	bitrateRegex *regexp.Regexp
	fpsRegex     *regexp.Regexp
	sizeRegex    *regexp.Regexp
	speedRegex   *regexp.Regexp
	clientRegex  *regexp.Regexp

	// Camera preview HTTP port mapping
	previewPorts       map[string]int                // camera_id -> HTTP port
	streamBroadcasters map[string]*StreamBroadcaster // camera_id -> broadcaster
}

func NewFFmpegHandler() *FFmpegHandler {
	h := &FFmpegHandler{
		proc: New("ffmpeg"),
		stats: FFmpegStats{
			State: FFmpegStopped,
		},
		bitrateRegex:       regexp.MustCompile(`bitrate=\s*([\d.]+)kbits/s`),
		fpsRegex:           regexp.MustCompile(`fps=\s*([\d.]+)`),
		sizeRegex:          regexp.MustCompile(`size=\s*(\d+)kB`),
		speedRegex:         regexp.MustCompile(`speed=\s*([\d.]+)x`),
		clientRegex:        regexp.MustCompile(`Opening '.*' for (reading|writing)`),
		previewPorts:       make(map[string]int),
		streamBroadcasters: make(map[string]*StreamBroadcaster),
	}

	h.proc.SetLogCallback(h.handleLog)
	return h
}

func (h *FFmpegHandler) SetLogCallback(cb func(LogLine)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logCallback = cb
}

func (h *FFmpegHandler) Mode() FFmpegMode {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.mode
}

func (h *FFmpegHandler) Start(rtmpPort int, streamKey string, srtPort int) error {
	return h.StartWithBindAddress(rtmpPort, streamKey, srtPort, "0.0.0.0")
}

func (h *FFmpegHandler) StartWithBindAddress(rtmpPort int, streamKey string, srtPort int, bindAddr string) error {
	return h.StartWithPreview(rtmpPort, streamKey, srtPort, bindAddr, "")
}

// StartWithPreview behaves like StartWithBindAddress but also tees to an HLS output when hlsDir is provided.
func (h *FFmpegHandler) StartWithPreview(rtmpPort int, streamKey string, srtPort int, bindAddr string, hlsDir string) error {
	h.mu.Lock()
	h.stats = FFmpegStats{State: FFmpegWaiting}
	if srtPort > 0 {
		h.mode = FFmpegModeStreaming
	} else {
		h.mode = FFmpegModeReceiveOnly
	}
	h.mu.Unlock()

	rtmpURL := fmt.Sprintf("rtmp://%s:%d/%s", bindAddr, rtmpPort, streamKey)
	outputs := []string{}

	// SRT leg is optional; skip when srtPort is 0 (e.g., preview-only flow)
	if srtPort > 0 {
		// SRT options for robust streaming:
		// - mode=caller: FFmpeg initiates connection to SRTLA
		// - connect_timeout=10000000: 10 second connection timeout (in microseconds)
		// - latency=200000: 200ms latency buffer (in microseconds)
		// - pkt_size=1316: optimal packet size for MPEG-TS over SRT
		srtURL := fmt.Sprintf("srt://127.0.0.1:%d?mode=caller&connect_timeout=10000000&latency=200000&pkt_size=1316", srtPort)
		outputs = append(outputs, fmt.Sprintf("[f=mpegts]%s", srtURL))
	}
	if hlsDir != "" {
		if err := os.RemoveAll(hlsDir); err != nil {
			return err
		}
		if err := os.MkdirAll(hlsDir, 0777); err != nil {
			return err
		}
		hlsOut := fmt.Sprintf("[f=hls:hls_time=1:hls_list_size=10:hls_flags=delete_segments+omit_endlist]%s/playlist.m3u8", hlsDir)
		outputs = append(outputs, hlsOut)
	}

	// Avoid starting ffmpeg with no outputs defined
	if len(outputs) == 0 {
		return fmt.Errorf("no outputs configured for ffmpeg")
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-listen", "1",
		"-i", rtmpURL,
		"-c", "copy",
		"-map", "0",
		"-f", "tee",
		strings.Join(outputs, "|"),
	}

	return h.proc.Start("ffmpeg", args...)
}

func (h *FFmpegHandler) Stop() error {
	h.mu.Lock()
	h.stats = FFmpegStats{State: FFmpegStopped}
	h.mode = ""
	h.mu.Unlock()
	return h.proc.Stop()
}

// GetPreviewPort returns the HTTP port for a camera's preview stream (or 0 if not active)
func (h *FFmpegHandler) GetPreviewPort(cameraID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.previewPorts[cameraID]
}

// StopPreview stops the preview for a specific camera and cleans up the broadcaster
func (h *FFmpegHandler) StopPreview(cameraID string) error {
	h.mu.Lock()
	broadcaster, ok := h.streamBroadcasters[cameraID]
	delete(h.streamBroadcasters, cameraID)
	h.mu.Unlock()

	// Close the broadcaster if it exists (this will kill the FFmpeg process)
	if ok && broadcaster != nil {
		broadcaster.Close()
	}

	// Clear the port mapping
	h.SetPreviewPort(cameraID, 0)

	return nil
}

// SetPreviewPort stores the HTTP port for a camera's preview stream
func (h *FFmpegHandler) SetPreviewPort(cameraID string, port int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if port > 0 {
		h.previewPorts[cameraID] = port
	} else {
		delete(h.previewPorts, cameraID)
	}
}

// FindFreePort finds an available TCP port starting from basePort
func (h *FFmpegHandler) FindFreePort(basePort int) (int, error) {
	for port := basePort; port < basePort+1000; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free ports available starting from %d", basePort)
}

func (h *FFmpegHandler) Stats() FFmpegStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stats
}

func (h *FFmpegHandler) ProcessState() State {
	return h.proc.State()
}

func (h *FFmpegHandler) handleLog(log LogLine) {
	h.mu.Lock()
	cb := h.logCallback
	h.mu.Unlock()

	if cb != nil {
		cb(log)
	}

	h.parseLogLine(log.Line)
}

func (h *FFmpegHandler) parseLogLine(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if strings.Contains(line, "Opening 'rtmp://") && strings.Contains(line, "reading") {
		h.stats.State = FFmpegConnected
		h.stats.LastUpdate = time.Now()
		if match := regexp.MustCompile(`Opening 'rtmp://([^']+)'`).FindStringSubmatch(line); len(match) > 1 {
			parts := strings.Split(match[1], "/")
			if len(parts) > 0 {
				h.stats.ClientIP = strings.Split(parts[0], ":")[0]
			}
		}
	}

	if strings.Contains(line, "Stream mapping") || strings.Contains(line, "Output #0") {
		h.stats.State = FFmpegStreaming
		h.stats.LastUpdate = time.Now()
	}

	if match := h.bitrateRegex.FindStringSubmatch(line); len(match) > 1 {
		if v, err := strconv.ParseFloat(match[1], 64); err == nil {
			h.stats.Bitrate = v
			h.stats.State = FFmpegStreaming
			h.stats.LastUpdate = time.Now()
		}
	}

	if match := h.fpsRegex.FindStringSubmatch(line); len(match) > 1 {
		if v, err := strconv.ParseFloat(match[1], 64); err == nil {
			h.stats.FPS = v
			h.stats.LastUpdate = time.Now()
		}
	}

	if match := h.sizeRegex.FindStringSubmatch(line); len(match) > 1 {
		if v, err := strconv.ParseInt(match[1], 10, 64); err == nil {
			h.stats.TotalSize = v * 1024
		}
	}

	if match := h.speedRegex.FindStringSubmatch(line); len(match) > 1 {
		if v, err := strconv.ParseFloat(match[1], 64); err == nil {
			h.stats.Speed = v
		}
	}

	if strings.Contains(line, "error") || strings.Contains(line, "Error") {
		if h.stats.State != FFmpegStreaming {
			h.stats.State = FFmpegError
		}
	}
}

// IsStale returns true when the process is running but has not emitted
// any parsed progress information within the given threshold.
func (h *FFmpegHandler) IsStale(threshold time.Duration) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.proc.State() != StateRunning {
		return false
	}

	if h.stats.LastUpdate.IsZero() {
		return false
	}

	return time.Since(h.stats.LastUpdate) > threshold
}

// USBCaptureConfig holds configuration for USB camera capture
type USBCaptureConfig struct {
	DevicePath  string
	Width       int
	Height      int
	FPS         int
	Encoder     string // libx264, h264_vaapi, h264_nvenc, copy
	Bitrate     int    // kbps
	InputFormat string // mjpeg, h264, yuyv422
	SRTPort     int
	HLSDir      string
}

// StartUSBCapture starts capturing from a USB camera via V4L2
func (h *FFmpegHandler) StartUSBCapture(config USBCaptureConfig) error {
	h.mu.Lock()
	h.stats = FFmpegStats{State: FFmpegWaiting}
	if config.SRTPort > 0 {
		h.mode = FFmpegModeStreaming
	} else {
		h.mode = FFmpegModeReceiveOnly
	}
	h.mu.Unlock()

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
	}

	// Add hardware device for VAAPI encoding
	if config.Encoder == "h264_vaapi" {
		args = append(args, "-vaapi_device", "/dev/dri/renderD128")
	}

	// Input options
	args = append(args, "-f", "v4l2")

	// Set input format based on camera capabilities
	switch config.InputFormat {
	case "h264":
		args = append(args, "-input_format", "h264")
	case "mjpeg":
		args = append(args, "-input_format", "mjpeg")
	case "yuyv422":
		args = append(args, "-input_format", "yuyv422")
	}

	args = append(args,
		"-framerate", fmt.Sprintf("%d", config.FPS),
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-i", config.DevicePath,
	)

	// Encoding options based on encoder type
	var videoCodec []string
	switch config.Encoder {
	case "copy":
		// Passthrough - no re-encoding (for cameras with H.264 output)
		videoCodec = []string{"-c:v", "copy"}
	case "h264_vaapi":
		// VAAPI hardware encoding (Intel/AMD)
		videoCodec = []string{
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			"-b:v", fmt.Sprintf("%dk", config.Bitrate),
		}
	case "h264_nvenc":
		// NVIDIA hardware encoding
		videoCodec = []string{
			"-c:v", "h264_nvenc",
			"-preset", "p4",
			"-tune", "ll",
			"-b:v", fmt.Sprintf("%dk", config.Bitrate),
		}
	case "libopenh264":
		// OpenH264 software encoding (available on distros without libx264)
		// Add format conversion filter to handle yuvj422p -> yuvj420p colorspace conversion
		videoCodec = []string{
			"-vf", "format=yuvj420p",
			"-c:v", "libopenh264",
			"-b:v", fmt.Sprintf("%dk", config.Bitrate),
		}
	case "libx264":
		// libx264 software encoding
		videoCodec = []string{
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-b:v", fmt.Sprintf("%dk", config.Bitrate),
		}
	default:
		// Auto-detect best software encoder
		encoder := DetectSoftwareEncoder()
		if encoder == "libopenh264" {
			videoCodec = []string{
				"-vf", "format=yuvj420p",
				"-c:v", "libopenh264",
				"-b:v", fmt.Sprintf("%dk", config.Bitrate),
			}
		} else {
			videoCodec = []string{
				"-c:v", "libx264",
				"-preset", "ultrafast",
				"-tune", "zerolatency",
				"-b:v", fmt.Sprintf("%dk", config.Bitrate),
			}
		}
	}

	args = append(args, videoCodec...)

	// Prepare HLS directory if needed
	if config.HLSDir != "" {
		if err := os.RemoveAll(config.HLSDir); err != nil {
			return err
		}
		if err := os.MkdirAll(config.HLSDir, 0777); err != nil {
			return err
		}
	}

	hasSRT := config.SRTPort > 0
	hasHLS := config.HLSDir != ""

	if !hasSRT && !hasHLS {
		return fmt.Errorf("no outputs configured for ffmpeg")
	}

	if hasSRT && hasHLS {
		// Multiple outputs — use tee muxer
		srtURL := fmt.Sprintf("srt://127.0.0.1:%d?mode=caller&connect_timeout=10000000&latency=200000&pkt_size=1316", config.SRTPort)
		hlsOut := fmt.Sprintf("[f=hls:hls_time=1:hls_list_size=10:hls_flags=delete_segments+omit_endlist]%s/playlist.m3u8", config.HLSDir)
		teeOutput := fmt.Sprintf("[f=mpegts]%s|%s", srtURL, hlsOut)
		args = append(args, "-map", "0", "-f", "tee", teeOutput)
	} else if hasSRT {
		// SRT only
		srtURL := fmt.Sprintf("srt://127.0.0.1:%d?mode=caller&connect_timeout=10000000&latency=200000&pkt_size=1316", config.SRTPort)
		args = append(args, "-f", "mpegts", srtURL)
	} else {
		// HLS only — output directly with explicit HLS options
		args = append(args,
			"-f", "hls",
			"-hls_time", "1",
			"-hls_list_size", "10",
			"-hls_flags", "delete_segments+omit_endlist",
			"-flvflags", "+discardcorrupt",
			fmt.Sprintf("%s/playlist.m3u8", config.HLSDir),
		)
	}

	return h.proc.Start("ffmpeg", args...)
}

// StartUSBCameraStream starts capturing from a USB camera and streams MJPEG to stdout
// This is used for real-time HTTP streaming previews (no files, instant playback)
func (h *FFmpegHandler) StartUSBCameraStream(config USBCaptureConfig) error {
	h.mu.Lock()
	h.stats = FFmpegStats{State: FFmpegWaiting}
	h.mode = FFmpegModeReceiveOnly
	h.mu.Unlock()

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-f", "v4l2",
	}

	// Set input format based on camera capabilities
	switch config.InputFormat {
	case "h264":
		args = append(args, "-input_format", "h264")
	case "mjpeg":
		args = append(args, "-input_format", "mjpeg")
	case "yuyv422":
		args = append(args, "-input_format", "yuyv422")
	}

	args = append(args,
		"-framerate", fmt.Sprintf("%d", config.FPS),
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-i", config.DevicePath,
		"-vf", "format=yuvj420p",
	)

	// For MJPEG streaming, use mjpeg encoder with quality tuning
	args = append(args,
		"-c:v", "mjpeg",
		"-q:v", "5", // Quality (1-31, lower is better)
		"-f", "mjpeg",
		"pipe:1", // Output to stdout
	)

	return h.proc.Start("ffmpeg", args...)
}

// StartUSBCameraHTTPPreview starts an HTTP server for camera MJPEG preview
// Returns the port number where the stream is available
func (h *FFmpegHandler) StartUSBCameraHTTPPreview(cameraID string, config USBCaptureConfig) (int, error) {
	// Find a free port (start from 19100)
	port, err := h.FindFreePort(19100)
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}

	h.mu.Lock()
	h.stats = FFmpegStats{State: FFmpegWaiting}
	h.mode = FFmpegModeReceiveOnly
	h.mu.Unlock()

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-f", "v4l2",
	}

	// Set input format - MUST come before framerate and video_size
	switch config.InputFormat {
	case "h264":
		args = append(args, "-input_format", "h264")
	case "mjpeg":
		args = append(args, "-input_format", "mjpeg")
	case "nv12":
		args = append(args, "-input_format", "nv12")
	case "yuyv422":
		args = append(args, "-input_format", "yuyv422")
	}

	// Output MJPEG to stdout
	args = append(args,
		"-framerate", fmt.Sprintf("%d", config.FPS),
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-i", config.DevicePath,
		"-vf", "format=yuvj420p",
		"-c:v", "mjpeg",
		"-q:v", "5",
		"-f", "mpjpeg",
		"pipe:1", // Output to stdout
	)

	// Log the full FFmpeg command for debugging
	log.Printf("[FFmpeg Preview] Starting FFmpeg with command: ffmpeg %s", strings.Join(args, " "))

	// Create a broadcaster to handle multiple HTTP connections
	broadcaster := NewStreamBroadcaster()
	broadcaster.restartEnabled = true
	broadcaster.cameraID = cameraID
	broadcaster.ffmpegArgs = args
	broadcaster.handler = h
	h.mu.Lock()
	h.streamBroadcasters[cameraID] = broadcaster
	h.mu.Unlock()

	// Start FFmpeg with custom stdout handling
	if err := h.startFFmpegWithBroadcast(broadcaster, "ffmpeg", args...); err != nil {
		h.mu.Lock()
		delete(h.streamBroadcasters, cameraID)
		h.mu.Unlock()
		return 0, err
	}

	// Store the port for this camera (broadcaster serves on this port)
	h.SetPreviewPort(cameraID, port)
	go broadcaster.ListenAndServe(port)

	return port, nil
}

// DetectSoftwareEncoder probes ffmpeg for available H.264 software encoders
// and returns the best one. Prefers libx264 > libopenh264.
func DetectSoftwareEncoder() string {
	out, err := exec.Command("ffmpeg", "-encoders", "-hide_banner").Output()
	if err != nil {
		return "libx264" // fallback, let ffmpeg error if missing
	}
	encoders := string(out)
	if strings.Contains(encoders, "libx264") {
		return "libx264"
	}
	if strings.Contains(encoders, "libopenh264") {
		return "libopenh264"
	}
	return "libx264"
}

// StreamBroadcaster broadcasts MJPEG stream to multiple HTTP clients
type StreamBroadcaster struct {
	mu        sync.RWMutex
	clients   map[chan []byte]bool
	newData   chan []byte
	closeOnce sync.Once
	closed    bool
	cmd       *exec.Cmd // FFmpeg process
	// Auto-restart fields
	restartEnabled bool
	cameraID       string
	ffmpegArgs     []string
	retryCount     int
	maxRetries     int
	handler        *FFmpegHandler
}

func NewStreamBroadcaster() *StreamBroadcaster {
	return &StreamBroadcaster{
		clients:    make(map[chan []byte]bool),
		newData:    make(chan []byte, 10),
		maxRetries: 10, // Allow up to 10 restart attempts
	}
}

func (sb *StreamBroadcaster) AddClient() chan []byte {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	ch := make(chan []byte, 10)
	sb.clients[ch] = true
	return ch
}

func (sb *StreamBroadcaster) RemoveClient(ch chan []byte) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if _, ok := sb.clients[ch]; ok {
		delete(sb.clients, ch)
		close(ch)
	}
}

func (sb *StreamBroadcaster) Broadcast(data []byte) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if sb.closed {
		return
	}
	for ch := range sb.clients {
		select {
		case ch <- data:
		default:
			// Skip if client is slow
		}
	}
}

func (sb *StreamBroadcaster) Close() {
	sb.closeOnce.Do(func() {
		sb.mu.Lock()
		sb.closed = true
		sb.restartEnabled = false // Disable auto-restart on intentional close
		// Kill FFmpeg process if it exists
		if sb.cmd != nil && sb.cmd.Process != nil {
			sb.cmd.Process.Kill()
		}
		for ch := range sb.clients {
			close(ch)
		}
		sb.clients = make(map[chan []byte]bool)
		sb.mu.Unlock()
		close(sb.newData)
	})
}

func (sb *StreamBroadcaster) ListenAndServe(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/stream-%d", port), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=ffmpeg")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		clientChan := sb.AddClient()
		defer sb.RemoveClient(clientChan)

		// Flush headers immediately
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		for data := range clientChan {
			n, err := w.Write(data)
			if err != nil || n != len(data) {
				// Client disconnected
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	return http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux)
}

// startFFmpegWithBroadcast starts FFmpeg and broadcasts stdout to all clients
func (h *FFmpegHandler) startFFmpegWithBroadcast(broadcaster *StreamBroadcaster, cmdPath string, args ...string) error {
	// Preview processes are independent from the main RTMP receiver process
	// Don't interfere with h.proc here

	cmd := exec.Command(cmdPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Store cmd in broadcaster so it can be killed on Close()
	broadcaster.mu.Lock()
	broadcaster.cmd = cmd
	broadcaster.mu.Unlock()

	// Monitor stderr for errors
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				// Log FFmpeg errors
				log.Printf("[FFmpeg Preview] %s", string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()

	// Monitor process and broadcast stdout to all clients
	go func() {
		defer func() {
			if broadcaster.cmd != nil && broadcaster.cmd.Process != nil {
				broadcaster.cmd.Process.Kill()
			}
		}()

		buf := make([]byte, 32768)
		streamingStartTime := time.Now()
		bytesReceived := 0
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				bytesReceived += n
				data := make([]byte, n)
				copy(data, buf[:n])
				broadcaster.Broadcast(data)

				// Reset retry count if stream has been running successfully for >30 seconds
				if bytesReceived > 100000 && time.Since(streamingStartTime) > 30*time.Second {
					broadcaster.mu.Lock()
					if broadcaster.retryCount > 0 {
						log.Printf("[FFmpeg Preview] Resetting retry count for camera %s (streaming successfully)", broadcaster.cameraID)
						broadcaster.retryCount = 0
					}
					broadcaster.mu.Unlock()
					bytesReceived = 0 // Reset to check again in another 30s
				}
			}
			if err != nil {
				// Stream ended
				break
			}
		}
	}()

	// Monitor process exit and handle auto-restart
	go func() {
		cmd.Wait()

		broadcaster.mu.RLock()
		restartEnabled := broadcaster.restartEnabled
		closed := broadcaster.closed
		retryCount := broadcaster.retryCount
		maxRetries := broadcaster.maxRetries
		broadcaster.mu.RUnlock()

		// Check if intentional close or if restart is disabled
		if closed || !restartEnabled {
			return
		}

		// Check if we've exceeded max retries
		if retryCount >= maxRetries {
			log.Printf("[FFmpeg Preview] Max retries (%d) exceeded for camera %s, giving up", maxRetries, broadcaster.cameraID)
			broadcaster.Close()
			return
		}

		// Exponential backoff: 1s, 2s, 4s, 8s, capped at 30s
		backoff := time.Duration(1<<uint(retryCount)) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}

		log.Printf("[FFmpeg Preview] Restarting preview for camera %s (attempt %d/%d) after %v",
			broadcaster.cameraID, retryCount+1, maxRetries, backoff)

		time.Sleep(backoff)

		// Increment retry count
		broadcaster.mu.Lock()
		broadcaster.retryCount++
		broadcaster.mu.Unlock()

		// Restart FFmpeg
		if err := h.startFFmpegWithBroadcast(broadcaster, cmdPath, broadcaster.ffmpegArgs...); err != nil {
			log.Printf("[FFmpeg Preview] Failed to restart preview for camera %s: %v", broadcaster.cameraID, err)
			// Don't close, let it retry again
		}
	}()

	return nil
}
