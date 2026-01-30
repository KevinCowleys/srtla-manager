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

	"srtla-manager/internal/updates"
)

const (
	backupDir = "/opt/srtla-manager/backups"
	binPath   = "/opt/srtla-manager/srtla-manager"
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
		http.Error(w, fmt.Sprintf("Failed to check for updates: %v", err), http.StatusInternalServerError)
		return
	}

	if updateInfo == nil {
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
