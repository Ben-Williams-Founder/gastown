package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAdaptiveRole(t *testing.T) {
	tests := []struct {
		name                       string
		flag, envTownHost, envRole string
		wantRole, wantSource       string
		wantExplicit               bool
	}{
		{
			name:         "no role anywhere → not explicit",
			wantExplicit: false,
		},
		{
			name:         "flag town-host wins",
			flag:         "town-host",
			wantRole:     "town-host",
			wantSource:   "--role flag",
			wantExplicit: true,
		},
		{
			name:         "flag beats env",
			flag:         "sandbox",
			envTownHost:  "town-host",
			wantRole:     "sandbox",
			wantSource:   "--role flag",
			wantExplicit: true,
		},
		{
			name:         "GT_TOWN_HOST_ROLE used when no flag",
			envTownHost:  "town-host",
			wantRole:     "town-host",
			wantSource:   "GT_TOWN_HOST_ROLE env",
			wantExplicit: true,
		},
		{
			name:         "GT_ROLE lowest precedence",
			envRole:      "dev",
			wantRole:     "dev",
			wantSource:   "GT_ROLE env",
			wantExplicit: true,
		},
		{
			name:         "unrecognized flag value → not explicit (fail-safe)",
			flag:         "production",
			wantExplicit: false,
		},
		{
			name:         "unrecognized flag falls through to valid env",
			flag:         "bogus",
			envTownHost:  "town-host",
			wantRole:     "town-host",
			wantSource:   "GT_TOWN_HOST_ROLE env",
			wantExplicit: true,
		},
		{
			name:         "whitespace is trimmed",
			flag:         "  town-host  ",
			wantRole:     "town-host",
			wantSource:   "--role flag",
			wantExplicit: true,
		},
		{
			name:         "GT_ROLE=polecat (non-adaptive role) ignored",
			envRole:      "polecat",
			wantExplicit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, source, explicit := resolveAdaptiveRole(tt.flag, tt.envTownHost, tt.envRole)
			if explicit != tt.wantExplicit {
				t.Fatalf("explicit = %v, want %v", explicit, tt.wantExplicit)
			}
			if role != tt.wantRole {
				t.Errorf("role = %q, want %q", role, tt.wantRole)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}

func TestDecideAdaptiveBoot(t *testing.T) {
	t.Run("missing script → skip, fail-open", func(t *testing.T) {
		d := decideAdaptiveBoot(false, "town-host", "--role flag", true, true)
		if d.run {
			t.Error("run should be false when script is missing")
		}
		if d.note == "" {
			t.Error("expected a skip note")
		}
	})

	t.Run("town-host interactive → apply without --yes", func(t *testing.T) {
		d := decideAdaptiveBoot(true, "town-host", "--role flag", true, true)
		if !d.run || d.mode != "apply" {
			t.Fatalf("got run=%v mode=%q, want run=true mode=apply", d.run, d.mode)
		}
		joined := strings.Join(d.args, " ")
		// Apply mode = mode=apply default (no --print). There is no --apply flag.
		if !strings.Contains(joined, "--role town-host") || strings.Contains(joined, "--print") {
			t.Errorf("args = %v, want --role town-host in apply mode (no --print)", d.args)
		}
		if strings.Contains(joined, "--yes") {
			t.Errorf("interactive apply should not pass --yes; args = %v", d.args)
		}
	})

	t.Run("town-host non-interactive → apply with --yes", func(t *testing.T) {
		d := decideAdaptiveBoot(true, "town-host", "GT_TOWN_HOST_ROLE env", true, false)
		joined := strings.Join(d.args, " ")
		if d.mode != "apply" {
			t.Fatalf("mode = %q, want apply", d.mode)
		}
		if !strings.Contains(joined, "--yes") {
			t.Errorf("non-interactive apply must pass --yes; args = %v", d.args)
		}
	})

	t.Run("explicit sandbox → print-only, never apply", func(t *testing.T) {
		d := decideAdaptiveBoot(true, "sandbox", "--role flag", true, false)
		joined := strings.Join(d.args, " ")
		if d.mode != "print" {
			t.Fatalf("mode = %q, want print", d.mode)
		}
		if strings.Contains(joined, "--apply") {
			t.Errorf("sandbox must never apply; args = %v", d.args)
		}
		if !strings.Contains(joined, "--print") {
			t.Errorf("expected --print; args = %v", d.args)
		}
	})

	t.Run("no role → print-only, preserves historical behavior", func(t *testing.T) {
		d := decideAdaptiveBoot(true, "", "", false, false)
		joined := strings.Join(d.args, " ")
		if d.mode != "print" {
			t.Fatalf("mode = %q, want print", d.mode)
		}
		if strings.Contains(joined, "--apply") {
			t.Errorf("no-role path must never apply; args = %v", d.args)
		}
		if !strings.Contains(joined, "--print") {
			t.Errorf("no-role path must use --print; args = %v", d.args)
		}
		if !strings.Contains(d.note, "--role") {
			t.Errorf("no-role note should instruct re-running with --role; got %q", d.note)
		}
	})
}

func TestAdaptiveScriptPath_HonorsGovTools(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "tools", "ops")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptFile := filepath.Join(scriptDir, "adaptive-boot.sh")
	if err := os.WriteFile(scriptFile, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOV_TOOLS", dir)

	path, exists := adaptiveScriptPath()
	if !exists {
		t.Fatalf("expected script to exist at %s", path)
	}
	if path != scriptFile {
		t.Errorf("path = %q, want %q", path, scriptFile)
	}
}

func TestAdaptiveScriptPath_MissingIsFailOpen(t *testing.T) {
	t.Setenv("GOV_TOOLS", t.TempDir()) // empty dir, no script
	path, exists := adaptiveScriptPath()
	if exists {
		t.Errorf("expected script to be absent, but path %q reported existing", path)
	}
}

// TestRunAdaptiveBoot_MissingScriptIsSilentNoFail verifies the top-level helper
// never panics or writes apply notes when the script is absent (fail-open).
func TestRunAdaptiveBoot_MissingScriptIsSilentNoFail(t *testing.T) {
	t.Setenv("GOV_TOOLS", t.TempDir())
	t.Setenv("GT_TOWN_HOST_ROLE", "")
	t.Setenv("GT_ROLE", "")
	adaptiveRole = ""

	var buf bytes.Buffer
	runAdaptiveBoot(t.TempDir(), &buf, false)

	if strings.Contains(buf.String(), "applying host tuning") {
		t.Errorf("should not apply when script missing; got output: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("expected a 'not found' skip note; got: %q", buf.String())
	}
}

// TestRunAdaptiveBoot_RunsScriptAndIsFailOpenOnNonZero verifies the helper
// executes the resolved script and swallows a non-zero exit (fail-open).
func TestRunAdaptiveBoot_RunsScriptAndIsFailOpenOnNonZero(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "tools", "ops")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Script records its args then exits non-zero — runAdaptiveBoot must not fail.
	marker := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + marker + "\nexit 7\n"
	scriptFile := filepath.Join(scriptDir, "adaptive-boot.sh")
	if err := os.WriteFile(scriptFile, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOV_TOOLS", dir)
	t.Setenv("GT_TOWN_HOST_ROLE", "")
	t.Setenv("GT_ROLE", "")
	adaptiveRole = "" // no role → print mode

	var buf bytes.Buffer
	// Must not panic / must return normally despite exit 7.
	runAdaptiveBoot(t.TempDir(), &buf, false)

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("script did not run (marker missing): %v", err)
	}
	if !strings.Contains(string(got), "--print") {
		t.Errorf("script should have been invoked with --print; got args: %q", string(got))
	}

	// Reset shared global to avoid cross-test leakage.
	adaptiveRole = ""
}
