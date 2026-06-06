package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/activity"
	"github.com/steveyegge/gastown/internal/constants"
)

// snapshotAgent matches the agent object in status-snapshot.json.
type snapshotAgent struct {
	Name      string `json:"name"`
	Address   string `json:"address"`
	Session   string `json:"session"`
	Role      string `json:"role"`
	Running   bool   `json:"running"`
	HasWork   bool   `json:"has_work"`
	State     string `json:"state"`
	AgentInfo string `json:"agent_info"`
}

// snapshotHook matches the hook object inside rig entries.
type snapshotHook struct {
	Agent   string `json:"agent"`
	Role    string `json:"role"`
	HasWork bool   `json:"has_work"`
}

// snapshotRig matches the rig object in status-snapshot.json.
type snapshotRig struct {
	Name         string          `json:"name"`
	PolecatCount int             `json:"polecat_count"`
	CrewCount    int             `json:"crew_count"`
	HasWitness   bool            `json:"has_witness"`
	HasRefinery  bool            `json:"has_refinery"`
	Hooks        []snapshotHook  `json:"hooks"`
	Agents       []snapshotAgent `json:"agents"`
}

// snapshotDolt matches the dolt object in status-snapshot.json.
type snapshotDolt struct {
	Running bool `json:"running"`
	Port    int  `json:"port"`
}

// snapshotDaemon matches the daemon object in status-snapshot.json.
type snapshotDaemon struct {
	Running bool `json:"running"`
}

// statusSnapshot is the top-level structure of status-snapshot.json.
type statusSnapshot struct {
	Daemon snapshotDaemon  `json:"daemon"`
	Dolt   snapshotDolt    `json:"dolt"`
	Agents []snapshotAgent `json:"agents"`
	Rigs   []snapshotRig   `json:"rigs"`
}

// SnapshotFetcher implements ConvoyFetcher by reading the cached
// status-snapshot.json instead of spawning live bd/tmux commands.
// It populates the workers, rigs, sessions, health, hooks, and mayor
// panels. Convoy-specific panels (convoys, merge queue, mail, dogs,
// escalations, queues, issues, activity) return empty slices because
// that data is not present in the snapshot.
type SnapshotFetcher struct {
	snapshotPath string
}

// SnapshotPath returns the resolved path to the snapshot file.
// If snapshotPath is empty it returns the default location:
// ~/gt/.cache/status-snapshot.json (written by gt-status-snapshot.timer).
func SnapshotPath(snapshotPath string) string {
	if snapshotPath != "" {
		return snapshotPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "gt", ".cache", "status-snapshot.json")
}

// NewSnapshotFetcher creates a fetcher that reads the given snapshot file.
// If snapshotPath is empty, it defaults to ~/gt/.cache/status-snapshot.json.
func NewSnapshotFetcher(snapshotPath string) *SnapshotFetcher {
	return &SnapshotFetcher{snapshotPath: SnapshotPath(snapshotPath)}
}

// load reads and parses the snapshot file.
func (f *SnapshotFetcher) load() (*statusSnapshot, error) {
	data, err := os.ReadFile(f.snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("reading snapshot %s: %w", f.snapshotPath, err)
	}
	var snap statusSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parsing snapshot %s: %w", f.snapshotPath, err)
	}
	return &snap, nil
}

// unknownActivity returns an activity.Info representing no live data.
func unknownActivity() activity.Info {
	return activity.Info{
		FormattedAge: "snapshot",
		ColorClass:   activity.ColorUnknown,
	}
}

// FetchWorkers returns polecats and refineries from the snapshot.
// Activity timestamps are unavailable in the snapshot, so all workers
// report ColorUnknown activity.
func (f *SnapshotFetcher) FetchWorkers() ([]WorkerRow, error) {
	snap, err := f.load()
	if err != nil {
		return nil, err
	}

	var rows []WorkerRow
	for _, rig := range snap.Rigs {
		for _, agent := range rig.Agents {
			var agentType string
			switch agent.Role {
			case "polecat":
				agentType = constants.RolePolecat
			case "refinery":
				agentType = constants.RoleRefinery
			case "crew":
				agentType = constants.RolePolecat // crew displayed like polecats
			default:
				continue // skip witness, etc.
			}

			workStatus := "idle"
			if agent.State == "working" || agent.HasWork {
				workStatus = "working"
			}

			rows = append(rows, WorkerRow{
				Name:         agent.Name,
				Rig:          rig.Name,
				SessionID:    agent.Session,
				LastActivity: unknownActivity(),
				StatusHint:   agent.State,
				WorkStatus:   workStatus,
				AgentType:    agentType,
			})
		}
	}
	return rows, nil
}

// FetchRigs returns rig info from the snapshot.
func (f *SnapshotFetcher) FetchRigs() ([]RigRow, error) {
	snap, err := f.load()
	if err != nil {
		return nil, err
	}

	rows := make([]RigRow, 0, len(snap.Rigs))
	for _, rig := range snap.Rigs {
		rows = append(rows, RigRow{
			Name:         rig.Name,
			PolecatCount: rig.PolecatCount,
			CrewCount:    rig.CrewCount,
			HasWitness:   rig.HasWitness,
			HasRefinery:  rig.HasRefinery,
		})
	}
	return rows, nil
}

// FetchHealth returns system health derived from daemon and dolt snapshot state.
func (f *SnapshotFetcher) FetchHealth() (*HealthRow, error) {
	snap, err := f.load()
	if err != nil {
		return nil, err
	}

	row := &HealthRow{}

	if snap.Daemon.Running {
		row.DeaconHeartbeat = "snapshot (daemon running)"
		row.HeartbeatFresh = true
	} else {
		row.DeaconHeartbeat = "daemon not running"
	}

	return row, nil
}

// FetchSessions returns all agents from the snapshot as session rows.
func (f *SnapshotFetcher) FetchSessions() ([]SessionRow, error) {
	snap, err := f.load()
	if err != nil {
		return nil, err
	}

	var rows []SessionRow

	// Top-level agents (mayor, deacon)
	for _, agent := range snap.Agents {
		if !agent.Running {
			continue
		}
		rows = append(rows, SessionRow{
			Name:    agent.Session,
			Role:    agent.Role,
			Worker:  agent.Name,
			IsAlive: agent.Running,
		})
	}

	// Rig agents
	for _, rig := range snap.Rigs {
		for _, agent := range rig.Agents {
			if !agent.Running {
				continue
			}
			rows = append(rows, SessionRow{
				Name:    agent.Session,
				Role:    agent.Role,
				Rig:     rig.Name,
				Worker:  agent.Name,
				IsAlive: agent.Running,
			})
		}
	}

	return rows, nil
}

// FetchMayor returns the mayor's status from the snapshot.
func (f *SnapshotFetcher) FetchMayor() (*MayorStatus, error) {
	snap, err := f.load()
	if err != nil {
		return nil, err
	}

	for _, agent := range snap.Agents {
		if agent.Name == "mayor" {
			return &MayorStatus{
				IsAttached:  agent.Running,
				SessionName: agent.Session,
				IsActive:    agent.State == "working" || agent.HasWork,
				Runtime:     agent.AgentInfo,
			}, nil
		}
	}

	return &MayorStatus{}, nil
}

// FetchHooks returns hooks that have work attached, derived from rig hook entries.
func (f *SnapshotFetcher) FetchHooks() ([]HookRow, error) {
	snap, err := f.load()
	if err != nil {
		return nil, err
	}

	var rows []HookRow
	for _, rig := range snap.Rigs {
		for _, hook := range rig.Hooks {
			if !hook.HasWork {
				continue
			}
			rows = append(rows, HookRow{
				Assignee: hook.Agent,
				Agent:    formatAgentAddress(hook.Agent),
			})
		}
	}
	return rows, nil
}

// The following methods return empty results because convoy-specific data
// is not available in the status snapshot.

func (f *SnapshotFetcher) FetchConvoys() ([]ConvoyRow, error)         { return nil, nil }
func (f *SnapshotFetcher) FetchMergeQueue() ([]MergeQueueRow, error)  { return nil, nil }
func (f *SnapshotFetcher) FetchMail() ([]MailRow, error)              { return nil, nil }
func (f *SnapshotFetcher) FetchDogs() ([]DogRow, error)               { return nil, nil }
func (f *SnapshotFetcher) FetchEscalations() ([]EscalationRow, error) { return nil, nil }
func (f *SnapshotFetcher) FetchQueues() ([]QueueRow, error)           { return nil, nil }
func (f *SnapshotFetcher) FetchIssues() ([]IssueRow, error)           { return nil, nil }
func (f *SnapshotFetcher) FetchActivity() ([]ActivityRow, error)      { return nil, nil }
