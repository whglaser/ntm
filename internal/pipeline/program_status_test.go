package pipeline

import (
	"context"
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
