package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internal "srtla-manager/internal"
	"srtla-manager/internal/logger"
	"srtla-manager/internal/updates"
)

const (
	backupDir            = "/opt/srtla-manager/backups"
	binPath              = "/opt/srtla-manager/srtla-manager"
	srtlaSendDownloadDir = "/tmp/srtla-downloads"
)

// UpdateStatusResponse represents the current update status
type UpdateStatusResponse struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseURL     string `json:"release_url"`
	ReleaseNotes   string `json:"release_notes"`
	DownloadURL    string `json:"download_url"`
	ChecksumURL    string `json:"checksum_url"`
	IsPrerelease   bool   `json:"is_prerelease"`
}

// BackupInfo represents a backup version
type BackupInfo struct {
	Version   string `json:"version"`
	Timestamp int64  `json:"timestamp"`
	FilePath  string `json:"file_path"`
	Size      int64  `json:"size"`
}

// UpdateRequest is the payload for performing an update
type UpdateRequest struct {
	Version string `json:"version"`
}

// UpdateProgressResponse indicates update status
type UpdateProgressResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// HandleCheckUpdates checks for available updates (GET /api/updates/check)
func (h *Handler) HandleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	currentVersion := h.GetVersion()
	if currentVersion == "" {
		currentVersion = "v0.0.0-dev"
	}

	checker := updates.NewChecker(currentVersion)
	updateInfo, err := checker.CheckForUpdates()
	if err != nil {
		logger.Error("Failed to check for updates: %v", err)
		http.Error(w, fmt.Sprintf("Failed to check for updates: %v", err), http.StatusInternalServerError)
		return
	}

	if updateInfo == nil {
		logger.Error("No update information available")
		http.Error(w, "No update information available", http.StatusInternalServerError)
		return
	}

	resp := UpdateStatusResponse{
		Available:      updateInfo.Available,
		CurrentVersion: updateInfo.CurrentVersion,
		LatestVersion:  updateInfo.LatestVersion,
		ReleaseURL:     updateInfo.ReleaseURL,
		ReleaseNotes:   updateInfo.ReleaseNotes,
		DownloadURL:    updateInfo.DownloadURL,
		ChecksumURL:    updateInfo.DownloadURL + ".sha256",
		IsPrerelease:   updateInfo.IsPrerelease,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleGetReleases gets recent releases (GET /api/updates/releases)
func (h *Handler) HandleGetReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	currentVersion := h.GetVersion()
	if currentVersion == "" {
		currentVersion = "v0.0.0-dev"
	}

	checker := updates.NewChecker(currentVersion)
	releases, err := checker.GetAllReleases(20)
	if err != nil {
		logger.Error("Failed to fetch releases: %v", err)
		http.Error(w, fmt.Sprintf("Failed to fetch releases: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(releases)
}

// HandlePerformUpdate performs the actual update (POST /api/updates/perform)
func (h *Handler) HandlePerformUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Version == "" {
		http.Error(w, "Version is required", http.StatusBadRequest)
		return
	}

	// Set content type for streaming updates
	w.Header().Set("Content-Type", "application/json")

	// Perform update in background and stream progress
	go performUpdate(req.Version, h)

	json.NewEncoder(w).Encode(UpdateProgressResponse{
		Status:  "started",
		Message: fmt.Sprintf("Starting update to %s", req.Version),
	})
}

// HandleGetBackups gets available backup versions (GET /api/updates/backups)
func (h *Handler) HandleGetBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	backups, err := getBackupsList()
	if err != nil {
		logger.Error("Failed to get backups: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get backups: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backups)
}

// HandleRollback restores a previous version (POST /api/updates/rollback)
func (h *Handler) HandleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Timestamp int64 `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Timestamp == 0 {
		http.Error(w, "Timestamp is required", http.StatusBadRequest)
		return
	}

	backupFile := filepath.Join(backupDir, fmt.Sprintf("srtla-manager.%d.bak", req.Timestamp))
	if _, err := os.Stat(backupFile); err != nil {
		http.Error(w, "Backup not found", http.StatusNotFound)
		return
	}

	// Perform rollback
	if err := performRollback(backupFile); err != nil {
		logger.Error("Rollback failed: %v", err)
		http.Error(w, fmt.Sprintf("Rollback failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(UpdateProgressResponse{
		Status:  "success",
		Message: "Rollback completed successfully",
	})
}

// performUpdate downloads and installs the new version
func performUpdate(version string, h *Handler) {
	currentVersion := h.GetVersion()
	if currentVersion == "" {
		currentVersion = "v0.0.0-dev"
	}

	checker := updates.NewChecker(currentVersion)

	// Get release info
	releases, err := checker.GetAllReleases(100)
	if err != nil {
		return
	}

	// Find the target release
	var targetRelease *updates.Release
	for i := range releases {
		if releases[i].TagName == version {
			targetRelease = &releases[i]
			break
		}
	}

	if targetRelease == nil {
		return
	}

	// Create temp directory
	tempDir, err := os.MkdirTemp("", "srtla-update-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tempDir)

	tempBinary := filepath.Join(tempDir, "srtla-manager")
	tempChecksum := filepath.Join(tempDir, "srtla-manager.sha256")

	// Find download URLs
	downloadURL := ""
	checksumURL := ""
	for _, asset := range targetRelease.Assets {
		if asset.State == "uploaded" && !isChecksumFile(asset.Name) {
			downloadURL = asset.DownloadURL
		}
		if asset.State == "uploaded" && hasSuffix(asset.Name, ".sha256") {
			checksumURL = asset.DownloadURL
		}
	}

	if downloadURL == "" {
		return
	}

	// Download binary
	if err := downloadFile(downloadURL, tempBinary); err != nil {
		return
	}

	// Download and verify checksum if available
	if checksumURL != "" {
		if err := downloadFile(checksumURL, tempChecksum); err == nil {
			if err := verifyChecksum(tempBinary, tempChecksum); err != nil {
				return
			}
		}
	}

	// Create backup
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return
	}

	backupFile := filepath.Join(backupDir, fmt.Sprintf("srtla-manager.%d.bak", time.Now().Unix()))
	if err := copyFile(binPath, backupFile); err != nil {
		return
	}

	// Stop service
	exec.Command("sudo", "systemctl", "stop", "srtla-manager").Run()

	// Replace binary
	if err := copyFile(tempBinary, binPath); err != nil {
		// Restore from backup
		copyFile(backupFile, binPath)
		exec.Command("sudo", "systemctl", "start", "srtla-manager").Run()
		return
	}

	os.Chmod(binPath, 0755)

	// Start service
	exec.Command("sudo", "systemctl", "start", "srtla-manager").Run()

	// Wait a moment and verify
	time.Sleep(2 * time.Second)
	if err := exec.Command("sudo", "systemctl", "is-active", "--quiet", "srtla-manager").Run(); err != nil {
		// Service failed, rollback
		copyFile(backupFile, binPath)
		exec.Command("sudo", "systemctl", "start", "srtla-manager").Run()
	}
}

// performRollback restores a previous version
func performRollback(backupFile string) error {
	// Stop service
	exec.Command("sudo", "systemctl", "stop", "srtla-manager").Run()

	// Restore binary
	if err := copyFile(backupFile, binPath); err != nil {
		exec.Command("sudo", "systemctl", "start", "srtla-manager").Run()
		return err
	}

	os.Chmod(binPath, 0755)

	// Start service
	exec.Command("sudo", "systemctl", "start", "srtla-manager").Run()

	// Verify
	time.Sleep(2 * time.Second)
	if err := exec.Command("sudo", "systemctl", "is-active", "--quiet", "srtla-manager").Run(); err != nil {
		return fmt.Errorf("service failed to start after rollback")
	}

	return nil
}

// getBackupsList returns list of available backups
func getBackupsList() ([]BackupInfo, error) {
	var backups []BackupInfo

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return backups, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".bak") {
			info, _ := entry.Info()
			// Parse timestamp from filename: srtla-manager.TIMESTAMP.bak
			parts := strings.Split(entry.Name(), ".")
			if len(parts) >= 2 {
				var timestamp int64
				fmt.Sscanf(parts[1], "%d", &timestamp)
				backups = append(backups, BackupInfo{
					Version:   fmt.Sprintf("Backup from %s", time.Unix(timestamp, 0).Format("2006-01-02 15:04:05")),
					Timestamp: timestamp,
					FilePath:  filepath.Join(backupDir, entry.Name()),
					Size:      info.Size(),
				})
			}
		}
	}

	// Sort by timestamp descending (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp > backups[j].Timestamp
	})

	return backups, nil
}

// Helper functions

func downloadFile(url, filepath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func verifyChecksum(filePath, checksumFile string) error {
	checksumData, err := os.ReadFile(checksumFile)
	if err != nil {
		return err
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	calculatedHash := hex.EncodeToString(hash.Sum(nil))
	expectedHash := strings.Fields(string(checksumData))[0]

	if calculatedHash != expectedHash {
		return fmt.Errorf("checksum mismatch")
	}

	return nil
}

func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func isChecksumFile(name string) bool {
	return hasSuffix(name, ".sha256") || hasSuffix(name, ".sum") || hasSuffix(name, ".md5")
}

func hasSuffix(name, suffix string) bool {
	return len(name) >= len(suffix) && name[len(name)-len(suffix):] == suffix
}

// SRTLA Send Update Handlers

// HandleCheckSRTLASendUpdates checks for available srtla_send updates (GET /api/updates/srtla/check)
func (h *Handler) HandleCheckSRTLASendUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	checker := updates.NewSRTLASendChecker()
	updateInfo, err := checker.CheckForUpdates()
	if err != nil {
		logger.Error("Failed to check for srtla_send updates: %v", err)
		http.Error(w, fmt.Sprintf("Failed to check for srtla_send updates: %v", err), http.StatusInternalServerError)
		return
	}

	if updateInfo == nil {
		logger.Error("No srtla_send update information available")
		http.Error(w, "No update information available", http.StatusInternalServerError)
		return
	}

	resp := UpdateStatusResponse{
		Available:      updateInfo.Available,
		CurrentVersion: updateInfo.CurrentVersion,
		LatestVersion:  updateInfo.LatestVersion,
		ReleaseURL:     updateInfo.ReleaseURL,
		ReleaseNotes:   updateInfo.ReleaseNotes,
		DownloadURL:    updateInfo.DownloadURL,
		ChecksumURL:    updateInfo.DownloadURL + ".sha256",
		IsPrerelease:   updateInfo.IsPrerelease,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleGetSRTLASendReleases gets recent srtla_send releases (GET /api/updates/srtla/releases)
func (h *Handler) HandleGetSRTLASendReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	checker := updates.NewSRTLASendChecker()
	releases, err := checker.GetAllReleases(20)
	if err != nil {
		logger.Error("Failed to fetch srtla_send releases: %v", err)
		http.Error(w, fmt.Sprintf("Failed to fetch srtla_send releases: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(releases)
}

// HandleInstallSRTLASend downloads and installs srtla_send (POST /api/updates/srtla/install)
func (h *Handler) HandleInstallSRTLASend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Version == "" {
		http.Error(w, "Version is required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Perform installation in background
	go h.performSRTLASendInstall(req.Version)

	json.NewEncoder(w).Encode(UpdateProgressResponse{
		Status:  "started",
		Message: fmt.Sprintf("Downloading and installing srtla_send %s...", req.Version),
	})
}

// performSRTLASendInstall downloads and installs srtla_send
func (h *Handler) performSRTLASendInstall(version string) {
	h.broadcastSRTLAInstallProgress("info", fmt.Sprintf("Starting installation of srtla_send %s", version))

	checker := updates.NewSRTLASendChecker()

	// Get releases to find the target version
	h.broadcastSRTLAInstallProgress("info", "Fetching release information...")
	releases, err := checker.GetAllReleases(100)
	if err != nil {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("Failed to fetch releases: %v", err))
		return
	}

	var targetRelease *updates.Release
	for i := range releases {
		if releases[i].TagName == version {
			targetRelease = &releases[i]
			break
		}
	}

	if targetRelease == nil {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("Release %s not found", version))
		return
	}

	// Detect system architecture
	h.broadcastSRTLAInstallProgress("info", "Detecting system architecture...")
	arch := detectArchitecture()
	if arch == "" {
		h.broadcastSRTLAInstallProgress("error", "Failed to detect system architecture")
		return
	}
	h.broadcastSRTLAInstallProgress("info", fmt.Sprintf("Detected architecture: %s", arch))

	// Find the appropriate .deb file
	var debURL string
	for _, asset := range targetRelease.Assets {
		if strings.Contains(asset.Name, arch) && strings.HasSuffix(asset.Name, ".deb") {
			debURL = asset.DownloadURL
		}
	}

	if debURL == "" {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("No .deb package found for %s", arch))
		return
	}

	h.broadcastSRTLAInstallProgress("info", fmt.Sprintf("Found package: %s", debURL))

	// Create download directory
	if err := os.MkdirAll(srtlaSendDownloadDir, 0755); err != nil {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("Failed to create download directory: %v", err))
		return
	}

	// Download the .deb file
	debFile := filepath.Join(srtlaSendDownloadDir, fmt.Sprintf("srtla_%s_%s.deb", strings.TrimPrefix(version, "v"), arch))
	h.broadcastSRTLAInstallProgress("info", fmt.Sprintf("Downloading to %s...", debFile))
	if err := downloadFile(debURL, debFile); err != nil {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("Download failed: %v", err))
		return
	}
	h.broadcastSRTLAInstallProgress("success", "Download complete!")

	// Install the package
	h.broadcastSRTLAInstallProgress("info", "Installing package...")
	if err := h.installDebPackage(debFile); err != nil {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("Installation failed: %v", err))
		return
	}
	h.broadcastSRTLAInstallProgress("success", fmt.Sprintf("srtla_send %s installed successfully!", version))
}

// detectArchitecture detects the system architecture
func detectArchitecture() string {
	cmd := exec.Command("dpkg", "--print-architecture")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to uname
		cmd = exec.Command("uname", "-m")
		output, err = cmd.Output()
		if err != nil {
			return ""
		}
		arch := strings.TrimSpace(string(output))
		if arch == "x86_64" {
			return "amd64"
		} else if arch == "aarch64" {
			return "arm64"
		}
		return arch
	}

	return strings.TrimSpace(string(output))
}

// installDebPackage installs a .deb package
func (h *Handler) installDebPackage(debFile string) error {
	h.broadcastSRTLAInstallProgress("info", "Attempting to install .deb package via privileged backend...")

	resp, err := internal.InstallDebPackage(debFile)
	if err != nil {
		h.broadcastSRTLAInstallProgress("error", fmt.Sprintf("Privileged install error: %v", err))
		return fmt.Errorf("privileged install error: %w", err)
	}

	if resp.Success {
		h.broadcastSRTLAInstallProgress("success", "Package installed successfully!")
		if resp.Output != "" {
			h.broadcastSRTLAInstallProgress("info", resp.Output)
		}
		return nil
	}

	msg := resp.Error
	if msg == "" {
		msg = "Unknown error from privileged installer."
	}
	h.broadcastSRTLAInstallProgress("error", msg)
	return fmt.Errorf("privileged install failed: %s", msg)
}

// broadcastSRTLAInstallProgress broadcasts installation progress to connected clients
func (h *Handler) broadcastSRTLAInstallProgress(level, message string) {
	// Log to console/file
	switch level {
	case "error":
		logger.Error("[SRTLA_INSTALL] %s", message)
	case "success":
		logger.Info("[SRTLA_INSTALL] %s", message)
	default:
		logger.Printf("[SRTLA_INSTALL] %s", message)
	}

	// Broadcast via websocket
	if h.wsHub != nil {
		h.wsHub.Broadcast("srtla_install", map[string]string{
			"level":   level,
			"message": message,
		})
	}
}

// --- SRTLA Installer Update Logic ---
// Call updateSrtlaInstallerIfNeeded() as part of performUpdate
func updateSrtlaInstallerIfNeeded() {
	const installerPath = "/usr/local/bin/srtla-installer"
	currentVersion := internal.GetInstallerVersion(installerPath)
	checker := updates.NewInstallerChecker(currentVersion)
	release, downloadURL, err := checker.GetLatestRelease()
	if err != nil {
		logger.Error("Failed to check srtla-installer updates: %v", err)
		return
	}
	if release == nil || downloadURL == "" {
		logger.Info("No srtla-installer update available or asset missing.")
		return
	}
	if release.TagName == currentVersion {
		logger.Info("srtla-installer is up to date (%s)", currentVersion)
		return
	}
	logger.Info("Updating srtla-installer from %s to %s", currentVersion, release.TagName)

	// Download new binary
	tempDir, err := os.MkdirTemp("", "srtla-installer-update-")
	if err != nil {
		logger.Error("Failed to create temp dir: %v", err)
		return
	}
	defer os.RemoveAll(tempDir)
	tempBinary := filepath.Join(tempDir, "srtla-installer")
	if err := downloadFile(downloadURL, tempBinary); err != nil {
		logger.Error("Failed to download srtla-installer: %v", err)
		return
	}
	os.Chmod(tempBinary, 0755)

	// Stop service
	exec.Command("sudo", "systemctl", "stop", "srtla-installer").Run()

	// Replace binary (privileged)
	if err := copyFile(tempBinary, installerPath); err != nil {
		logger.Error("Failed to replace srtla-installer: %v", err)
		return
	}
	os.Chmod(installerPath, 0755)

	// Start service
	exec.Command("sudo", "systemctl", "start", "srtla-installer").Run()
	logger.Info("srtla-installer updated to %s", release.TagName)
}
