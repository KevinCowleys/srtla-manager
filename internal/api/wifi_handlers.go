package api

import (
	"encoding/json"
	"net/http"

	"srtla-manager/internal/wifi"
)

func (h *Handler) HandleWiFi(w http.ResponseWriter, r *http.Request) {
	if h.wifiMgr == nil {
		http.Error(w, "WiFi manager not available", http.StatusServiceUnavailable)
		return
	}

	switch r.URL.Path {
	case "/api/wifi/networks":
		h.handleWiFiNetworks(w, r)
	case "/api/wifi/status":
		h.handleWiFiStatus(w, r)
	case "/api/wifi/connect":
		h.handleWiFiConnect(w, r)
	case "/api/wifi/disconnect":
		h.handleWiFiDisconnect(w, r)
	case "/api/wifi/hotspot":
		h.handleWiFiHotspot(w, r)
	case "/api/wifi/hotspot/stop":
		h.handleWiFiHotspotStop(w, r)
	case "/api/wifi/forget":
		h.handleWiFiForget(w, r)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

func (h *Handler) handleWiFiNetworks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	networks, err := h.wifiMgr.ListNetworks()
	if err != nil {
		networks = []wifi.NetworkInfo{}
	}

	resp := WiFiNetworksResponse{
		Available: h.wifiMgr.IsAvailable(),
		Networks:  networks,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleWiFiStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conn := h.wifiMgr.GetConnectionStatus()
	resp := WiFiStatusResponse{Connection: conn}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleWiFiConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req WiFiConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.SSID == "" {
		http.Error(w, "SSID required", http.StatusBadRequest)
		return
	}

	err := h.wifiMgr.Connect(req.SSID, req.Password)
	resp := WiFiActionResponse{
		Success: err == nil,
		Message: "",
	}
	if err != nil {
		resp.Message = err.Error()
	} else {
		resp.Message = "Connected to " + req.SSID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleWiFiDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := h.wifiMgr.Disconnect()
	resp := WiFiActionResponse{
		Success: err == nil,
		Message: "",
	}
	if err != nil {
		resp.Message = err.Error()
	} else {
		resp.Message = "Disconnected from WiFi"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleWiFiHotspot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req WiFiHotspotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.SSID == "" || req.Password == "" {
		http.Error(w, "SSID and password required", http.StatusBadRequest)
		return
	}

	config := wifi.HotspotConfig{
		SSID:     req.SSID,
		Password: req.Password,
		Band:     req.Band,
		Channel:  req.Channel,
	}

	err := h.wifiMgr.CreateHotspot(config)
	resp := WiFiActionResponse{
		Success: err == nil,
		Message: "",
	}
	if err != nil {
		resp.Message = err.Error()
	} else {
		resp.Message = "Hotspot created: " + req.SSID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleWiFiHotspotStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := h.wifiMgr.StopHotspot()
	resp := WiFiActionResponse{
		Success: err == nil,
		Message: "",
	}
	if err != nil {
		resp.Message = err.Error()
	} else {
		resp.Message = "Hotspot stopped"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleWiFiForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SSID string `json:"ssid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.SSID == "" {
		http.Error(w, "SSID required", http.StatusBadRequest)
		return
	}

	err := h.wifiMgr.ForgetNetwork(req.SSID)
	resp := WiFiActionResponse{
		Success: err == nil,
		Message: "",
	}
	if err != nil {
		resp.Message = err.Error()
	} else {
		resp.Message = "Forgot network: " + req.SSID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
