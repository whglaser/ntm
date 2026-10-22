package status

import (
	"errors"
	"testing"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/programstatus"
)

type fakeProgramStatusSource struct {
	events map[string]programstatus.Event
}

func (f *fakeProgramStatusSource) Latest(paneID string) (programstatus.Event, bool) {
	event, ok := f.events[paneID]
	return event, ok
}

type fakeStatusDetector struct {
	statuses map[string]AgentStatus
	all      []AgentStatus
	err      error
}

func (f *fakeStatusDetector) Detect(paneID string) (AgentStatus, error) {
	if f.err != nil {
		return AgentStatus{}, f.err
	}
	status, ok := f.statuses[paneID]
	if !ok {
		return AgentStatus{}, errors.New("pane not found")
	}
	return status, nil
}

func (f *fakeStatusDetector) DetectAll(string) ([]AgentStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]AgentStatus(nil), f.all...), nil
}

// atClock pins a detector to a fixed instant for freshness tests.
func atClock(detector *OSCDetector, at time.Time) {
	detector.now = func() time.Time { return at }
}

func TestOSCDetectorPrefersFreshOSCReport(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", State: programstatus.StateDone, App: "pi", ObservedAt: at},
	}}
	fallback := &fakeStatusDetector{statuses: map[string]AgentStatus{
		// The scrape path misclassifies the idle pane the way the observed
		// failure did; the OSC report must win.
		"%0": {PaneID: "%0", PaneName: "cc_1", AgentType: "pi", State: StateWorking},
	}}

	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	atClock(detector, at.Add(time.Minute))

	got, err := detector.Detect("%0")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.State != StateIdle {
		t.Fatalf("state = %q, want idle from osc", got.State)
	}
	if got.StateSource != StateSourceOSC {
		t.Fatalf("source = %q, want osc", got.StateSource)
	}
	// Pane metadata still comes from the fallback.
	if got.PaneName != "cc_1" || got.AgentType != "pi" {
		t.Fatalf("metadata lost: %+v", got)
	}
}

func TestOSCDetectorFallsBackWithoutReport(t *testing.T) {
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{}}
	fallback := &fakeStatusDetector{statuses: map[string]AgentStatus{
		"%0": {PaneID: "%0", State: StateIdle},
	}}

	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	got, err := detector.Detect("%0")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.State != StateIdle || got.StateSource != StateSourceScrape {
		t.Fatalf("got state=%q source=%q, want idle/scrape", got.State, got.StateSource)
	}
}

func TestOSCDetectorExpiresOldReport(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", State: programstatus.StateWorking, ObservedAt: at},
	}}
	fallback := &fakeStatusDetector{statuses: map[string]AgentStatus{
		"%0": {PaneID: "%0", State: StateIdle},
	}}

	detector := NewOSCDetector(fallback, source, time.Minute)
	atClock(detector, at.Add(2*time.Minute))

	got, err := detector.Detect("%0")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.State != StateIdle || got.StateSource != StateSourceScrape {
		t.Fatalf("stale osc report was used: %+v", got)
	}
}

func TestOSCDetectorIgnoresQueriesAndUnknownStates(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", Query: true, ObservedAt: at},
		"%1": {PaneID: "%1", State: "paused", ObservedAt: at},
	}}
	fallback := &fakeStatusDetector{statuses: map[string]AgentStatus{
		"%0": {PaneID: "%0", State: StateIdle},
		"%1": {PaneID: "%1", State: StateIdle},
	}}

	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	atClock(detector, at)

	for _, paneID := range []string{"%0", "%1"} {
		got, err := detector.Detect(paneID)
		if err != nil {
			t.Fatalf("Detect(%s): %v", paneID, err)
		}
		if got.StateSource != StateSourceScrape || got.State != StateIdle {
			t.Fatalf("Detect(%s) = %+v, want scrape/idle", paneID, got)
		}
	}
}

func TestOSCDetectorReturnsOSCWhenFallbackFails(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", State: programstatus.StateError, Message: "rate limited", ObservedAt: at},
	}}
	fallback := &fakeStatusDetector{err: errors.New("tmux capture failed")}

	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	atClock(detector, at)

	got, err := detector.Detect("%0")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.State != StateError || got.ErrorType != ErrorGeneric || got.StateSource != StateSourceOSC {
		t.Fatalf("got %+v, want error/osc", got)
	}
}

func TestOSCDetectorBlockedIsNotIdle(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, state := range []programstatus.State{programstatus.StateWorking, programstatus.StateBlocked} {
		source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
			"%0": {PaneID: "%0", State: state, ObservedAt: at},
		}}
		fallback := &fakeStatusDetector{statuses: map[string]AgentStatus{
			"%0": {PaneID: "%0", State: StateIdle},
		}}
		detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
		atClock(detector, at)

		got, err := detector.Detect("%0")
		if err != nil {
			t.Fatalf("Detect(%s): %v", state, err)
		}
		if got.State != StateWorking {
			t.Fatalf("%s mapped to %q, want working (never idle)", state, got.State)
		}
	}
}

func TestOSCDetectorDetectAllOverridesPerPane(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%1": {PaneID: "%1", State: programstatus.StateDone, ObservedAt: at},
	}}
	fallback := &fakeStatusDetector{all: []AgentStatus{
		{PaneID: "%0", State: StateWorking},
		{PaneID: "%1", State: StateWorking},
	}}

	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	atClock(detector, at)

	got, err := detector.DetectAll("sess")
	if err != nil {
		t.Fatalf("DetectAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want 2", len(got))
	}
	if got[0].State != StateWorking || got[0].StateSource != StateSourceScrape {
		t.Fatalf("pane 0 = %+v, want working/scrape", got[0])
	}
	if got[1].State != StateIdle || got[1].StateSource != StateSourceOSC {
		t.Fatalf("pane 1 = %+v, want idle/osc", got[1])
	}
}

func TestProgramStatusStateMapping(t *testing.T) {
	tests := []struct {
		state programstatus.State
		want  AgentState
		known bool
	}{
		{programstatus.StateIdle, StateIdle, true},
		{programstatus.StateDone, StateIdle, true},
		{programstatus.StateWorking, StateWorking, true},
		{programstatus.StateBlocked, StateWorking, true},
		{programstatus.StateError, StateError, true},
		{programstatus.StateClear, StateUnknown, false},
		{"paused", StateUnknown, false},
	}
	for _, tt := range tests {
		got, _, known := ProgramStatusState(programstatus.Event{State: tt.state})
		if got != tt.want || known != tt.known {
			t.Errorf("ProgramStatusState(%q) = (%q, known=%v), want (%q, known=%v)",
				tt.state, got, known, tt.want, tt.known)
		}
	}
}
