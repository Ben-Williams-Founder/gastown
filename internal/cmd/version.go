package cmd

import (
	"fmt"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/version"
)

// Version information - set at build time via ldflags
var (
	Version = "1.2.1"
	// Build can be set via ldflags at compile time
	Build = "dev"
	// Commit and Branch - the git revision the binary was built from (optional ldflag)
	Commit = ""
	Branch = ""
	// BuiltProperly is set to "1" by `make build`. If empty, the binary was built
	// with raw `go build` and is likely unsigned (will be killed on macOS).
	BuiltProperly = ""

	// Provenance stamp — set ONLY by deploy/deploy-gt.sh from a gate-emitted
	// attestation (verification-derived; never from bare git state). Empty values
	// mean the binary was built outside the attested deploy path ("unattested").
	// VerifiedBase is the live-lineage commit the candidate was superset-verified
	// against; PatchSetHash is sha256 of deploy/fork-patch-signatures.tsv at
	// attest time; AttestationID identifies the attestation record.
	VerifiedBase   = ""
	PatchSetHash   = ""
	AttestationID  = ""
)

var versionVerbose bool
var versionShort bool
var versionProvenance bool

var versionCmd = &cobra.Command{
	Use:         "version",
	GroupID:     GroupDiag,
	Annotations: map[string]string{AnnotationPolecatSafe: "true"},
	Short:       "Print version information",
	Long: `Print the gt version, build type, git branch, and commit hash.

Output includes the semantic version, whether this is a dev or release build,
and the git revision the binary was built from (if available).`,
	Run: func(cmd *cobra.Command, args []string) {
		if versionProvenance {
			printProvenance()
			return
		}
		if versionShort {
			fmt.Printf("%s-%s\n", Version, Build)
			return
		}

		commit := resolveCommitHash()
		branch := resolveBranch()

		if commit != "" && branch != "" {
			fmt.Printf("gt version %s (%s: %s@%s)\n", Version, Build, branch, version.ShortCommit(commit))
		} else if commit != "" {
			fmt.Printf("gt version %s (%s: %s)\n", Version, Build, version.ShortCommit(commit))
		} else {
			fmt.Printf("gt version %s (%s)\n", Version, Build)
		}

		if versionVerbose {
			fmt.Printf("Timestamp: %s\n", time.Now().Format(time.RFC3339))
			fmt.Printf("Go version: %s\n", runtime.Version())
		}
	},
}

// printProvenance emits the verification-derived provenance stamp in a stable
// key=value format (one per line, machine-parseable). vcsRevision is the
// independent buildinfo cross-check: it proves what SOURCE was compiled, while
// the stamp proves what was VERIFIED — the two must corroborate, and buildinfo
// is never the authority (git state alone cannot identify the live base).
func printProvenance() {
	vb, psh, aid := VerifiedBase, PatchSetHash, AttestationID
	attested := "true"
	if vb == "" && psh == "" && aid == "" {
		attested = "false"
	}
	fmt.Printf("attested=%s\n", attested)
	fmt.Printf("verifiedBase=%s\n", vb)
	fmt.Printf("patchSetHash=%s\n", psh)
	fmt.Printf("attestationId=%s\n", aid)
	vcsRev, vcsMod := "", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				vcsRev = s.Value
			case "vcs.modified":
				vcsMod = s.Value
			}
		}
	}
	fmt.Printf("vcsRevision=%s\n", vcsRev)
	fmt.Printf("vcsModified=%s\n", vcsMod)
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolVarP(&versionVerbose, "verbose", "v", false, "Show extended version info including timestamp")
	versionCmd.Flags().BoolVar(&versionShort, "short", false, "Output only the version number (e.g., 0.5.0-362)")
	versionCmd.Flags().BoolVar(&versionProvenance, "provenance", false, "Print the verification-derived provenance stamp (key=value; attested=false if built outside the attested deploy path)")

	// Pass the build-time commit to the version package for stale binary checks
	if Commit != "" {
		version.SetCommit(Commit)
	}
}

func resolveCommitHash() string {
	if Commit != "" {
		return Commit
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}

	return ""
}

func resolveBranch() string {
	if Branch != "" {
		return Branch
	}

	// Try to get branch from build info (build-time VCS detection)
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.branch" && setting.Value != "" {
				return setting.Value
			}
		}
	}

	// Fallback: try to get branch from git at runtime
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = "."
	if output, err := cmd.Output(); err == nil {
		if branch := strings.TrimSpace(string(output)); branch != "" && branch != "HEAD" {
			return branch
		}
	}

	return ""
}
