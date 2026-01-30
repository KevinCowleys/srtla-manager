package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"srtla-manager/internal/process"
	"srtla-manager/internal/system"
)

func (h *Handler) HandleStreamStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.config.Get()

	// Pre-flight validation before starting any processes
	if err := cfg.Validate(); err != nil {
		jsonError(w, fmt.Sprintf("Cannot start stream: invalid configuration - %v", err), http.StatusBadRequest)
		return
	}

	// Check if already streaming
	if h.GetPipelineMode() == PipelineModeStreaming {
		jsonError(w, "Already streaming", http.StatusBadRequest)
		return
	}

	// Determine available bind IPs if SRTLA is enabled
	var availableIPs []string
	if cfg.SRTLA.Enabled && len(cfg.SRTLA.BindIPs) > 0 {
		interfaces := system.ListNetworkInterfaces()
		systemIPs := make(map[string]bool)
		for _, iface := range interfaces {
			for _, ip := range iface.IPs {
				systemIPs[ip] = true
			}
		}

		var unavailableIPs []string
		for _, ip := range cfg.SRTLA.BindIPs {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			if systemIPs[ip] {
				availableIPs = append(availableIPs, ip)
			} else {
				unavailableIPs = append(unavailableIPs, ip)
			}
		}

		// Require at least 1 IP available
		if len(availableIPs) == 0 {
			jsonError(w, "Cannot start stream: no bind IPs available on system. Check modem/USB network status.", http.StatusBadRequest)
			return
		}

		// Warn about unavailable IPs (don't block)
		if len(unavailableIPs) > 0 {
			h.logOutput("manager", fmt.Sprintf("[WARNING] Starting with %d/%d IPs. Unavailable: %s",
				len(availableIPs), len(cfg.SRTLA.BindIPs), strings.Join(unavailableIPs, ", ")))
		}
	}

	// Check SRTLA not already running
	if cfg.SRTLA.Enabled && h.srtla.ProcessState() == process.StateRunning {
		jsonError(w, "Cannot start stream: SRTLA is already running", http.StatusBadRequest)
		return
	}

	// Start SRTLA first so it's listening on the SRT port before FFmpeg tries to connect
	if cfg.SRTLA.Enabled && len(availableIPs) > 0 {
		if err := h.startSRTLA(&cfg, availableIPs); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.activeBindIPs = availableIPs
	}

	bindAddr := h.getBindAddr()

	// Stop current FFmpeg (receive-only mode) and restart with SRT output
	h.ffmpeg.Stop()
	time.Sleep(300 * time.Millisecond)
	h.cleanPreviewDir()

	// Restart FFmpeg with SRT output (streaming mode)
	if err := h.ffmpeg.StartWithPreview(cfg.RTMP.ListenPort, cfg.RTMP.StreamKey, cfg.SRT.LocalPort, bindAddr, h.previewDir); err != nil {
		// If FFmpeg fails, stop SRTLA and try to restore receive mode
		if cfg.SRTLA.Enabled {
			h.srtla.Stop()
		}
		// Try to restore receive-only mode
		_ = h.ffmpeg.StartWithPreview(cfg.RTMP.ListenPort, cfg.RTMP.StreamKey, 0, bindAddr, h.previewDir)
		h.SetPipelineMode(PipelineModeReceiving)
		go h.monitorReceiveHealth(bindAddr)
		jsonError(w, fmt.Sprintf("Failed to start FFmpeg with SRT: %v", err), http.StatusInternalServerError)
		return
	}

	h.SetPipelineMode(PipelineModeStreaming)

	// Start streaming-mode health monitor
	go h.monitorPipelineHealth(bindAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (h *Handler) HandleStreamStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.config.Get()

	// Signal health monitors to stop by transitioning mode first
	h.SetPipelineMode(PipelineModeIdle)

	// Stop SRTLA and FFmpeg
	h.srtla.Stop()
	h.ffmpeg.Stop()
	time.Sleep(300 * time.Millisecond)

	// Restart FFmpeg in receive-only mode
	bindAddr := h.getBindAddr()
	h.cleanPreviewDir()

	if err := h.ffmpeg.StartWithPreview(cfg.RTMP.ListenPort, cfg.RTMP.StreamKey, 0, bindAddr, h.previewDir); err != nil {
		h.logOutput("manager", fmt.Sprintf("[WARNING] Failed to restart FFmpeg in receive mode: %v", err))
		h.SetPipelineMode(PipelineModeIdle)
	} else {
		h.SetPipelineMode(PipelineModeReceiving)
		h.logOutput("manager", "[FFmpeg] Restarted in receive-only mode")
		go h.monitorReceiveHealth(bindAddr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// monitorReceiveHealth monitors FFmpeg in receive-only mode and restarts it if it crashes.
// Uses exponential backoff to avoid hammering failed processes (e.g. port already in use).
func (h *Handler) monitorReceiveHealth(bindAddr string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	consecutiveFailures := 0
	backoff := InitialBackoff
	lastFailure := time.Time{}

	for range ticker.C {
		// Stop this monitor if mode has changed
		if h.GetPipelineMode() != PipelineModeReceiving {
			return
		}

		cfg := h.config.Get()

		ffState := h.ffmpeg.ProcessState()
		if ffState != process.StateRunning {
			// Re-check mode before restarting — another handler (e.g. USB camera
			// preview) may have intentionally stopped FFmpeg and switched to idle
			// between our mode check above and this point.
			if h.GetPipelineMode() != PipelineModeReceiving {
				return
			}

			// Apply backoff if we've had recent failures
			if consecutiveFailures > 0 && !lastFailure.IsZero() && time.Since(lastFailure) < backoff {
				continue // Skip this tick, wait for backoff to elapse
			}

			h.logOutput("manager", "[AUTO-RESTART] FFmpeg stopped in receive mode, restarting...")

			if err := h.ffmpeg.StartWithPreview(cfg.RTMP.ListenPort, cfg.RTMP.StreamKey, 0, bindAddr, h.previewDir); err != nil {
				h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] Failed to restart FFmpeg in receive mode: %v (retry in %v)", err, backoff))
				consecutiveFailures++
				lastFailure = time.Now()
				newBackoff := time.Duration(float64(backoff) * BackoffMultiplier)
				if newBackoff > MaxBackoff {
					newBackoff = MaxBackoff
				}
				backoff = newBackoff
			} else {
				// Process launched — verify it actually stays alive briefly.
				// FFmpeg may start then immediately exit (e.g. port in use).
				time.Sleep(1 * time.Second)
				if h.ffmpeg.ProcessState() != process.StateRunning {
					h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] FFmpeg started but immediately exited (retry in %v)", backoff))
					consecutiveFailures++
					lastFailure = time.Now()
					newBackoff := time.Duration(float64(backoff) * BackoffMultiplier)
					if newBackoff > MaxBackoff {
						newBackoff = MaxBackoff
					}
					backoff = newBackoff
				} else {
					h.logOutput("manager", "[AUTO-RESTART] FFmpeg restarted in receive mode")
					consecutiveFailures = 0
					backoff = InitialBackoff
					lastFailure = time.Time{}
				}
			}
		} else {
			// FFmpeg is running — reset backoff state
			if consecutiveFailures > 0 {
				consecutiveFailures = 0
				backoff = InitialBackoff
				lastFailure = time.Time{}
			}
		}
	}
}

// monitorPipelineHealth monitors FFmpeg/SRTLA in streaming mode and restarts them if they crash or stall.
// Uses exponential backoff to avoid hammering failed processes.
// Also detects when new bind IPs become available and reloads SRTLA to use them.
func (h *Handler) monitorPipelineHealth(bindAddr string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Stop monitoring if no longer in streaming mode
		if h.GetPipelineMode() != PipelineModeStreaming {
			return
		}

		cfg := h.config.Get()

		if cfg.SRTLA.Enabled && len(cfg.SRTLA.BindIPs) > 0 {
			srtlaState := h.srtla.ProcessState()
			srtlaStale := h.srtla.IsStale(SRTLAStaleThreshold)

			// Check if SRTLA needs restart
			if srtlaState != process.StateRunning || srtlaStale {
				reason := "stopped"
				if srtlaStale {
					reason = fmt.Sprintf("stale for >%ds", int(SRTLAStaleThreshold.Seconds()))
				}

				if h.shouldRestartWithBackoff(h.srtlaRestarts, reason, "SRTLA") {
					// Re-evaluate available IPs before restart
					availableIPs := h.getAvailableBindIPs(&cfg)
					if len(availableIPs) == 0 {
						h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] SRTLA %s, but no bind IPs available. Waiting...", reason))
						continue // Skip restart, try again next tick
					}

					h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] SRTLA %s, restarting with %d IPs...", reason, len(availableIPs)))
					_ = h.srtla.Stop()
					if err := h.startSRTLA(&cfg, availableIPs); err != nil {
						h.recordRestartFailure(h.srtlaRestarts)
						h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] Failed to restart SRTLA: %v", err))
					} else {
						h.recordRestartSuccess(h.srtlaRestarts)
						h.activeBindIPs = availableIPs
						h.logOutput("manager", "[AUTO-RESTART] SRTLA restarted successfully")
						// Give it a moment before checking FFmpeg again
						time.Sleep(time.Second)
					}
				}
			} else {
				// SRTLA is running - check if any new IPs have become available
				currentAvailable := h.getAvailableBindIPs(&cfg)

				// Find newly available IPs not currently in use
				activeSet := make(map[string]bool)
				for _, ip := range h.activeBindIPs {
					activeSet[ip] = true
				}

				var newIPs []string
				for _, ip := range currentAvailable {
					if !activeSet[ip] {
						newIPs = append(newIPs, ip)
					}
				}

				// If new IPs available, reload SRTLA to include them
				if len(newIPs) > 0 {
					h.logOutput("manager", fmt.Sprintf("[IP-RECOVERY] Detected %d new IPs: %s. Reloading...",
						len(newIPs), strings.Join(newIPs, ", ")))
					if err := h.srtla.ReloadIPs(currentAvailable); err != nil {
						h.logOutput("manager", fmt.Sprintf("[IP-RECOVERY] Reload failed: %v", err))
					} else {
						h.activeBindIPs = currentAvailable
						h.logOutput("manager", fmt.Sprintf("[IP-RECOVERY] Now using %d IPs", len(currentAvailable)))
					}
				}
			}
		}

		// Check if FFmpeg is still running (in streaming mode, restart with SRT)
		ffState := h.ffmpeg.ProcessState()
		ffStale := h.ffmpeg.IsStale(FFmpegStaleThreshold)
		if ffState != process.StateRunning || ffStale {
			reason := "stopped unexpectedly"
			if ffStale {
				reason = fmt.Sprintf("stalled for >%ds", int(FFmpegStaleThreshold.Seconds()))
			}

			if h.shouldRestartWithBackoff(h.ffmpegRestarts, reason, "FFmpeg") {
				h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] FFmpeg %s, restarting in streaming mode...", reason))

				if err := h.ffmpeg.StartWithPreview(cfg.RTMP.ListenPort, cfg.RTMP.StreamKey, cfg.SRT.LocalPort, bindAddr, h.previewDir); err != nil {
					h.recordRestartFailure(h.ffmpegRestarts)
					h.logOutput("manager", fmt.Sprintf("[AUTO-RESTART] Failed to restart FFmpeg: %v", err))
					// Will retry on next tick with backoff
				} else {
					h.recordRestartSuccess(h.ffmpegRestarts)
					h.logOutput("manager", "[AUTO-RESTART] FFmpeg restarted successfully in streaming mode")
				}
			}
		}
	}
}
