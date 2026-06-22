package daemon

import (
	"os"
	"testing"

	"github.com/steveyegge/gastown/internal/polecat"
)

// TestCompletionCloseCycle_FlagOff_IsPureNoOp verifies the WS2 Rung-3 daemon
// hook is a true no-op when GT_COMPLETION_CLOSE is unset. The Daemon here has a
// NIL rigPool and NIL config: if completionCloseCycle does ANY work before the
// flag check (rigPool.runPerRig, getKnownRigs, config reads, etc.) it would nil-
// panic. A clean early return touches none of those, so it must not panic.
//
// This is the static/dynamic discriminator for the dispatch-outage investigation:
// if this passes, the flag-off completionCloseCycle cannot be what blocks the
// heartbeat's later dispatchQueuedWork step.
func TestCompletionCloseCycle_FlagOff_IsPureNoOp(t *testing.T) {
	t.Setenv(polecat.CompletionCloseEnv, "") // ensure feature OFF
	os.Unsetenv(polecat.CompletionCloseEnv)

	d := &Daemon{} // nil rigPool, nil config, nil ctx, nil logger

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("completionCloseCycle panicked with flag OFF — it did work before the early return: %v", r)
		}
	}()

	d.completionCloseCycle() // must return immediately, touching nothing
}
