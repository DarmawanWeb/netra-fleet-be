//go:build !unix

package licensemgr

// lockDir does not lock on systems without flock; run one manager per data folder.
func lockDir(string) (func(), error) { return func() {}, nil }
