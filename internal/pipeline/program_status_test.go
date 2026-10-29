package pipeline

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/programstatus"
	"github.com/Dicklesworthstone/ntm/internal/status"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// fakeOSCSource is a deterministic OSC 7501 emitter: the test decides exactly
// when the pane reports idle/working, independent of any live tmux server.
type fakeOSCSource struct {
	mu     sync.Mutex
	events map[string]programstatus.Event
	calls  int
}

func newFakeOSCSource() *fakeOSCSource {
	return &fakeOSCSource{events: make(map[string]programstatus.Event)}
}

func (f *fakeOSCSource) Latest(paneID string) (programstatus.Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	event, ok := f.events[paneID]
	return event, ok
}

func (f *fakeOSCSource) emit(paneID string, state programstatus.State) {
	f.mu.Lock()
	f.events[paneID] = programstatus.Event{
		PaneID:     paneID,
		State:      state,
		App:        "pi",
		ObservedAt: time.Now(),
	}
	f.mu.Unlock()
}

func (f *fakeOSCSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestAdHocStageCompletesOnOSCReportWhenScrapeNeverIdles is the behavioral
// acceptance for the authoritative-status path: the scrape detector never
// reports idle (the observed idle-pane failure), and the step completes anyway
// because pi's own "done" report arrived. Without OSC this run would block
// until its deadline.
func TestAdHocStageCompletesOnOSCReportWhenScrapeNeverIdles(t *testing.T) {
	executor, client, _ := newStageDeliveryFixture(t)
	client.pane.Type = tmux.AgentType("pi")
	client.pane.Command = "pi"
	// Upstream's pi delivery gate requires a complete, recognizable pi frame
	// on every capture; a bare transcript is rejected as a partial repaint.
	client.before = piStagePane("Pi v1.3.0\nExisting transcript.", "")
	client.after = piStagePane("Pi v1.3.0\n\nrun the task\n\nStage answer.", "")

	executor.SetDetector(alwaysWorkingDetector{})
	source := newFakeOSCSource()
	executor.SetProgramStatusSource(source)

	client.prepare = func(_ context.Context, call int) error {
		if call == 2 {
			// The final pre-paste gate: after this the wait begins. pi emits
			// state=done when the turn settles (agent_settled).
			source.emit(client.pane.ID, programstatus.StateDone)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	started := time.Now()
	if _, err := executor.executeAdHocStage(ctx, Stage{AgentType: "pi", Prompt: "run the task"}, client.pane.ID, "", AgentDeliveryState{}); err != nil {
		t.Fatalf("stage did not complete on the OSC report: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("stage took %v; the OSC idle streak never decided it", elapsed)
	}
	if source.callCount() == 0 {
		t.Fatal("detector never consulted the OSC source")
	}
}

// resetSpyDetector records per-step classification resets while behaving
// like the idle stage detector.
type resetSpyDetector struct {
	stageIdleDetector
	resets []string
}

func (d *resetSpyDetector) ResetPane(paneID string) {
	d.resets = append(d.resets, paneID)
}

// TestAdHocStageProgramStatusCaptureSkipsInjectedClients pins the test
// isolation contract: a mock tmux client must never cause a real pipe-pane
// tap against the host server. It also proves the per-step classification
// reset reaches the detector through the optional interface.
func TestAdHocStageProgramStatusCaptureSkipsInjectedClients(t *testing.T) {
	executor, client, _ := newStageDeliveryFixture(t)
	detector := &resetSpyDetector{}
	executor.SetDetector(detector)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := executor.executeAdHocStage(ctx, Stage{AgentType: "cc", Prompt: "task"}, client.pane.ID, "", AgentDeliveryState{}); err != nil {
		t.Fatalf("stage failed: %v", err)
	}

	executor.programStatusMu.Lock()
	streams := executor.programStatusStreams
	executor.programStatusMu.Unlock()
	if streams != nil {
		t.Fatal("injected tmux client started a real OSC capture")
	}
	if len(detector.resets) != 1 || detector.resets[0] != client.pane.ID {
		t.Fatalf("per-step resets = %v, want [%s]", detector.resets, client.pane.ID)
	}
}

// TestProgramStatusStateNeverCompletesOnBlocked pins the mapping choice that
// matters for pipelines: a pane waiting on a dialog is not available work.
func TestProgramStatusStateNeverCompletesOnBlocked(t *testing.T) {
	state, _, known := status.ProgramStatusState(programstatus.Event{State: programstatus.StateBlocked})
	if !known || state != status.StateWorking {
		t.Fatalf("blocked mapped to %q (known=%v), want working", state, known)
	}
}

// TestAgentDeliveryParksScrolledBoundaryWhileWorkingThenCompletes is the
// ntm-l6cuz.9 acceptance for the working half: the pre-send boundary scrolls
// away while the pane reports working, the wait parks without failing or
// re-sending, and a later OSC settle attributes the current frame.
func TestAgentDeliveryParksScrolledBoundaryWhileWorkingThenCompletes(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-park-working")
	executor.config.ProjectDir = t.TempDir()
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	source := newFakeOSCSource()
	detector := &stageIdleDetector{} // scrape never idles; only OSC decides
	executor.SetDetector(detector)
	executor.SetProgramStatusSource(source)

	const answer = "Lane note complete."
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		source.emit(target, programstatus.StateWorking)
		return mock.SetPaneOutput(target, piStagePane("Pi v1.3.0\n\n"+strings.Repeat("lane output line\n", 400)+answer, ""))
	})

	// Detect calls: (1) the first post-dispatch poll sees the lost boundary
	// while the pane reports working and must park; (2) waitForIdle reads the
	// same working state; (3) the next poll's collect, where the settle lands.
	// The pre-parking code failed at (1), so reaching (3) and completing proves
	// the working poll parked instead of failing the step.
	detector.onIdle = func(call int) {
		if call == 3 {
			source.emit("%1", programstatus.StateDone)
		}
	}

	workflow := singleStepWorkflow("pi-park-working", Step{
		ID:      "work",
		Pane:    PaneSpec{Index: 1},
		Prompt:  strings.Repeat("Run the lane and report every artifact. ", 90),
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 20 * time.Second},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	state, err := executor.Run(ctx, workflow, nil, nil)
	if err != nil || state.Status != StatusCompleted {
		t.Fatalf("parked turn did not complete on the later OSC report: state=%+v err=%v", state.Status, err)
	}
	if got := state.Steps["work"].Output; !strings.Contains(got, answer) {
		t.Fatalf("recovered turn lost the current frame's output: %q", got)
	}
	if detector.calls < 4 {
		t.Fatalf("detector calls=%d; the settle landed before the parked poll", detector.calls)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryParksScrolledBoundaryUntilStepTimeout pins the backstop: a
// boundary lost while the pane reports working does not fail immediately and
// is never re-sent, but a turn that never settles still ends at the step
// timeout. The OSC source deciding the failure evidence also confirms the tap
// stayed authoritative through the wait.
func TestAgentDeliveryParksScrolledBoundaryUntilStepTimeout(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-park-timeout")
	executor.config.ProjectDir = t.TempDir()
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	source := newFakeOSCSource()
	executor.SetDetector(&stageIdleDetector{})
	executor.SetProgramStatusSource(source)
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		source.emit(target, programstatus.StateWorking)
		return mock.SetPaneOutput(target, piStagePane("Pi v1.3.0\n\n"+strings.Repeat("lane output line\n", 400)+"still working", ""))
	})

	workflow := singleStepWorkflow("pi-park-timeout", Step{
		ID:      "work",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 5 * time.Second},
	})
	state, err := executor.Run(context.Background(), workflow, nil, nil)
	if err == nil || state.Status != StatusFailed {
		t.Fatalf("never-settling working turn did not end at its timeout: state=%+v err=%v", state.Status, err)
	}
	got := state.Steps["work"]
	if got.Error == nil || got.Error.Type != "timeout" || !strings.Contains(got.Error.Message, "timeout after") {
		t.Fatalf("step error = %+v, want a step-timeout failure", got.Error)
	}
	if strings.Contains(got.Error.Message, "boundary is no longer visible") {
		t.Fatalf("working boundary loss was still treated as a fatal receipt error: %q", got.Error.Message)
	}
	if got.Error.AgentStateSource != string(status.StateSourceOSC) {
		t.Fatalf("timeout evidence source = %q, want osc", got.Error.AgentStateSource)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryScrolledBoundaryWhileIdleStillFailsClosed pins the other
// half of ntm-l6cuz.9: an idle reading without an authoritative OSC settle is
// not attribution evidence, so the receipt still fails closed and the prompt
// is never re-sent.
func TestAgentDeliveryScrolledBoundaryWhileIdleStillFailsClosed(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-idle-boundary")
	executor.config.ProjectDir = t.TempDir()
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		return mock.SetPaneOutput(target, piStagePane("Pi v1.3.0\n\n"+strings.Repeat("lane output line\n", 400)+"unattributed", ""))
	})

	workflow := singleStepWorkflow("pi-idle-boundary", Step{
		ID:      "work",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 10 * time.Second},
	})
	state, err := executor.Run(context.Background(), workflow, nil, nil)
	if err == nil || state.Status == StatusCompleted || state.AgentDeliveries["work"].Status != agentDeliveryDelivered {
		t.Fatalf("idle boundary loss changed the receipt: state=%+v err=%v", state.Status, err)
	}
	got := state.Steps["work"]
	if got.Error == nil || !strings.Contains(got.Error.Message, "boundary is no longer visible") {
		t.Fatalf("idle unattributable boundary did not fail closed: %+v", got.Error)
	}
	assertNotResent(t, mock)
}

// TestNewTurnForgetsProgramStatusButResumeKeepsIt pins the ntm-l6cuz.9
// evidence-retention fix: a fresh dispatch drops the pane's previous report so
// a stale "done" cannot complete a new step, while resuming the same recorded
// delivery keeps the report. pi emits only state changes, so a report dropped
// at retry time would never be re-emitted.
func TestNewTurnForgetsProgramStatusButResumeKeepsIt(t *testing.T) {
	cfg := DefaultExecutorConfig("program-status-turn")
	executor := NewExecutor(cfg)
	executor.SetTmuxClient(NewMockTmuxClient())
	observer := programstatus.NewObserver()
	executor.programStatusObserver = observer

	for _, tc := range []struct {
		name      string
		beginTurn bool
		wantKept  bool
	}{
		{"new turn", true, false},
		{"recorded resume", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer.Observe("%1", []byte("\x1b]7501;state=done\x1b\\"))
			if _, ok := observer.Latest("%1"); !ok {
				t.Fatal("fixture report was not recorded")
			}
			executor.ensureProgramStatusCapture("%1", tc.beginTurn)
			_, kept := observer.Latest("%1")
			if kept != tc.wantKept {
				t.Fatalf("report kept=%v, want %v", kept, tc.wantKept)
			}
		})
	}
}
