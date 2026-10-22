package status

import (
	"time"

	"github.com/Dicklesworthstone/ntm/internal/programstatus"
)

// DefaultOSCStatusTTL bounds how long an OSC 7501 report is trusted. A turn
// can run for many minutes with no further report (pi reports state changes,
// not heartbeats), so the TTL is deliberately longer than any ordinary turn.
// When it lapses the detector falls back to scraping, which is what protects
// against a pane whose agent died without emitting "clear".
const DefaultOSCStatusTTL = 30 * time.Minute

// ProgramStatusSource supplies the most recent OSC 7501 report for a pane.
// programstatus.Observer (and its Registry) implement it.
type ProgramStatusSource interface {
	Latest(paneID string) (programstatus.Event, bool)
}

// OSCDetector wraps another Detector and prefers fresh, authoritative OSC
// 7501 reports over chrome scraping. Every returned status records which
// source decided its state (StateSourceOSC or StateSourceScrape), so callers
// can audit the classification.
//
// A fresh GUI report wins even when the fallback capture fails: the protocol
// is the agent's own statement about its state, and the scrape result only
// carries pane metadata. Without a fresh report the wrapped detector's result
// is returned unchanged apart from the source tag.
type OSCDetector struct {
	fallback Detector
	source   ProgramStatusSource
	ttl      time.Duration
	now      func() time.Time
}

// NewOSCDetector wraps fallback with programStatus. A nil fallback uses
// NewDetector; a nil source or a non-positive ttl disables the preference
// (ttl <= 0 means "never expire", which callers should pair with an explicit
// Forget on pane lifetime changes).
func NewOSCDetector(fallback Detector, programStatus ProgramStatusSource, ttl time.Duration) *OSCDetector {
	if fallback == nil {
		fallback = NewDetector()
	}
	return &OSCDetector{
		fallback: fallback,
		source:   programStatus,
		ttl:      ttl,
		now:      time.Now,
	}
}

// Detect returns the pane's status, preferring a fresh OSC report.
func (d *OSCDetector) Detect(paneID string) (AgentStatus, error) {
	fallbackStatus, fallbackErr := d.fallback.Detect(paneID)

	if event, ok := d.freshEventAt(paneID, d.now()); ok {
		if state, errType, known := ProgramStatusState(event); known {
			if fallbackErr != nil {
				// The OSC report is authoritative even when the tmux
				// capture failed; only pane metadata is missing.
				return AgentStatus{
					PaneID:      paneID,
					State:       state,
					ErrorType:   errType,
					StateSource: StateSourceOSC,
					UpdatedAt:   d.now(),
				}, nil
			}
			fallbackStatus.State = state
			fallbackStatus.ErrorType = errType
			fallbackStatus.StateSource = StateSourceOSC
			fallbackStatus.UpdatedAt = d.now()
			return fallbackStatus, nil
		}
	}

	if fallbackErr != nil {
		return fallbackStatus, fallbackErr
	}
	fallbackStatus.StateSource = StateSourceScrape
	return fallbackStatus, nil
}

// DetectAll returns statuses for every pane in session, with per-pane OSC
// overrides where fresh reports exist.
func (d *OSCDetector) DetectAll(session string) ([]AgentStatus, error) {
	statuses, err := d.fallback.DetectAll(session)
	if err != nil {
		return statuses, err
	}
	now := d.now()
	for i := range statuses {
		statuses[i].StateSource = StateSourceScrape
		event, ok := d.freshEventAt(statuses[i].PaneID, now)
		if !ok {
			continue
		}
		state, errType, known := ProgramStatusState(event)
		if !known {
			continue
		}
		statuses[i].State = state
		statuses[i].ErrorType = errType
		statuses[i].StateSource = StateSourceOSC
		statuses[i].UpdatedAt = now
	}
	return statuses, nil
}

// freshEventAt returns the pane's latest report when it is a state report and
// within the freshness window.
func (d *OSCDetector) freshEventAt(paneID string, now time.Time) (programstatus.Event, bool) {
	if d.source == nil {
		return programstatus.Event{}, false
	}
	event, ok := d.source.Latest(paneID)
	if !ok || !event.IsReport() {
		return programstatus.Event{}, false
	}
	// A clock that moves backwards (NTP step, test clock) must not make a
	// report that has not expired look stale.
	if age := now.Sub(event.ObservedAt); d.ttl > 0 && age > d.ttl {
		return programstatus.Event{}, false
	}
	return event, true
}

// ProgramStatusState maps a protocol report to NTM's agent state. known is
// false for states this package does not model (a future spec revision), in
// which case callers fall back to their scrape source rather than guessing.
//
// "blocked" maps to working, not idle: a pane waiting on a dialog is not
// available for the next unit of work, and treating it as idle would let a
// pipeline step complete while its agent still holds an unanswered prompt.
func ProgramStatusState(event programstatus.Event) (AgentState, ErrorType, bool) {
	switch event.State {
	case programstatus.StateDone, programstatus.StateIdle:
		return StateIdle, ErrorNone, true
	case programstatus.StateWorking, programstatus.StateBlocked:
		return StateWorking, ErrorNone, true
	case programstatus.StateError:
		return StateError, ErrorGeneric, true
	default:
		return StateUnknown, ErrorNone, false
	}
}
