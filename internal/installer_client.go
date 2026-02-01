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

// UpdateBinaryRequest requests the privileged installer to replace the srtla-manager binary
type UpdateBinaryRequest struct {
	Token       string `json:"token"`
	SourcePath  string `json:"source_path"`
	TargetPath  string `json:"target_path"`
	ServiceName string `json:"service_name"`
	BackupPath  string `json:"backup_path"`
}

// UpdateBinaryResponse indicates success/failure of binary replacement
type UpdateBinaryResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// UpdateInstallerRequest requests srtla-installer to update itself
type UpdateInstallerRequest struct {
	Token      string `json:"token"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
}

// UpdateInstallerResponse indicates success/failure of installer self-update
type UpdateInstallerResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
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

// UpdateBinaryWithInstaller contacts the privileged installer daemon to replace the srtla-manager binary
func UpdateBinaryWithInstaller(sourcePath, targetPath, serviceName, backupPath string) (UpdateBinaryResponse, error) {
	conn, err := net.Dial("unix", installerSocket)
	if err != nil {
		return UpdateBinaryResponse{}, fmt.Errorf("connect to installer: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if err := enc.Encode(UpdateBinaryRequest{
		Token:       "",
		SourcePath:  sourcePath,
		TargetPath:  targetPath,
		ServiceName: serviceName,
		BackupPath:  backupPath,
	}); err != nil {
		return UpdateBinaryResponse{}, fmt.Errorf("encode: %w", err)
	}

	var resp UpdateBinaryResponse
	if err := dec.Decode(&resp); err != nil {
		return UpdateBinaryResponse{}, fmt.Errorf("decode: %w", err)
	}
	return resp, nil
}

// UpdateInstallerSelf requests the srtla-installer daemon to update itself
func UpdateInstallerSelf(sourcePath, targetPath string) (UpdateInstallerResponse, error) {
	conn, err := net.Dial("unix", installerSocket)
	if err != nil {
		return UpdateInstallerResponse{}, fmt.Errorf("connect to installer: %w", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if err := enc.Encode(UpdateInstallerRequest{
		Token:      "",
		SourcePath: sourcePath,
		TargetPath: targetPath,
	}); err != nil {
		return UpdateInstallerResponse{}, fmt.Errorf("encode: %w", err)
	}

	var resp UpdateInstallerResponse
	if err := dec.Decode(&resp); err != nil {
		return UpdateInstallerResponse{}, fmt.Errorf("decode: %w", err)
	}
	return resp, nil
}
