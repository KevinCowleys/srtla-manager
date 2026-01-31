package internal

import (
	"os/exec"
	"strings"
)

// GetInstallerVersion runs srtla-installer --version and returns the version string, or empty string on error.
func GetInstallerVersion(installerPath string) string {
	cmd := exec.Command(installerPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Expect output like: "srtla-installer version v0.1.2"
	parts := strings.Fields(string(output))
	for _, part := range parts {
		if strings.HasPrefix(part, "v") && len(part) > 1 {
			return part
		}
	}
	return strings.TrimSpace(string(output))
}
