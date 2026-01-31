package updates

import (
	"runtime"
)

const InstallerName = "srtla-installer"
const InstallerOwner = "KevinCowleys"
const InstallerRepo = "srtla-manager"

// InstallerChecker handles checking for srtla-installer updates
// Usage: checker := NewInstallerChecker(); checker.GetLatestRelease()
type InstallerChecker struct {
	checker *Checker
}

// NewInstallerChecker creates a new srtla-installer update checker
func NewInstallerChecker(currentVersion string) *InstallerChecker {
	return &InstallerChecker{
		checker: NewCheckerWithRepo(InstallerOwner, InstallerRepo, currentVersion),
	}
}

// GetLatestRelease fetches the latest srtla-installer release from GitHub
func (i *InstallerChecker) GetLatestRelease() (*Release, string, error) {
	release, err := i.checker.GetLatestRelease()
	if err != nil {
		return nil, "", err
	}
	osType := runtime.GOOS
	archType := runtime.GOARCH
	var assetPattern string
	switch osType {
	case "linux":
		switch archType {
		case "amd64":
			assetPattern = "installer-linux-amd64"
		case "arm64":
			assetPattern = "installer-linux-arm64"
		case "arm":
			assetPattern = "installer-linux-armv7"
		default:
			assetPattern = "installer-linux-" + archType
		}
	case "darwin":
		assetPattern = "installer-darwin-" + archType
	default:
		assetPattern = "installer-" + osType + "-" + archType
	}
	for _, asset := range release.Assets {
		if asset.Name == assetPattern {
			return release, asset.DownloadURL, nil
		}
	}
	return release, "", nil // No matching asset found
}
