package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"srtla-manager/internal/modem"
)

func (h *Handler) HandleModems(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/modems")
	path = strings.TrimPrefix(path, "/")

	if path != "" {
		parts := strings.Split(path, "/")
		if len(parts) == 2 && parts[1] == "ussd" {
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			var req struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			code := strings.TrimSpace(req.Code)
			if code == "" {
				http.Error(w, "USSD code required", http.StatusBadRequest)
				return
			}

			resp, err := h.modem.DialUSSD(parts[0], code)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":  true,
				"response": resp,
			})
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		info, err := h.modem.GetModem(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if info == nil {
			http.Error(w, "Modem not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	modems, err := h.modem.ListModems()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := ModemsResponse{
		Available: h.modem.IsAvailable(),
		Modems:    modems,
	}
	if resp.Modems == nil {
		resp.Modems = []modem.ModemInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleUSBNet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := h.GetUSBNetStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
