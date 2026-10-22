package programstatus

import (
	"sync"
	"time"
)

// Registry stores the most recent report observed for each pane. It is the
// read side of a capture tap: callers feed it decoded Events (directly or via
// an Observer) and ask for the latest state per pane.
//
// A Registry is safe for concurrent use by multiple goroutines.
type Registry struct {
	mu     sync.RWMutex
	latest map[string]Event
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{latest: make(map[string]Event)}
}

// Apply records event as the pane's latest report. A query carries no state
// and is ignored; a "clear" state (pi's shutdown report) removes the pane's
// entry entirely so consumers fall back to their next source.
func (r *Registry) Apply(event Event) {
	switch {
	case event.Query:
		return
	case event.PaneID == "":
		return
	case event.State == StateClear:
		r.Forget(event.PaneID)
		return
	}
	r.mu.Lock()
	r.latest[event.PaneID] = event
	r.mu.Unlock()
}

// Latest returns the pane's most recent report. Reports never expire on their
// own: the freshness policy belongs to the consumer (see
// status.NewOSCDetector), because only it knows how stale a report may be
// before its fallback is more trustworthy.
func (r *Registry) Latest(paneID string) (Event, bool) {
	r.mu.RLock()
	event, ok := r.latest[paneID]
	r.mu.RUnlock()
	return event, ok
}

// Forget removes a pane's entry, for example when the pane's process lifetime
// ends and a successor must not inherit the predecessor's state.
func (r *Registry) Forget(paneID string) {
	r.mu.Lock()
	delete(r.latest, paneID)
	r.mu.Unlock()
}

// Snapshot returns a copy of every pane's latest report.
func (r *Registry) Snapshot() map[string]Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Event, len(r.latest))
	for paneID, event := range r.latest {
		out[paneID] = event
	}
	return out
}

// Len returns the number of panes with a recorded report.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.latest)
}

// Observer routes raw pane output chunks through one incremental parser per
// pane and records decoded reports in a Registry. Chunk boundaries are
// arbitrary: a sequence split across Observe calls is reassembled per pane.
//
// Observer is the capture half of the OSC 7501 pipeline. Feed it only from a
// raw output tap (tmux pipe-pane); feeding rendered pane captures is
// pointless — they cannot contain OSC sequences — and defeats the
// authoritative-status guarantee.
type Observer struct {
	mu       sync.Mutex
	parsers  map[string]*Parser
	registry *Registry
	now      func() time.Time
}

// NewObserver returns an observer with an empty registry.
func NewObserver() *Observer {
	return &Observer{
		parsers:  make(map[string]*Parser),
		registry: NewRegistry(),
		now:      time.Now,
	}
}

// Observe feeds one raw output chunk for paneID and returns every complete
// report it contained.
func (o *Observer) Observe(paneID string, chunk []byte) []Event {
	o.mu.Lock()
	parser := o.parsers[paneID]
	if parser == nil {
		parser = NewParser(paneID)
		parser.SetClock(o.now)
		o.parsers[paneID] = parser
	}
	events := parser.Feed(chunk)
	for _, event := range events {
		o.registry.Apply(event)
	}
	o.mu.Unlock()
	return events
}

// Latest returns the pane's latest report. *Observer satisfies interfaces that
// only need the read side of the capture.
func (o *Observer) Latest(paneID string) (Event, bool) {
	return o.registry.Latest(paneID)
}

// Forget drops a pane's parser and stored report. Use it when a pane's
// process lifetime changes: the half-parsed parser state must not be reused,
// because a successor process could complete a sequence its predecessor
// started.
func (o *Observer) Forget(paneID string) {
	o.mu.Lock()
	if parser := o.parsers[paneID]; parser != nil {
		parser.Reset()
	}
	delete(o.parsers, paneID)
	o.mu.Unlock()
	o.registry.Forget(paneID)
}

// Len returns the number of panes with a recorded report.
func (o *Observer) Len() int {
	return o.registry.Len()
}

// Snapshot returns a copy of every pane's latest report.
func (o *Observer) Snapshot() map[string]Event {
	return o.registry.Snapshot()
}
