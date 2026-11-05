// Package version is the build's version, set by the release build:
//
//	go build -ldflags "-X github.com/DarmawanWeb/netra-fleet-be/internal/version.Version=v1.2.0 \
//	  -X github.com/DarmawanWeb/netra-fleet-be/internal/version.Commit=abc1234"
package version

// Version and Commit of this build; "dev" for a development build.
var (
	Version = "dev"
	Commit  = ""
)

// String is the version with its commit, e.g. "v1.2.0 (abc1234)".
func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
