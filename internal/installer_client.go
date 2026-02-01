package internal

import (
	"encoding/json"
	"fmt"
	"net"
)

const installerSocket = "/run/srtla-installer.sock"

type InstallRequest struct {
	Token   string `json:"token"`
	DebPath string `json:"deb_path"`
}

type InstallResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// InstallDebPackage contacts the privileged installer daemon to install a .deb file
func InstallDebPackage(debPath string) (InstallResponse, error) {
	conn, err := net.Dial("unix", installerSocket)
	if err != nil {
		return InstallResponse{}, fmt.Errorf("connect to installer: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if err := enc.Encode(InstallRequest{Token: "", DebPath: debPath}); err != nil {
		return InstallResponse{}, fmt.Errorf("encode: %w", err)
	}

	var resp InstallResponse
	if err := dec.Decode(&resp); err != nil {
		return InstallResponse{}, fmt.Errorf("decode: %w", err)
	}
	return resp, nil
}
