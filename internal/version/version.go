package version

import (
	"fmt"
	"runtime"
)

// Build information injected at compile time via ldflags
var (
	// Version is the semantic version of the application
	Version = "v0.0.0-dev"

	// Commit is the git commit hash
	Commit = "unknown"

	// Branch is the git branch
	Branch = "unknown"

	// BuildTime is the timestamp when the binary was built
	BuildTime = "unknown"

	// Builder is the user/system that built the binary
	Builder = "unknown"
)

// Info returns a formatted string with version information
func Info() string {
	return fmt.Sprintf("srtla-manager %s (commit: %s, branch: %s)", Version, Commit, Branch)
}

// DetailedInfo returns detailed version information
func DetailedInfo() string {
	return fmt.Sprintf(
		"srtla-manager %s\n"+
			"  Commit: %s\n"+
			"  Branch: %s\n"+
			"  Built: %s\n"+
			"  Builder: %s\n"+
			"  Go: %s\n"+
			"  OS: %s\n"+
			"  Arch: %s",
		Version,
		Commit,
		Branch,
		BuildTime,
		Builder,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
}

// GetVersion returns just the version string
func GetVersion() string {
	return Version
}

// IsPrerelease returns true if this is a pre-release version (contains -dev, -alpha, -beta, etc)
func IsPrerelease() bool {
	// Consider -dev builds as dev versions
	// Consider anything with - as pre-release
	for _, char := range Version {
		if char == '-' {
			return true
		}
	}
	return false
}

// BuildInfo holds all build-related information
type BuildInfo struct {
	Version   string
	Commit    string
	Branch    string
	BuildTime string
	Builder   string
	GoVersion string
	OS        string
	Arch      string
}

// GetBuildInfo returns a structured BuildInfo
func GetBuildInfo() BuildInfo {
	return BuildInfo{
		Version:   Version,
		Commit:    Commit,
		Branch:    Branch,
		BuildTime: BuildTime,
		Builder:   Builder,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
