// Package version carries the build identity set by -ldflags -X.
package version

import "runtime"

// Build identity; the Makefile sets these from git.
var (
	Version   = "dev"
	Commit    = "none"
	Date      = "unknown"
	Branch    = "unknown"
	BuildUser = "unknown"
)

// GoVersion is runtime.Version().
func GoVersion() string { return runtime.Version() }
