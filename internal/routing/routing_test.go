package routing

import "testing"

func TestRemoteAllowlist(t *testing.T) {
	tbl := New(ModeRemoteAllowlist,
		[]string{"/work", "/tmp/rcc/vfs"}, nil)
	cases := map[string]bool{
		"/work":              true,  // exact prefix
		"/work/proj/main.go": true,  // nested
		"/tmp/rcc/vfs/a.txt": true,  // second prefix
		"/workspace/other":   false, // must not match "/work" by string prefix
		"/home/user/.claude": false, // default local
		"/usr/lib/dyld":      false, // system path stays local
	}
	for path, want := range cases {
		if got := tbl.IsRemote(path); got != want {
			t.Errorf("IsRemote(%q)=%v want %v", path, got, want)
		}
	}
}

func TestLocalAllowlist(t *testing.T) {
	tbl := New(ModeLocalAllowlist,
		nil, []string{"/home/user/.claude", "/usr/lib"})
	cases := map[string]bool{
		"/home/user/.claude/CLAUDE.md": false, // local whitelist
		"/usr/lib/dyld":                false, // local whitelist
		"/work/proj/main.go":           true,  // default remote
		"/tmp/scratch":                 true,  // default remote
		"/home/user/.claudex":          true,  // sibling must not match ".claude"
	}
	for path, want := range cases {
		if got := tbl.IsRemote(path); got != want {
			t.Errorf("IsRemote(%q)=%v want %v", path, got, want)
		}
	}
}

func TestEmptyPathUsesModeDefault(t *testing.T) {
	if New(ModeRemoteAllowlist, nil, nil).IsRemote("") {
		t.Error("empty path should be local under ModeRemoteAllowlist")
	}
	if !New(ModeLocalAllowlist, nil, nil).IsRemote("") {
		t.Error("empty path should be remote under ModeLocalAllowlist")
	}
}

func TestPrefixNormalisation(t *testing.T) {
	tbl := New(ModeRemoteAllowlist, []string{"/work/"}, nil)
	if !tbl.IsRemote("/work/a") {
		t.Error("trailing-slash prefix should still match nested path")
	}
	if tbl.IsRemote("/work") != true {
		t.Error("cleaned prefix should match exact path")
	}
}
