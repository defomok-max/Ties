// Package version exposes build metadata for the ties binary.
package version

// These values are overridable at build time via -ldflags.
var (
	// Version is the semantic version of the build.
	Version = "0.1.0-dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// Date is the build date.
	Date = "unknown"
)

// String returns a human readable version string.
func String() string {
	return "ties " + Version + " (" + Commit + ", " + Date + ")"
}
