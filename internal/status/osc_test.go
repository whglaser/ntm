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

// TestOSCDetectorDetectReportSkipsTheFallback pins the capture-free overlay
// the wait/activity surfaces use: DetectReport reads the report directly and
// never consults (or captures through) the fallback detector.
func TestOSCDetectorDetectReportSkipsTheFallback(t *testing.T) {
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", State: programstatus.StateDone, App: "pi", ObservedAt: at.Add(-90 * time.Second)},
	}}
	// The fallback refuses every call: if DetectReport touched it, this test
	// would observe the error or a scrape state.
	fallback := &fakeStatusDetector{err: errors.New("fallback must not be consulted")}
	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	atClock(detector, at)

	report, ok := detector.DetectReport("%0")
	if !ok {
		t.Fatal("fresh report was not returned")
	}
	if report.State != StateIdle || report.StateSource != StateSourceOSC {
		t.Fatalf("report = %+v, want idle/osc", report)
	}
	if report.ReportAgeSeconds != 90 {
		t.Fatalf("report age = %v, want 90s", report.ReportAgeSeconds)
	}
	if !report.UpdatedAt.Equal(at.Add(-90 * time.Second)) {
		t.Fatalf("UpdatedAt = %v, want the report's own time", report.UpdatedAt)
	}
}

func TestOSCDetectorDetectReportHonorsTTLAndUnknownStates(t *testing.T) {
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", State: programstatus.StateWorking, ObservedAt: at.Add(-2 * time.Minute)},
		"%1": {PaneID: "%1", State: programstatus.StateClear, ObservedAt: at},
		"%2": {PaneID: "%2", Query: true, ObservedAt: at},
	}}
	fallback := &fakeStatusDetector{err: errors.New("fallback must not be consulted")}
	detector := NewOSCDetector(fallback, source, time.Minute)
	atClock(detector, at)

	if _, ok := detector.DetectReport("%0"); ok {
		t.Fatal("expired report was returned")
	}
	if _, ok := detector.DetectReport("%1"); ok {
		t.Fatal("clear report was returned as a state")
	}
	if _, ok := detector.DetectReport("%2"); ok {
		t.Fatal("query was returned as a state")
	}
	if _, ok := detector.DetectReport(""); ok {
		t.Fatal("empty pane was returned as a state")
	}
}

// TestOSCDetectorReportAgeOnDetectAndDetectAll pins that the deciding report's
// age reaches every session-wide consumer, so live vs stale is auditable
// (ntm-l6cuz.7).
func TestOSCDetectorReportAgeOnDetectAndDetectAll(t *testing.T) {
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	source := &fakeProgramStatusSource{events: map[string]programstatus.Event{
		"%0": {PaneID: "%0", State: programstatus.StateWorking, ObservedAt: at.Add(-30 * time.Second)},
		"%1": {PaneID: "%1", State: programstatus.StateDone, ObservedAt: at.Add(-5 * time.Second)},
	}}
	fallback := &fakeStatusDetector{
		statuses: map[string]AgentStatus{
			"%0": {PaneID: "%0", State: StateIdle},
			"%1": {PaneID: "%1", State: StateWorking},
		},
		all: []AgentStatus{
			{PaneID: "%0", State: StateIdle},
			{PaneID: "%1", State: StateWorking},
		},
	}
	detector := NewOSCDetector(fallback, source, DefaultOSCStatusTTL)
	atClock(detector, at)

	got, err := detector.Detect("%0")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.ReportAgeSeconds != 30 || got.StateSource != StateSourceOSC {
		t.Fatalf("Detect age/source = %v/%q, want 30/osc", got.ReportAgeSeconds, got.StateSource)
	}

	all, err := detector.DetectAll("demo")
	if err != nil {
		t.Fatalf("DetectAll: %v", err)
	}
	ages := map[string]float64{}
	for _, status := range all {
		ages[status.PaneID] = status.ReportAgeSeconds
	}
	if ages["%0"] != 30 || ages["%1"] != 5 {
		t.Fatalf("DetectAll ages = %v, want %%0=30 %%1=5", ages)
	}
}
