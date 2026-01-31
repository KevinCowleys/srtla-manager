package updates

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	GitHubAPIBaseURL = "https://api.github.com"
	DefaultOwner     = "KevinCowleys"
	DefaultRepo      = "srtla-manager"
)

// Release represents a GitHub release
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Assets      []Asset   `json:"assets"`
	HTMLURL     string    `json:"html_url"`
}

// Asset represents a release asset (downloadable file)
type Asset struct {
	Name          string      `json:"name"`
	DownloadURL   string      `json:"browser_download_url"`
	Size          int         `json:"size"`
	DownloadCount int         `json:"download_count"`
	ContentType   string      `json:"content_type"`
	State         string      `json:"state"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
	ID            int         `json:"id"`
	NodeID        string      `json:"node_id"`
	URL           string      `json:"url"`
	Label         string      `json:"label"`
	Uploader      interface{} `json:"uploader"`
}

// UpdateInfo represents update information returned to the client
type UpdateInfo struct {
	Available      bool      `json:"available"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	ReleaseURL     string    `json:"release_url"`
	ReleaseNotes   string    `json:"release_notes"`
	PublishedAt    time.Time `json:"published_at"`
	DownloadURL    string    `json:"download_url"`
	IsPrerelease   bool      `json:"is_prerelease"`
	Changelog      string    `json:"changelog"`
}

// Checker handles checking for updates from GitHub
type Checker struct {
	owner          string
	repo           string
	currentVersion string
	httpClient     *http.Client
	cacheDir       string
	cacheTTL       time.Duration
}

// NewChecker creates a new GitHub update checker
func NewChecker(currentVersion string) *Checker {
	return NewCheckerWithRepo(DefaultOwner, DefaultRepo, currentVersion)
}

// NewCheckerWithRepo creates a new GitHub update checker with custom owner and repo
func NewCheckerWithRepo(owner, repo, currentVersion string) *Checker {
	return &Checker{
		owner:          owner,
		repo:           repo,
		currentVersion: currentVersion,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		cacheDir: "/tmp/srtla-manager-cache",
		cacheTTL: 10 * time.Minute,
	}
}

// CheckForUpdates checks GitHub for a newer release
func (c *Checker) CheckForUpdates() (*UpdateInfo, error) {
	release, err := c.GetLatestRelease()
	if err != nil {
		return nil, err
	}

	if release == nil {
		return &UpdateInfo{
			Available:      false,
			CurrentVersion: c.currentVersion,
		}, nil
	}

	// Check if the latest version is newer than current
	available := isNewerVersion(release.TagName, c.currentVersion)

	// Find the best download URL (prefer binary for common OS/arch combinations)
	downloadURL := c.findBestAsset(release)

	return &UpdateInfo{
		Available:      available,
		CurrentVersion: c.currentVersion,
		LatestVersion:  release.TagName,
		ReleaseURL:     release.HTMLURL,
		ReleaseNotes:   release.Body,
		PublishedAt:    release.PublishedAt,
		DownloadURL:    downloadURL,
		IsPrerelease:   release.Prerelease,
		Changelog:      release.Body,
	}, nil
}

// GetLatestRelease fetches the latest release from GitHub
func (c *Checker) GetLatestRelease() (*Release, error) {
	endpoint := "latest"

	// Try cache first
	if cached, ok := c.readCache(endpoint); ok {
		var release Release
		if err := json.Unmarshal(cached, &release); err == nil {
			return &release, nil
		}
	}

	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPIBaseURL, c.owner, c.repo)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	// Handle 404 (no releases found)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var release Release
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("failed to parse release JSON: %w", err)
	}

	// Cache the response
	c.writeCache(endpoint, body)

	return &release, nil
}

// GetAllReleases fetches all releases from GitHub
func (c *Checker) GetAllReleases(limit int) ([]Release, error) {
	endpoint := fmt.Sprintf("releases_%d", limit)

	// Try cache first
	if cached, ok := c.readCache(endpoint); ok {
		var releases []Release
		if err := json.Unmarshal(cached, &releases); err == nil {
			return releases, nil
		}
	}

	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d", GitHubAPIBaseURL, c.owner, c.repo, limit)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("failed to parse releases JSON: %w", err)
	}

	// Cache the response
	c.writeCache(endpoint, body)

	return releases, nil
}

// DownloadRelease downloads a release asset to a file
func (c *Checker) DownloadRelease(downloadURL, outputPath string) error {
	resp, err := c.httpClient.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	file, err := createFileWithParentDirs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// findBestAsset finds the most appropriate asset for the current platform
func (c *Checker) findBestAsset(release *Release) string {
	if len(release.Assets) == 0 {
		return "" // No assets available
	}

	// Return the first asset by default
	// In the future, this could be enhanced to match the current OS/arch
	for _, asset := range release.Assets {
		if !strings.HasSuffix(asset.Name, ".sha256") && !strings.HasSuffix(asset.Name, ".sum") {
			return asset.DownloadURL
		}
	}

	return release.Assets[0].DownloadURL
}

// isNewerVersion compares two version strings using semantic versioning
// Strips 'v' prefix and compares major.minor.patch versions numerically
func isNewerVersion(latest, current string) bool {
	latestClean := strings.TrimPrefix(latest, "v")
	currentClean := strings.TrimPrefix(current, "v")

	latestParts := parseVersion(latestClean)
	currentParts := parseVersion(currentClean)

	// Compare major version
	if latestParts[0] != currentParts[0] {
		return latestParts[0] > currentParts[0]
	}
	// Compare minor version
	if latestParts[1] != currentParts[1] {
		return latestParts[1] > currentParts[1]
	}
	// Compare patch version
	return latestParts[2] > currentParts[2]
}

// parseVersion parses a semantic version string into [major, minor, patch]
func parseVersion(version string) [3]int {
	parts := [3]int{0, 0, 0}

	// Remove any pre-release or build metadata
	if idx := strings.IndexAny(version, "-+"); idx != -1 {
		version = version[:idx]
	}

	versionParts := strings.Split(version, ".")
	for i := 0; i < len(versionParts) && i < 3; i++ {
		// Extract only the numeric part
		re := regexp.MustCompile(`^(\d+)`)
		matches := re.FindStringSubmatch(versionParts[i])
		if len(matches) > 1 {
			if num, err := strconv.Atoi(matches[1]); err == nil {
				parts[i] = num
			}
		}
	}

	return parts
}

// createFileWithParentDirs creates a file and its parent directories
func createFileWithParentDirs(path string) (io.WriteCloser, error) {
	// Create parent directories if they don't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	// Create the file
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	return file, nil
}

// getCacheFilePath returns the cache file path for a specific endpoint
func (c *Checker) getCacheFilePath(endpoint string) string {
	// Create a safe filename from owner/repo/endpoint
	safeName := strings.ReplaceAll(fmt.Sprintf("%s_%s_%s", c.owner, c.repo, endpoint), "/", "_")
	return filepath.Join(c.cacheDir, safeName+".json")
}

// readCache reads cached data if it's still valid
func (c *Checker) readCache(endpoint string) ([]byte, bool) {
	cachePath := c.getCacheFilePath(endpoint)

	info, err := os.Stat(cachePath)
	if err != nil {
		return nil, false
	}

	// Check if cache is still valid
	if time.Since(info.ModTime()) > c.cacheTTL {
		return nil, false
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false
	}

	return data, true
}

// writeCache writes data to cache
func (c *Checker) writeCache(endpoint string, data []byte) error {
	if err := os.MkdirAll(c.cacheDir, 0755); err != nil {
		return err
	}

	cachePath := c.getCacheFilePath(endpoint)
	return os.WriteFile(cachePath, data, 0644)
}
