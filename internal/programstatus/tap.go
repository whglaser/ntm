package programstatus

import (
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// Tap is the single OSC 7501 capture composition shared by the pipeline
// executor, the serve streaming hub, and the CLI wait/activity surfaces
// (ORI): one Observer fed by lazily started raw pipe-pane taps over tmux.
// Screen captures never reach the Observer — only raw stream bytes can
// contain OSC sequences, so a scrape can never masquerade as an authoritative
// status.
//
// A tap is exclusive per pane: when another process already owns the pane's
// pipe, StartStream fails and the caller keeps its scrape fallback. Capture
// is an authority upgrade, never a dependency.
type Tap struct {
	observer *Observer
	streams  *tmux.StreamManager
}

// TapHandler receives every raw stream event after its OSC reports have been
// recorded, so an embedder can publish both the bytes and the decoded
// reports. reports is nil for events that carried none (and always nil for
// rendered polling events).
type TapHandler func(event tmux.StreamEvent, reports []Event)

// NewTap builds a status tap over the default tmux client: capture is
// pipe-pane only, because a rendered polling capture can never contain an OSC
// sequence. A failed start (pipe-pane owned by another process, no such pane)
// leaves the caller's scrape fallback in charge. handler may be nil.
func NewTap(handler TapHandler) *Tap { return newTap(handler, false) }

// NewStreamingTap is NewTap with the streamer's polling fallback enabled, for
// embedders that publish raw pane output and need content even when pipe-pane
// is unavailable (the serve hub's WebSocket stream). OSC decoding uses the
// same path either way.
func NewStreamingTap(handler TapHandler) *Tap { return newTap(handler, true) }

func newTap(handler TapHandler, pollingFallback bool) *Tap {
	tap := &Tap{observer: NewObserver()}
	cfg := tmux.DefaultPaneStreamerConfig()
	cfg.DisablePollingFallback = !pollingFallback
	tap.streams = tmux.NewStreamManager(tmux.DefaultClient, func(event tmux.StreamEvent) {
		reports := tap.ObserveEvent(event)
		if handler != nil {
			handler(event, reports)
		}
	}, cfg)
	return tap
}

// Observer returns the shared report registry for this tap.
func (t *Tap) Observer() *Observer {
	if t == nil {
		return nil
	}
	return t.observer
}

// Ensure starts the pane's raw tap. It is idempotent while the stream is
// active. A failed start (for example, another process already owns the
// pane's pipe) is advisory: the caller falls back to scraping.
func (t *Tap) Ensure(paneID string) error {
	if t == nil || t.streams == nil || paneID == "" {
		return nil
	}
	return t.streams.StartStream(paneID)
}

// Forget drops the pane's recorded report. Callers use it when a pane's
// process lifetime changes or a turn NTM knows is no longer observed ends.
func (t *Tap) Forget(paneID string) {
	if t == nil || t.observer == nil || paneID == "" {
		return
	}
	t.observer.Forget(paneID)
}

// ObserveEvent decodes one raw stream event into the shared Observer and
// returns every report it contained. Polling events (IsFull) carry rendered
// screen captures and are ignored, as are empty events.
func (t *Tap) ObserveEvent(event tmux.StreamEvent) []Event {
	if t == nil || t.observer == nil || event.IsFull || event.Target == "" || len(event.Lines) == 0 {
		return nil
	}
	var reports []Event
	for _, line := range event.Lines {
		// The line's trailing newline was stripped by the streamer and is not
		// re-added: newlines never occur inside a protocol sequence, and
		// dropping them keeps the parser correct no matter how a future tap
		// chunks its reads.
		reports = append(reports, t.observer.Observe(event.Target, []byte(line))...)
	}
	return reports
}

// StopStream stops one pane's tap. The pane can be re-tapped later.
func (t *Tap) StopStream(target string) {
	if t == nil || t.streams == nil {
		return
	}
	t.streams.StopStream(target)
}

// StopAll retires every tap. The tap stays usable for later streams.
func (t *Tap) StopAll() {
	if t == nil || t.streams == nil {
		return
	}
	t.streams.StopAll()
}

// ListActive returns the targets with an active tap.
func (t *Tap) ListActive() []string {
	if t == nil || t.streams == nil {
		return nil
	}
	return t.streams.ListActive()
}

// Stats returns the underlying streamer statistics.
func (t *Tap) Stats() map[string]interface{} {
	if t == nil || t.streams == nil {
		return nil
	}
	return t.streams.Stats()
}
