package polecat

import (
	"errors"
	"strings"
	"testing"
)

func okLookPath(string) (string, error)  { return "/usr/bin/systemd-run", nil }
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

func TestWrapInSlice_EscapesSingleQuotes(t *testing.T) {
	cmd := "claude --prompt 'be brief'"
	got := wrapInSliceWith(cmd, "polecat.slice", "linux", okLookPath)
	// Each embedded ' becomes '\'' so the outer single-quoting stays balanced.
	if !strings.Contains(got, `'\''`) {
		t.Fatalf("embedded single quotes must be escaped; got %q", got)
	}
}
