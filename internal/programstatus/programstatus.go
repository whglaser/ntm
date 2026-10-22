// Package programstatus decodes and tracks the terminal Program Status
// Protocol (OSC 7501), the authoritative state channel emitted by pi
// (pi-tui).
//
// # Wire format
//
// A report is a single Operating System Command:
//
//	ESC ] 7501 ; <pairs> BEL
//	ESC ] 7501 ; <pairs> ESC \
//
// Both BEL (0x07) and ST (ESC \) terminators are accepted. The payload is a
// colon-separated list of key=value pairs. pi 1.x emits exactly these keys
// (see @earendil-works/pi-tui/dist/program-status.js):
//
//	state  (required)  working | blocked | done | error | idle | clear
//	app    (optional)  application name, 1-32 chars of [A-Za-z0-9_.+-]
//	kind   (optional)  blocked-dialog kind, only when state=blocked
//	msg    (optional)  base64 (std) UTF-8 message, capped at 2048 decoded bytes
//
// "clear" is the only state pi emits from its shutdown path (it removes a
// previously reported status); consumers remove the pane's stored status on
// "clear". A query form exists for terminals:
//
//	ESC ] 7501 ; ? ESC \
//
// A terminal that understands the protocol replies with the same body. pi
// only emits reports after it believes the terminal supports the protocol:
// the terminal must answer the query, or the pane must run with
// PI_PROGRAM_STATUS=1. tmux does not answer the query on the pane's behalf,
// so ntm-launched pi panes need that environment variable to produce status
// reports at all.
//
// # Semantics
//
// pi reports:
//   - working while an agent run or a compaction is in flight,
//   - blocked while a dialog waits for the user (kind names the dialog),
//   - done once a run settles after a successful assistant message,
//   - error when the latest assistant message ended with an error,
//   - idle when a run is aborted or the session is reset.
//
// Reports describe the whole pane/application (the root record); this package
// deliberately ignores nested conventions that later spec revisions may add.
//
// Capture never scrapes rendered pane content: the pane's raw output stream
// must be tapped (tmux pipe-pane, as tmux.PaneStreamer already does) and fed
// to an Observer or Parser. Screen captures do not contain OSC sequences, so
// a scrape can never masquerade as an authoritative status.
package programstatus

import "time"

// State is a program status state value.
type State string

const (
	// StateClear removes a previously reported status. pi emits it when it
	// stops (suspend/exit) and reports nothing else.
	StateClear State = "clear"
	// StateIdle means no run is active after an abort or a reset.
	StateIdle State = "idle"
	// StateWorking means an agent run or compaction is in flight.
	StateWorking State = "working"
	// StateBlocked means a dialog is waiting for the user.
	StateBlocked State = "blocked"
	// StateDone means the last run settled successfully.
	StateDone State = "done"
	// StateError means the last assistant message ended with an error.
	StateError State = "error"
)

// Event is one decoded OSC 7501 report.
type Event struct {
	// PaneID is the tmux pane the report was observed on. It is opaque to
	// this package; callers supply the pane identity of the tap.
	PaneID string `json:"pane_id"`
	// State is the decoded "state" pair. Empty only for a query.
	State State `json:"state,omitempty"`
	// App is the decoded "app" pair (pi reports its APP_NAME).
	App string `json:"app,omitempty"`
	// Kind is the decoded "kind" pair, present only for blocked dialogs.
	Kind string `json:"kind,omitempty"`
	// Message is the base64-decoded "msg" pair. pi limits it to the session
	// name, dialog titles, and the first line of errors; prompts and
	// assistant output are never reported.
	Message string `json:"message,omitempty"`
	// Values holds every raw wire pair, including keys this package does not
	// know, so a spec revision stays observable.
	Values map[string]string `json:"values,omitempty"`
	// Query is true for the capability query form (ESC]7501;?), which is not
	// a state report.
	Query bool `json:"query,omitempty"`
	// ObservedAt is when the terminating byte was parsed.
	ObservedAt time.Time `json:"observed_at"`
}

// IsReport reports whether the event carries a state (i.e., is not a query
// and is not malformed).
func (e Event) IsReport() bool {
	return !e.Query && e.State != ""
}
