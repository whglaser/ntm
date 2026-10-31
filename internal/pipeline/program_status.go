package pipeline

import (
	"log/slog"

	"github.com/Dicklesworthstone/ntm/internal/programstatus"
	"github.com/Dicklesworthstone/ntm/internal/status"
)

// OSC 7501 program-status capture for pipeline runs.
//
// The executor prefers an agent's own OSC 7501 reports over chrome scraping
// while waiting for a dispatched step to settle (an idle pi pane misread
// as working burns the step timeout). The capture is
// lazy and per-pane: the first dispatch to a pane starts a raw pipe-pane tap
// through programstatus.Tap, which reads the pane's output stream — the only
// place OSC sequences exist. That tap is the same composition the serve hub
// and the CLI wait/activity surfaces use (ORI). Polling fallback events are
// rendered screen captures and are ignored, so a scrape can never masquerade
// as an authoritative status.
//
// Tests and embedders that do not run a live tmux server inject a source with
// SetProgramStatusSource; the detector preference is identical either way.

// SetProgramStatusSource installs an OSC 7501 source and wraps the current
// detector so fresh reports win over scraping. Production Run paths start
// their own tmux capture; this exists for callers that already have a tap
// (and for tests driving the wait with a deterministic emitter).
//
// Call it before Run/Resume/Execute. The executor's own capture path calls it
// too; a second installation is a no-op because the detector is wrapped once.
func (e *Executor) SetProgramStatusSource(source status.ProgramStatusSource) {
	if e == nil || source == nil {
		return
	}
	e.programStatusMu.Lock()
	defer e.programStatusMu.Unlock()
	if e.programStatusWrapped {
		return
	}
	e.detector = status.NewOSCDetector(e.detector, source, status.DefaultOSCStatusTTL)
	e.programStatusWrapped = true
}

// ensureProgramStatusCapture starts (idempotently) a raw output tap for
// paneID so reports emitted during the coming turn are recorded before
// waitForIdle polls. It is a no-op for dry runs and injected (non-tmux)
// clients, so unit tests never touch the host tmux server.
//
// beginTurn distinguishes a fresh dispatch from resuming a recorded delivery.
// A new dispatch drops any report left from the pane's previous lifetime or
// earlier step so a stale "done" cannot complete this step before its agent
// starts. A recorded retry/resume keeps the report: it observes the same
// turn, pi emits reports only on state changes, and a "done" that landed
// during the retry gap is the authoritative settle evidence a second Forget
// would destroy (ntm-l6cuz.9).
func (e *Executor) ensureProgramStatusCapture(paneID string, beginTurn bool) {
	if e == nil || e.config.DryRun || paneID == "" {
		return
	}

	e.programStatusMu.Lock()
	tap := e.programStatusTap
	if beginTurn && tap != nil {
		tap.Forget(paneID)
	}
	e.programStatusMu.Unlock()

	if _, live := e.tmuxClient().(realTmuxClient); !live {
		return
	}

	e.programStatusMu.Lock()
	needInit := e.programStatusTap == nil
	if needInit {
		e.programStatusTap = programstatus.NewTap(nil)
	}
	tap = e.programStatusTap
	e.programStatusMu.Unlock()

	if needInit {
		// Install the observer through the same seam tests use, so the
		// preference path is identical in production and tests.
		e.SetProgramStatusSource(tap.Observer())
	}

	// A failed start (for example, another process owns the pane's pipe)
	// leaves scraping as the source; the wait must not fail because its
	// optional authority is unavailable.
	_ = tap.Ensure(paneID)
}

// forgetProgramStatus settles a pane's authoritative report once NTM knows no
// further observation of that turn will happen in this process: a terminal
// step failure or a cancelled run. Retries and recorded resumes keep the
// report because they observe the same turn (ntm-l6cuz.9); without this a
// fresh "working" with no done/clear would outlive its failed step
// (ntm-l6cuz.7).
func (e *Executor) forgetProgramStatus(paneID string) {
	if e == nil || paneID == "" {
		return
	}
	e.programStatusMu.Lock()
	tap := e.programStatusTap
	e.programStatusMu.Unlock()
	if tap != nil {
		tap.Forget(paneID)
	}
}

// resetDetectorPaneState drops per-pane classification history so a state
// derived from a previous step cannot leak into this one (ntm-l6cuz.3). The
// detector is wrapped (OSCDetector) in production, so the optional interface
// is forwarded by the wrapper as well as implemented by UnifiedDetector.
func (e *Executor) resetDetectorPaneState(paneID string) {
	if paneID == "" {
		return
	}
	if resetter, ok := e.detector.(interface{ ResetPane(string) }); ok {
		resetter.ResetPane(paneID)
		return
	}
	// A tracker that can only reset itself cannot be reached through the
	// detector interface; record the gap instead of silently keeping stale
	// classification state.
	slog.Debug("pipeline.detector.reset_unavailable", "pane_id", paneID)
}

// stopProgramStatusCapture retires every tap started by
// ensureProgramStatusCapture. Safe to call when capture never started.
func (e *Executor) stopProgramStatusCapture() {
	if e == nil {
		return
	}
	e.programStatusMu.Lock()
	tap := e.programStatusTap
	e.programStatusMu.Unlock()
	if tap != nil {
		tap.StopAll()
	}
}
