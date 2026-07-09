package main

// Embedded native interceptor artifacts. `make native` copies the platform's
// artifact into embedded/ before the Go build (see embedded/README.md); at
// runtime the artifact is extracted to the user cache dir, keyed by content
// hash so upgrades never reuse a stale copy and concurrent rca runs share one.

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed all:embedded
var embeddedFS embed.FS

// nativeArtifactName is the embedded/ file run mode injects on this platform;
// empty means the platform has no native interceptor.
func nativeArtifactName(goos string) string {
	switch goos {
	case "darwin":
		return "rcc_interpose.dylib"
	case "linux":
		return "rcc_seccomp"
	}
	return ""
}

// extractEmbeddedNative materializes the named embedded artifact under the user
// cache dir and returns its path. Returns ("", nil) when the artifact was not
// embedded (plain `go build` without `make native`) — the caller then requires
// an explicit --dylib/--supervisor.
func extractEmbeddedNative(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	data, err := embeddedFS.ReadFile("embedded/" + name)
	if err != nil {
		return "", nil // not embedded in this build
	}
	sum := sha256.Sum256(data)
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "rca", "native", hex.EncodeToString(sum[:6]))
	dest := filepath.Join(dir, name)
	if st, err := os.Stat(dest); err == nil && st.Size() == int64(len(data)) {
		return dest, nil // already extracted (content-hashed dir => same bytes)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("rca: extract %s: %w", name, err)
	}
	// Write to a temp name and rename so a concurrent rca never sees a torn file.
	tmp, err := os.CreateTemp(dir, name+".tmp*")
	if err != nil {
		return "", fmt.Errorf("rca: extract %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rca: extract %s: %w", name, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rca: extract %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rca: extract %s: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rca: extract %s: %w", name, err)
	}
	return dest, nil
}
