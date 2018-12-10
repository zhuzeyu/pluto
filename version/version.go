package version

import (
	"fmt"
)

// Major version component of the current release
const Major = 1

// Minor version component of the current release
const Minor = 0

// Fix version component of the current release
const Fix = 0

var (
	// Version is the full version string
	Version = fmt.Sprintf("%d.%d.%d", Major, Minor, Fix)

	// GitCommit is set with --ldflags "-X main.gitCommit=$(git rev-parse --short HEAD)"
	GitCommit string
)

func init() {
	if GitCommit != "" {
		Version += "-" + GitCommit
	}
}
