// program_status.go wires the CLI wait/activity/status surfaces to the shared
// OSC 7501 capture, so an agent's own state report outranks chrome scraping
// and the deciding source is visible (ntm-l6cuz.2).
package cli

import (
	"context"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/programstatus"
	"github.com/Dicklesworthstone/ntm/internal/status"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// programStatusSourceForSession starts a best-effort raw OSC 7501 tap for the
// session's panes and returns its source plus a stop function. tmux pipe-pane
// is exclusive per pane: a pane already tapped by the serve hub or a pipeline
// run keeps its scrape fallback for this invocation, and a missing session
// simply yields a source that never reports. Capture is an authority upgrade,
// never a dependency — the bounded pane listing below is the only tmux work
// this helper does, and a hung server cannot delay the caller.
//
// It is a var so tests can substitute a deterministic emitter without a live
// tmux server (the CLI-level fake-emitter contract).
var programStatusSourceForSession = func(ctx context.Context, session string) (status.ProgramStatusSource, func()) {
	tap := programstatus.NewTap(nil)
	if session != "" {
		listCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		if panes, err := tmux.GetPanesContext(listCtx, session); err == nil {
			for _, pane := range panes {
				_ = tap.Ensure(pane.ID)
			}
		}
	}
	return tap.Observer(), tap.StopAll
}

// programStatusDetector wraps an OSC source with the standard scrape fallback
// and freshness window. Wait and activity callers use only its capture-free
// DetectReport overlay; status callers use DetectAll over an
// observation-backed fallback so no session is captured twice.
func programStatusDetector(source status.ProgramStatusSource) *status.OSCDetector {
	return status.NewOSCDetector(
		status.NewDetectorWithConfig(status.DefaultConfig()),
		source,
		status.DefaultOSCStatusTTL,
	)
}

// observedSessionStatuses serves the status map from a session observation
// already in hand, so the OSC overlay in status.OSCDetector.DetectAll does not
// capture the session a second time.
type observedSessionStatuses map[string]status.AgentStatus

func (o observedSessionStatuses) Detect(paneID string) (status.AgentStatus, error) {
	if observed, ok := o[paneID]; ok {
		return observed, nil
	}
	return status.AgentStatus{PaneID: paneID, State: status.StateUnknown}, nil
}

func (o observedSessionStatuses) DetectAll(string) ([]status.AgentStatus, error) {
	out := make([]status.AgentStatus, 0, len(o))
	for _, observed := range o {
		out = append(out, observed)
	}
	return out, nil
}
