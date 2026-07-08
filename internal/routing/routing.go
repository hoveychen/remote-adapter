// Package routing decides, per path, whether a filesystem or subprocess
// operation executes locally (on the brain host) or is forwarded to the remote
// executor sidecar.
//
// Two policies are supported (design doc §3.2 and §4.1.1):
//
//   - ModeRemoteAllowlist (default, POC-proven): everything runs LOCAL except
//     paths under an explicit remote-prefix allowlist. This is the surgical
//     routing the end-to-end POC validated — the claude CLI boots and reads its
//     own credentials/config locally, only work paths route remote.
//
//   - ModeLocalAllowlist (design target): everything runs REMOTE except paths
//     under an explicit local-prefix allowlist (e.g. ~/.claude, CLI internals).
//     This is the eventual "default remote" behaviour; it is more aggressive and
//     must be enabled deliberately.
package routing

import (
	"path/filepath"
	"strings"
)

// Mode selects the routing policy.
type Mode int

const (
	// ModeRemoteAllowlist routes local by default; only RemotePrefixes go remote.
	ModeRemoteAllowlist Mode = iota
	// ModeLocalAllowlist routes remote by default; only LocalPrefixes stay local.
	ModeLocalAllowlist
)

// Table is an immutable routing decision table. Construct with New.
type Table struct {
	mode           Mode
	remotePrefixes []string
	localPrefixes  []string
}

// New builds a routing table. Prefixes are cleaned to absolute-ish form; a
// trailing separator is normalised away so that "/work" matches "/work/a" but
// not "/workspace".
func New(mode Mode, remotePrefixes, localPrefixes []string) *Table {
	return &Table{
		mode:           mode,
		remotePrefixes: cleanPrefixes(remotePrefixes),
		localPrefixes:  cleanPrefixes(localPrefixes),
	}
}

func cleanPrefixes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// IsRemote reports whether an operation on path should be forwarded to the
// remote executor. The path is cleaned before matching.
func (t *Table) IsRemote(path string) bool {
	if path == "" {
		// Empty/unknown path: fall back to the mode default.
		return t.mode == ModeLocalAllowlist
	}
	clean := filepath.Clean(path)
	switch t.mode {
	case ModeLocalAllowlist:
		// Default remote; local only if it matches a local prefix.
		return !matchesAny(clean, t.localPrefixes)
	default: // ModeRemoteAllowlist
		// Default local; remote only if it matches a remote prefix.
		return matchesAny(clean, t.remotePrefixes)
	}
}

// matchesAny reports whether clean is equal to or nested under any prefix.
func matchesAny(clean string, prefixes []string) bool {
	for _, pre := range prefixes {
		if clean == pre {
			return true
		}
		if strings.HasPrefix(clean, pre+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// RemotePrefixes returns the configured remote prefixes (for handing to the
// native interceptor via environment).
func (t *Table) RemotePrefixes() []string { return append([]string(nil), t.remotePrefixes...) }

// LocalPrefixes returns the configured local prefixes.
func (t *Table) LocalPrefixes() []string { return append([]string(nil), t.localPrefixes...) }

// Mode returns the active policy.
func (t *Table) Mode() Mode { return t.mode }
