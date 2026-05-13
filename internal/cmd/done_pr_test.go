package cmd

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

func TestDoneNeedsGitHubPR(t *testing.T) {
	tests := []struct {
		name     string
		settings *config.RigSettings
		want     bool
	}{
		{name: "nil settings", want: false},
		{name: "no merge queue", settings: &config.RigSettings{}, want: false},
		{name: "default mr strategy", settings: &config.RigSettings{MergeQueue: &config.MergeQueueConfig{}}, want: false},
		{name: "github pr strategy", settings: &config.RigSettings{MergeQueue: &config.MergeQueueConfig{MergeStrategy: "pr", VCSProvider: "github"}}, want: true},
		{name: "empty provider defaults to github", settings: &config.RigSettings{MergeQueue: &config.MergeQueueConfig{MergeStrategy: "pr"}}, want: true},
		{name: "bitbucket provider skipped", settings: &config.RigSettings{MergeQueue: &config.MergeQueueConfig{MergeStrategy: "pr", VCSProvider: "bitbucket"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := doneNeedsGitHubPR(tt.settings); got != tt.want {
				t.Fatalf("doneNeedsGitHubPR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDonePRTitle(t *testing.T) {
	tests := []struct {
		name   string
		issue  *beads.Issue
		id     string
		wanted string
	}{
		{name: "issue title and id", issue: &beads.Issue{Title: "Fix done"}, id: "gt-123", wanted: "Fix done (gt-123)"},
		{name: "issue title only", issue: &beads.Issue{Title: "Fix done"}, wanted: "Fix done"},
		{name: "id fallback", id: "gt-123", wanted: "gt-123"},
		{name: "generic fallback", wanted: "Polecat work"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := donePRTitle(tt.issue, tt.id); got != tt.wanted {
				t.Fatalf("donePRTitle() = %q, want %q", got, tt.wanted)
			}
		})
	}
}

func TestBuildDonePRBody(t *testing.T) {
	issue := &beads.Issue{Description: strings.Join([]string{
		"Implement the safety check.",
		"attached_molecule: hq-test",
		"dispatched_by: mayor",
		"formula_vars: base_branch=main",
		"Keep this detail.",
	}, "\n")}

	body := buildDonePRBody(issue, "gt-123", "minuteman", " internal/cmd/done.go | 12 ++++++++++++")

	for _, want := range []string{
		"## Summary",
		"Implement the safety check.",
		"Keep this detail.",
		"## Changes",
		"internal/cmd/done.go",
		"*Polecat: minuteman | Issue: gt-123*",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR body missing %q:\n%s", want, body)
		}
	}

	for _, unwanted := range []string{"attached_molecule", "dispatched_by", "formula_vars"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("PR body contains attachment metadata %q:\n%s", unwanted, body)
		}
	}
}
