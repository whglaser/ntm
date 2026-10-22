package pipeline

import (
	"github.com/Dicklesworthstone/ntm/internal/programstatus"
	"github.com/Dicklesworthstone/ntm/internal/status"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// OSC 7501 program-status capture for pipeline runs.
//
// The executor prefers an agent's own OSC 7501 reports over chrome scraping
// while waiting for a dispatched step to settle (an idle pi pane misread
// as working burns the step timeout). The capture is
// lazy and per-pane: the first dispatch to a pane starts a raw pipe-pane tap
// through tmux.StreamManager, which reads the pane's output stream — the only
// place OSC sequences exist. Polling fallback events are rendered screen
// captures and are ignored, so a scrape can never masquerade as an
// authoritative status.
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
func (e *Executor) ensureProgramStatusCapture(paneID string) {
	if e == nil || e.config.DryRun || paneID == "" {
		return
	}
	if _, live := e.tmuxClient().(realTmuxClient); !live {
		return
	}

	e.programStatusMu.Lock()
	needInit := e.programStatusObserver == nil
	if needInit {
		observer := programstatus.NewObserver()
		e.programStatusObserver = observer
		e.programStatusStreams = tmux.NewStreamManager(tmux.DefaultClient, func(event tmux.StreamEvent) {
			if event.IsFull || event.Target == "" || len(event.Lines) == 0 {
				return
			}
			for _, line := range event.Lines {
				observer.Observe(event.Target, []byte(line))
			}
		}, tmux.DefaultPaneStreamerConfig())
	}
	observer := e.programStatusObserver
	e.programStatusMu.Unlock()

	if needInit {
		// Install the observer through the same seam tests use, so the
		// preference path is identical in production and tests.
		e.SetProgramStatusSource(observer)
	}

	// A new dispatch begins a new turn. Drop any report left from the pane's
	// previous lifetime or earlier step so a stale "done" cannot complete
	// this step before its agent starts.
	e.programStatusMu.Lock()
	observer.Forget(paneID)
	// A failed start (for example, another process owns the pane's pipe)
	// leaves scraping as the source; the wait must not fail because its
	// optional authority is unavailable.
	_ = e.programStatusStreams.StartStream(paneID)
	e.programStatusMu.Unlock()
}

// stopProgramStatusCapture retires every tap started by
// ensureProgramStatusCapture. Safe to call when capture never started.
func (e *Executor) stopProgramStatusCapture() {
	if e == nil {
		return
	}
	e.programStatusMu.Lock()
	streams := e.programStatusStreams
	e.programStatusMu.Unlock()
	if streams != nil {
		streams.StopAll()
	}
}
