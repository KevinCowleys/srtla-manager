package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"srtla-manager/internal/system"
)

func (h *Handler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ffStats := h.ffmpeg.Stats()
	srtlaStats := h.srtla.Stats()

	resp := StatusResponse{
		Uptime:       int64(h.uptime().Seconds()),
		PipelineMode: h.GetPipelineMode(),
		FFmpeg: FFmpegStatus{
			ProcessState: string(h.ffmpeg.ProcessState()),
			State:        ffStats.State,
			Bitrate:      ffStats.Bitrate,
			FPS:          ffStats.FPS,
			Stale:        h.ffmpeg.IsStale(FFmpegStaleThreshold),
		},
		SRTLA: SRTLAStatus{
			ProcessState: string(h.srtla.ProcessState()),
			State:        srtlaStats.State,
			Bitrate:      srtlaStats.TotalBitrate,
			Connections:  srtlaStats.Connections,
			Stale:        h.srtla.IsStale(SRTLAStaleThreshold),
		},
		History: h.stats.History(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleConfigGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.config.Get()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (h *Handler) HandleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cfg interface{}
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Re-marshal to config.Config type
	data, _ := json.Marshal(cfg)
	var newCfg interface{}
	json.Unmarshal(data, &newCfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

type DependenciesResponse struct {
	FFmpeg system.DependencyStatus `json:"ffmpeg"`
	SRTLA  system.DependencyStatus `json:"srtla"`
	OS     string                  `json:"os"`
}

func (h *Handler) HandleDependencies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.config.Get()
	resp := DependenciesResponse{
		FFmpeg: system.CheckFFmpeg(),
		SRTLA:  system.CheckSRTLA(cfg.SRTLA.BinaryPath),
		OS:     system.GetOSInfo(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	interfaces := system.ListNetworkInterfaces()

	// Augment with modem interfaces
	if h.modem != nil {
		modems, err := h.modem.ListModems()
		if err == nil {
			existingIfaces := make(map[string]*system.NetworkInterface)
			for i := range interfaces {
				existingIfaces[interfaces[i].Name] = &interfaces[i]
			}

			for _, modem := range modems {
				if modem.Interface != "" {
					if existing, found := existingIfaces[modem.Interface]; found {
						existing.IsUp = true
					} else {
						newIface := system.NetworkInterface{
							Name:       modem.Interface,
							IPs:        []string{},
							IsUp:       true,
							IsLoopback: false,
						}
						interfaces = append(interfaces, newIface)
						existingIfaces[modem.Interface] = &newIface
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(interfaces)
}

func (h *Handler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logs := h.logs.GetRecent(1000)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	h.wsHub.HandleConnection(w, r)
}
