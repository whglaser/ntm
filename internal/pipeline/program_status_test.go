package pipeline

import (
	"context"
	"os"
	"path/filepath"
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

// TestAgentDeliveryCompletesOnStableArtifactsWithoutPiFrame is the
// ntm-l6cuz.11 acceptance for the live failure: the declared deliverable is
// written and byte-stable while the pane frame is unattributable (idle, the
// pre-send boundary scrolled away). The wait must park on the lost boundary,
// complete on the artifact set instead of demanding another attributed pi
// frame, and never re-send the prompt.
func TestAgentDeliveryCompletesOnStableArtifactsWithoutPiFrame(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-stable-artifacts")
	executor.config.ProjectDir = t.TempDir()
	executor.config.OutputStabilityWindow = 100 * time.Millisecond
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(executor.config.ProjectDir, "lane-note.md")
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		if err := os.WriteFile(artifact, []byte("lane note complete\n"), 0o600); err != nil {
			return err
		}
		// The turn repaints away the pre-send boundary; the frame is never
		// attributable from here on.
		return mock.SetPaneOutput(target, piStagePane("Pi v1.3.0\n\n"+strings.Repeat("lane output line\n", 400)+"unattributed", ""))
	})

	workflow := singleStepWorkflow("pi-stable-artifacts", Step{
		ID:      "work",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 10 * time.Second},
		Outputs: []OutputDecl{{Name: "lane-note", Path: artifact}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	state, err := executor.Run(ctx, workflow, nil, nil)
	if err != nil || state.Status != StatusCompleted {
		t.Fatalf("stable declared artifacts did not complete the step: state=%+v err=%v", state.Status, err)
	}
	if got := state.Steps["work"]; got.Status != StatusCompleted {
		t.Fatalf("step status = %q, want completed", got.Status)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("artifact disappeared: %v", err)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryMissingArtifactsStillFailClosedOnBoundaryLoss pins that the
// parking rule did not weaken the receipt: a lost boundary while idle with no
// declared deliverable on disk still fails closed and never re-sends.
func TestAgentDeliveryMissingArtifactsStillFailClosedOnBoundaryLoss(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-missing-artifacts")
	executor.config.ProjectDir = t.TempDir()
	executor.config.OutputStabilityWindow = 100 * time.Millisecond
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(executor.config.ProjectDir, "never-written.md")
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		return mock.SetPaneOutput(target, piStagePane("Pi v1.3.0\n\n"+strings.Repeat("lane output line\n", 400)+"unattributed", ""))
	})

	workflow := singleStepWorkflow("pi-missing-artifacts", Step{
		ID:      "work",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 5 * time.Second},
		Outputs: []OutputDecl{{Name: "lane-note", Path: artifact}},
	})
	state, err := executor.Run(context.Background(), workflow, nil, nil)
	if err == nil || state.Status == StatusCompleted {
		t.Fatalf("missing deliverable completed on an unattributable frame: state=%+v err=%v", state.Status, err)
	}
	got := state.Steps["work"]
	if got.Error == nil || !strings.Contains(got.Error.Message, "boundary is no longer visible") {
		t.Fatalf("missing deliverable did not fail closed on the boundary error: %+v", got.Error)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryStableArtifactsOutrankIncompletePostWaitFrame pins the
// second discard path from the live run: waitForIdle can return on the
// artifact window while the required post-wait pi frame is an incomplete
// capture. The stable deliverable is terminal; the step must complete without
// re-entering the wait (whose debounce plus re-baselined watcher used to
// consume the rest of the deadline).
func TestAgentDeliveryStableArtifactsOutrankIncompletePostWaitFrame(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-artifacts-over-frame")
	executor.config.ProjectDir = t.TempDir()
	executor.config.OutputStabilityWindow = 100 * time.Millisecond
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(executor.config.ProjectDir, "lane-note.md")
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		if err := os.WriteFile(artifact, []byte("lane note complete\n"), 0o600); err != nil {
			return err
		}
		// A bare transcript with no pi chrome: AgentResponseTranscript reports
		// the capture incomplete, so collect() is (false, nil) every poll.
		return mock.SetPaneOutput(target, "lane output line\n")
	})

	workflow := singleStepWorkflow("pi-artifacts-over-frame", Step{
		ID:      "work",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 4 * time.Second},
		Outputs: []OutputDecl{{Name: "lane-note", Path: artifact}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	state, err := executor.Run(ctx, workflow, nil, nil)
	if err != nil || state.Status != StatusCompleted {
		t.Fatalf("stable artifacts did not outrank the incomplete post-wait frame: state=%+v err=%v", state.Status, err)
	}
	assertNotResent(t, mock)
}

// providerRejectionTranscript reconstructs the live ntm-l6cuz.10 frame: the
// provider payload arrived after a long turn had already scrolled the pre-send
// boundary away, and the pane width split the JSON mid-token.
func providerRejectionTranscript() string {
	payload := `Error: 400 data: {"error":{"code":"content_filter","message":"Response content blocked by label 'Jailbreak'."}}`
	var wrapped strings.Builder
	for len(payload) > 24 {
		wrapped.WriteString(payload[:24])
		wrapped.WriteString("\n")
		payload = payload[24:]
	}
	wrapped.WriteString(payload)
	return "Pi v1.3.0\n\n" + strings.Repeat("lane output line\n", 400) + wrapped.String() + "\nIf this looks like a pi bug, /bug sends a report to the developers."
}

// TestAgentDeliverySurfacesProviderContentFilterInsteadOfBoundaryLoss is the
// ntm-l6cuz.10 acceptance: the turn ended on the provider's content-filter
// error, so the step must carry the provider's code/label instead of the
// misleading boundary-loss wording, keep the pane evidence, and never re-send.
func TestAgentDeliverySurfacesProviderContentFilterInsteadOfBoundaryLoss(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-provider-filter")
	executor.config.ProjectDir = t.TempDir()
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		return mock.SetPaneOutput(target, piStagePane(providerRejectionTranscript(), ""))
	})

	workflow := singleStepWorkflow("pi-provider-filter", Step{
		ID:      "intake",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 10 * time.Second},
	})
	state, err := executor.Run(context.Background(), workflow, nil, nil)
	if err == nil || state.Status != StatusFailed {
		t.Fatalf("provider rejection did not fail the step: state=%+v err=%v", state.Status, err)
	}
	got := state.Steps["intake"]
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 — the default fail policy must not blind-retry a provider rejection", got.Attempts)
	}
	if got.Error == nil || got.Error.Type != "provider_rejected" {
		t.Fatalf("step error = %+v, want provider_rejected", got.Error)
	}
	if !strings.Contains(got.Error.Message, "content_filter") || !strings.Contains(got.Error.Message, "Jailbreak") {
		t.Fatalf("provider code/label lost from the step error: %q", got.Error.Message)
	}
	if strings.Contains(got.Error.Message, "boundary is no longer visible") {
		t.Fatalf("provider rejection still reported as boundary loss: %q", got.Error.Message)
	}
	if got.Error.AgentState != string(status.StateIdle) {
		t.Fatalf("agent state at rejection = %q, want idle", got.Error.AgentState)
	}
	if !strings.Contains(got.Error.PaneOutput, "Error: 400 data:") || !strings.Contains(got.Error.PaneOutput, "Jailbreak") {
		t.Fatalf("provider payload missing from the step's pane evidence: %q", got.Error.PaneOutput)
	}
	if strings.Contains(got.Output, "content_filter") || strings.Contains(got.Output, "Jailbreak") {
		t.Fatalf("error frame was attributed as the step's answer: %q", got.Output)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryProviderRejectionInFreshFrameIsNotAnAnswer pins the case
// the ordinary attribution rules would accept: the boundary is still visible,
// so the error block lands in the fresh delta after the prompt echo and looks
// like a completed response. The provider verdict must win over completion.
func TestAgentDeliveryProviderRejectionInFreshFrameIsNotAnAnswer(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-provider-fresh")
	executor.config.ProjectDir = t.TempDir()
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context.", "")); err != nil {
		t.Fatal(err)
	}
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		return mock.SetPaneOutput(target, piStagePane("Pi v1.3.0\nPrevious turn context.\n\nRun the lane.\n\n"+providerRejectionTranscript(), ""))
	})

	workflow := singleStepWorkflow("pi-provider-fresh", Step{
		ID:      "intake",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 10 * time.Second},
	})
	state, err := executor.Run(context.Background(), workflow, nil, nil)
	if err == nil || state.Status != StatusFailed {
		t.Fatalf("fresh-frame provider rejection did not fail the step: state=%+v err=%v", state.Status, err)
	}
	got := state.Steps["intake"]
	if got.Error == nil || got.Error.Type != "provider_rejected" {
		t.Fatalf("step error = %+v, want provider_rejected", got.Error)
	}
	if strings.Contains(got.Output, "content_filter") || strings.Contains(got.Output, "Jailbreak") {
		t.Fatalf("error frame was attributed as the step's answer: %q", got.Output)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryProviderRejectionWithStableArtifactsStillCompletes pins the
// ntm-l6cuz.10 exception: when every declared deliverable is already on disk,
// the artifact-stability fallback outranks the error frame instead of failing a
// step whose output exists (ntm-l6cuz.11 semantics preserved).
func TestAgentDeliveryProviderRejectionWithStableArtifactsStillCompletes(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-provider-artifacts")
	executor.config.ProjectDir = t.TempDir()
	executor.config.OutputStabilityWindow = 1500 * time.Millisecond
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(executor.config.ProjectDir, "lane-note.md")
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		if err := os.WriteFile(artifact, []byte("lane note complete\n"), 0o600); err != nil {
			return err
		}
		return mock.SetPaneOutput(target, piStagePane(providerRejectionTranscript(), ""))
	})

	workflow := singleStepWorkflow("pi-provider-artifacts", Step{
		ID:      "intake",
		Pane:    PaneSpec{Index: 1},
		Prompt:  "Run the lane.",
		Wait:    WaitCompletion,
		Timeout: Duration{Duration: 10 * time.Second},
		Outputs: []OutputDecl{{Name: "lane-note", Path: artifact}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	state, err := executor.Run(ctx, workflow, nil, nil)
	if err != nil || state.Status != StatusCompleted {
		t.Fatalf("declared deliverable did not outrank the provider rejection: state=%+v err=%v", state.Status, err)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("artifact disappeared: %v", err)
	}
	assertNotResent(t, mock)
}

// TestAgentDeliveryRecordsProviderRejectionAttempts pins the retry half of the
// contract: when the workflow policy retries, every rejected attempt is kept in
// the run state (not only the last one), the final step carries the provider
// verdict, and retries still never re-send the delivered prompt.
func TestAgentDeliveryRecordsProviderRejectionAttempts(t *testing.T) {
	executor, mock := submittingExecutor(t, "pi-provider-retry")
	executor.config.ProjectDir = t.TempDir()
	mock.AddPane("", tmux.Pane{ID: "%1", Index: 1, PID: 4141, Type: tmux.AgentType("pi"), Width: 100})
	if err := mock.SetPaneOutput("%1", piStagePane("Pi v1.3.0\nPrevious turn context that will scroll away.", "")); err != nil {
		t.Fatal(err)
	}
	mock.SetSubmissionVerifier(func(target, _, _ string) error {
		return mock.SetPaneOutput(target, piStagePane(providerRejectionTranscript(), ""))
	})

	workflow := singleStepWorkflow("pi-provider-retry", Step{
		ID:           "intake",
		Pane:         PaneSpec{Index: 1},
		Prompt:       "Run the lane.",
		Wait:         WaitCompletion,
		Timeout:      Duration{Duration: 5 * time.Second},
		OnError:      ErrorActionRetry,
		RetryCount:   2,
		RetryDelay:   Duration{Duration: 10 * time.Millisecond},
		RetryBackoff: "none",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	state, err := executor.Run(ctx, workflow, nil, nil)
	if err == nil || state.Status != StatusFailed {
		t.Fatalf("retried provider rejection did not fail the run: state=%+v err=%v", state.Status, err)
	}
	got := state.Steps["intake"]
	if got.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", got.Attempts)
	}
	if got.Error == nil || got.Error.Type != "provider_rejected" {
		t.Fatalf("final step error = %+v, want provider_rejected", got.Error)
	}
	var recorded []string
	for _, execErr := range state.Errors {
		if execErr.Type == "provider_rejected" {
			recorded = append(recorded, execErr.Message)
		}
	}
	if len(recorded) != 2 {
		t.Fatalf("recorded provider attempts = %d (%v), want 2", len(recorded), recorded)
	}
	for i, want := range []string{"attempt 1/3", "attempt 2/3"} {
		if !strings.Contains(recorded[i], want) {
			t.Fatalf("attempt record %d = %q, want it to mention %q", i, recorded[i], want)
		}
	}
	assertNotResent(t, mock)
}
