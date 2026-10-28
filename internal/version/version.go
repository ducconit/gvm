package version

import "runtime"

var (
	// Version is the semantic version of the g CLI. Overridden via -ldflags "-X github.com/ducconit/gvm/internal/version.Version=..."
	Version = "1.0.0"
	// Commit is the git commit hash when built.
	Commit = "dev"
	// BuildDate is the RFC3339 build timestamp.
	BuildDate = "unknown"
)

// Platform returns GOOS/GOARCH string.
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
