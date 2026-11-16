// Package version is amptui's version: shown by --version and reported
// to the servers in the client headers.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is stamped in at build time by release builds and packagers:
//
//	go build -ldflags "-X github.com/tipii/amptui/internal/version.Version=0.4.0" ./cmd/amptui
//
// A leading "v" is fine. Left empty, String falls back to what the Go
// toolchain recorded.
var Version = ""

// String is the running build's version, without a leading "v": the
// stamped one, else the module version Go recorded (the tag for
// `go install …@v0.4.0`; a pseudo-version for a build in a git
// checkout), else "dev".
func String() string {
	if Version != "" {
		return strings.TrimPrefix(Version, "v")
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}
