package updates

import (
	"os/exec"
	"strings"
)

const (
	SRTLASendOwner = "irlserver"
	SRTLASendRepo  = "srtla_send"
)

// SRTLASendChecker handles checking for srtla_send updates
type SRTLASendChecker struct {
	checker *Checker
}

// NewSRTLASendChecker creates a new srtla_send update checker
func NewSRTLASendChecker() *SRTLASendChecker {
	currentVersion := getCurrentSRTLASendVersion()
	checker := NewCheckerWithRepo(SRTLASendOwner, SRTLASendRepo, currentVersion)

	return &SRTLASendChecker{
		checker: checker,
	}
}

// CheckForUpdates checks GitHub for a newer srtla_send release
func (s *SRTLASendChecker) CheckForUpdates() (*UpdateInfo, error) {
	return s.checker.CheckForUpdates()
}

// GetLatestRelease fetches the latest srtla_send release from GitHub
func (s *SRTLASendChecker) GetLatestRelease() (*Release, error) {
	return s.checker.GetLatestRelease()
}

// GetAllReleases fetches all srtla_send releases from GitHub
func (s *SRTLASendChecker) GetAllReleases(limit int) ([]Release, error) {
	return s.checker.GetAllReleases(limit)
}

// GetCurrentVersion returns the currently installed srtla_send version
func (s *SRTLASendChecker) GetCurrentVersion() string {
	return s.checker.currentVersion
}

// getCurrentSRTLASendVersion attempts to get the installed srtla_send version
func getCurrentSRTLASendVersion() string {
	// Try to get version from srtla_send binary
	cmd := exec.Command("srtla_send", "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If command fails, try alternative locations
		for _, path := range []string{"/usr/local/bin/srtla_send", "/usr/bin/srtla_send", "./srtla_send"} {
			cmd = exec.Command(path, "--version")
			if output, err = cmd.CombinedOutput(); err == nil {
				break
			}
		}

		// If still no version found, return unknown
		if err != nil {
			return "v0.0.0-unknown"
		}
	}

	// Parse version from output
	// Expected format: "srtla_send version v1.2.3" or similar
	outputStr := strings.TrimSpace(string(output))
	parts := strings.Fields(outputStr)

	// Look for version string
	for _, part := range parts {
		if strings.HasPrefix(part, "v") || strings.HasPrefix(part, "V") {
			return part
		}
	}

	// If version not found in expected format, try to extract any version-like string
	for i, part := range parts {
		if (part == "version" || part == "Version") && i+1 < len(parts) {
			return parts[i+1]
		}
	}

	// Fallback: return the entire output if it looks like a version
	if len(parts) > 0 && (strings.Contains(outputStr, ".") || strings.HasPrefix(outputStr, "v")) {
		return strings.TrimSpace(outputStr)
	}

	return "v0.0.0-unknown"
}
