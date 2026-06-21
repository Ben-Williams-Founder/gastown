package polecat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func okLookPath(string) (string, error)   { return "/usr/bin/systemd-run", nil }
func missLookPath(string) (string, error) { return "", errors.New("not found") }

func TestWrapInSlice_InertWhenUnset(t *testing.T) {
	cmd := "exec env A=1 claude --flag \"hi\""
	if got := wrapInSliceWith(cmd, "", "linux", okLookPath); got != cmd {
		t.Fatalf("unset env must be byte-identical no-op; got %q", got)
	}
	if got := wrapInSliceWith(cmd, "   ", "linux", okLookPath); got != cmd {
		t.Fatalf("blank env must be a no-op; got %q", got)
	}
}

func TestWrapInSlice_FailOpenNonLinux(t *testing.T) {
	cmd := "exec env A=1 claude"
	if got := wrapInSliceWith(cmd, "polecat.slice", "darwin", okLookPath); got != cmd {
		t.Fatalf("non-linux must fail open; got %q", got)
	}
}

func TestWrapInSlice_FailOpenNoSystemdRun(t *testing.T) {
	cmd := "exec env A=1 claude"
	if got := wrapInSliceWith(cmd, "polecat.slice", "linux", missLookPath); got != cmd {
		t.Fatalf("absent systemd-run must fail open; got %q", got)
	}
}

func TestWrapInSlice_WrapsUnderSlice(t *testing.T) {
	cmd := "exec env A=1 claude --flag x"
	got := wrapInSliceWith(cmd, "polecat.slice", "linux", okLookPath)
	for _, want := range []string{
		"systemd-run --user --scope --slice=polecat.slice",
		"--quiet --collect",
		"/bin/sh -c ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped command missing %q; got %q", want, got)
		}
	}
	// The original line must be preserved verbatim inside the single-quoted payload.
	if !strings.Contains(got, "'"+cmd+"'") {
		t.Fatalf("original command not preserved verbatim; got %q", got)
	}
}

// TestWrapInSlice_FailOpenOnPlacementRuntimeFailure pins the wkb-h468 dispatch
// regression fix: the wrapped line must NOT begin with `exec` (which would replace
// the pane shell with systemd-run and kill the polecat when systemd-run exits 1),
// and must include a `|| exec /bin/sh -c` fallback so a placement failure (e.g. no
// $XDG_RUNTIME_DIR / user bus in the daemon-spawned polecat pane) still runs the
// polecat UNPLACED instead of leaving the pane dead → spawn fail → dispatch outage.
func TestWrapInSlice_FailOpenOnPlacementRuntimeFailure(t *testing.T) {
	cmd := "exec env A=1 claude --flag x"
	got := wrapInSliceWith(cmd, "polecat.slice", "linux", okLookPath)
	if strings.HasPrefix(got, "exec systemd-run") {
		t.Fatalf("wrapped line must not `exec` systemd-run (no fallback possible); got %q", got)
	}
	if !strings.Contains(got, "|| exec /bin/sh -c") {
		t.Fatalf("wrapped line must fall through to run the polecat unplaced on placement failure; got %q", got)
	}
	// The original line must appear in BOTH the placed and the fallback branch.
	if c := strings.Count(got, "'"+cmd+"'"); c != 2 {
		t.Fatalf("original command must appear in both placement and fallback branch (want 2, got %d): %q", c, got)
	}
}

func TestWrapInSlice_EscapesSingleQuotes(t *testing.T) {
	cmd := "claude --prompt 'be brief'"
	got := wrapInSliceWith(cmd, "polecat.slice", "linux", okLookPath)
	// Each embedded ' becomes '\'' so the outer single-quoting stays balanced.
	if !strings.Contains(got, `'\''`) {
		t.Fatalf("embedded single quotes must be escaped; got %q", got)
	}
}

// --- resolveSlice: the env→config→off precedence + kill-switch (wkb-h468) ---

// cfgFunc builds an injectable config lookup returning a fixed slice for any townRoot.
func cfgFunc(slice string) func(string) string {
	return func(string) string { return slice }
}

// sentinelFunc builds an injectable sentinel check returning a fixed presence.
func sentinelFunc(present bool) func(string) bool {
	return func(string) bool { return present }
}

func TestResolveSlice(t *testing.T) {
	const town = "/town"
	cases := []struct {
		name       string
		env        string
		config     string
		sentinel   bool
		wantSlice  string
		wantReason string
	}{
		{
			name:       "env set uses env slice",
			env:        "polecat.slice",
			config:     "",
			wantSlice:  "polecat.slice",
			wantReason: "env override active",
		},
		{
			name:       "env unset + config set uses config slice (manual dispatch fix)",
			env:        "",
			config:     "polecat.slice",
			wantSlice:  "polecat.slice",
			wantReason: "config fallback makes ambient-env-free dispatch place",
		},
		{
			name:       "both unset => no placement (deploy-neutral, unchanged)",
			env:        "",
			config:     "",
			wantSlice:  "",
			wantReason: "byte-identical to today when nothing is configured",
		},
		{
			name:       "env overrides config (precedence)",
			env:        "fromenv.slice",
			config:     "fromconfig.slice",
			wantSlice:  "fromenv.slice",
			wantReason: "env is the per-invocation override and wins",
		},
		{
			name:       "kill-switch sentinel disables even when configured",
			env:        "",
			config:     "polecat.slice",
			sentinel:   true,
			wantSlice:  "",
			wantReason: "NO_POLECAT_PLACEMENT sentinel force-disables placement",
		},
		{
			name:       "kill-switch sentinel disables even when env set",
			env:        "polecat.slice",
			config:     "",
			sentinel:   true,
			wantSlice:  "",
			wantReason: "sentinel wins over env override too",
		},
		{
			name:       "config value 'off' disables placement",
			env:        "",
			config:     "off",
			wantSlice:  "",
			wantReason: "config kill-switch value",
		},
		{
			name:       "config value 'none' disables placement",
			env:        "",
			config:     "NONE",
			wantSlice:  "",
			wantReason: "config kill-switch value (case-insensitive)",
		},
		{
			name:       "env value 'off' disables placement (overriding config)",
			env:        "off",
			config:     "polecat.slice",
			wantSlice:  "",
			wantReason: "explicit env kill-switch beats config",
		},
		{
			name:       "whitespace-only config is treated as unset",
			env:        "",
			config:     "   ",
			wantSlice:  "",
			wantReason: "trimmed empty == no placement",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSlice(tc.env, town, cfgFunc(tc.config), sentinelFunc(tc.sentinel))
			if got != tc.wantSlice {
				t.Fatalf("%s: resolveSlice = %q, want %q", tc.wantReason, got, tc.wantSlice)
			}
		})
	}
}

// End-to-end through wrapInSliceWith: config-resolved slice wraps just like env did.
func TestResolveSlice_ThenWrap_ConfigPathPlaces(t *testing.T) {
	slice := resolveSlice("", "/town", cfgFunc("polecat.slice"), sentinelFunc(false))
	got := wrapInSliceWith("exec claude", slice, "linux", okLookPath)
	if !strings.Contains(got, "--slice=polecat.slice") {
		t.Fatalf("config-resolved slice should place; got %q", got)
	}
}

func TestResolveSlice_ThenWrap_KillSwitchLeavesUnchanged(t *testing.T) {
	cmd := "exec claude"
	slice := resolveSlice("polecat.slice", "/town", cfgFunc("polecat.slice"), sentinelFunc(true))
	if got := wrapInSliceWith(cmd, slice, "linux", okLookPath); got != cmd {
		t.Fatalf("kill-switch must leave command unchanged; got %q", got)
	}
}

// --- the real injected helpers against a temp town root (no daemon, no host) ---

func TestSentinelExists(t *testing.T) {
	dir := t.TempDir()
	if sentinelExists(dir) {
		t.Fatal("no sentinel file => sentinelExists must be false")
	}
	if err := os.WriteFile(filepath.Join(dir, NoPlacementSentinel), []byte{}, 0o644); err != nil {
		t.Fatalf("writing sentinel: %v", err)
	}
	if !sentinelExists(dir) {
		t.Fatal("sentinel file present => sentinelExists must be true")
	}
	if sentinelExists("") {
		t.Fatal("empty townRoot => sentinelExists must be false")
	}
}

func TestConfiguredPolecatSlice(t *testing.T) {
	dir := t.TempDir()
	// No settings file yet => empty (inert), never errors.
	if got := configuredPolecatSlice(dir); got != "" {
		t.Fatalf("missing settings => empty slice; got %q", got)
	}
	if got := configuredPolecatSlice(""); got != "" {
		t.Fatalf("empty townRoot => empty slice; got %q", got)
	}
	// Write a settings file with polecat.slice set; configuredPolecatSlice must read it.
	settingsDir := filepath.Join(dir, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir settings: %v", err)
	}
	json := `{"type":"town-settings","version":1,"polecat":{"slice":"polecat.slice"}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "config.json"), []byte(json), 0o644); err != nil {
		t.Fatalf("writing config.json: %v", err)
	}
	if got := configuredPolecatSlice(dir); got != "polecat.slice" {
		t.Fatalf("configured slice should be read from settings; got %q", got)
	}
}
