package capacity

import "testing"

func intPtr(i int) *int { return &i }

// TestIsDeferred_CapSemantics — pre-registered from EXP-scheduler-cap-semantics.
// Per DEC-OPS-cap-semantics ratified 2026-07-15: -1 = unbounded-but-governed (defer),
// 0 = truly disabled (direct dispatch), >0 = deferred with cap.
func TestIsDeferred_CapSemantics(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *SchedulerConfig
		expected bool
	}{
		{"nil config = default (-1) = deferred (gated)", &SchedulerConfig{MaxPolecats: nil}, true},
		{"cap=-1 unbounded-but-gated = deferred", &SchedulerConfig{MaxPolecats: intPtr(-1)}, true},
		{"cap=0 truly disabled = direct", &SchedulerConfig{MaxPolecats: intPtr(0)}, false},
		{"cap=1 = deferred", &SchedulerConfig{MaxPolecats: intPtr(1)}, true},
		{"cap=20 = deferred (regression guard)", &SchedulerConfig{MaxPolecats: intPtr(20)}, true},
	}
	for _, c := range cases {
		got := c.cfg.IsDeferred()
		if got != c.expected {
			t.Errorf("%s: IsDeferred() = %v, want %v", c.name, got, c.expected)
		}
	}
}
