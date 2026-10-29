package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/status"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// orphanStateDetector is a fixed detector for orphaned-turn tests.
type orphanStateDetector struct {
	state    status.AgentState
	source   status.StateSource
	lastSeen time.Time
}

func (d orphanStateDetector) Detect(paneID string) (status.AgentStatus, error) {
	return status.AgentStatus{
		PaneID: paneID, State: d.state, StateSource: d.source,
		LastActive: d.lastSeen, UpdatedAt: time.Now().UTC(),
	}, nil
}

func (d orphanStateDetector) DetectAll(string) ([]status.AgentStatus, error) { return nil, nil }

// orphanInterrupts reads the mock's recorded interrupts without exporting a
// test-only accessor that would be dead in production builds.
func orphanInterrupts(mock *MockTmuxClient, target string) []MockTmuxInterrupt {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	var history []MockTmuxInterrupt
	for _, call := range mock.interrupts {
		if call.Target == target {
			history = append(history, call)
		}
	}
	return history
}

// orphanedTurnFixture builds a failed pi step whose pane is still present and
// whose declared lane note was written after the step stopped being observed.
func orphanedTurnFixture(t *testing.T) (*Executor, *MockTmuxClient, string, string) {
	t.Helper()
	projectDir := t.TempDir()
	runID := "run-20261028-162735-39f3"
	session := "orphan-session"
	started := time.Date(2026, time.October, 28, 16, 49, 0, 0, time.UTC)
	finished := started.Add(7 * time.Minute)

	artifactPath := filepath.Join(projectDir, "evidence", "demo", "tool-ox.md")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("lane note complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written := finished.Add(20 * time.Minute)
	if err := os.Chtimes(artifactPath, written, written); err != nil {
		t.Fatal(err)
	}

	workflowPath := filepath.Join(projectDir, "workflow.yaml")
	workflowYAML := `schema_version: "2.0"
name: orphan-fixture
vars:
  review_slug:
    required: true
    type: string
steps:
  - id: evidence_lanes_ox
    outputs:
      - name: lane_note
        path: evidence/${vars.review_slug}/tool-ox.md
    prompt: run the lane
`
	if err := os.WriteFile(workflowPath, []byte(workflowYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	delivery := AgentDeliveryState{
		Version: agentDeliveryVersion, StepID: "evidence_lanes_ox", Kind: StepKindPrompt,
		Session: session, Endpoint: "local", PaneID: "%7", PanePID: 4242, AgentType: "pi",
		StepHash: strings.Repeat("a", 64), PromptHash: strings.Repeat("b", 64),
		PromptEchoHash: strings.Repeat("c", 64), PromptEchoLength: 42,
		Status: agentDeliveryDelivered, BeforeOutput: "old frame", Output: "old frame\nlane started",
		StartedAt: started, DeliveredAt: started.Add(time.Second),
	}
	state := &ExecutionState{
		RunID: runID, WorkflowID: "orphan-fixture", WorkflowFile: workflowPath, Session: session,
		Status: StatusFailed, StartedAt: started, UpdatedAt: finished, FinishedAt: finished,
		Steps: map[string]StepResult{
			"evidence_lanes_ox": {
				StepID: "evidence_lanes_ox", Status: StatusFailed,
				StartedAt: started, FinishedAt: finished,
			},
		},
		Variables:       map[string]interface{}{"review_slug": "demo"},
		AgentDeliveries: map[string]AgentDeliveryState{"evidence_lanes_ox": delivery},
	}
	if err := SaveState(projectDir, state); err != nil {
		t.Fatalf("save fixture state: %v", err)
	}

	cfg := DefaultExecutorConfig(session)
	cfg.ProjectDir = projectDir
	cfg.RunID = runID
	e := NewExecutor(cfg)
	mock := NewMockTmuxClient()
	mock.AddPane(session, tmux.Pane{ID: "%7", Index: 7, PID: 4242, Type: tmux.AgentType("pi"), Width: 120})
	if err := mock.SetPaneOutput("%7", piStagePane("Pi v1.3.0\n\nold frame\nlane started", "")); err != nil {
		t.Fatal(err)
	}
	e.SetTmuxClient(mock)
	return e, mock, projectDir, runID
}

// TestInspectOrphanedTurnsDistinguishesGeneratingFromArtifacts covers the
// ntm-l6cuz.8 acceptance: recover inspection separates a pane that is still
// generating from one whose declared artifact landed after the step stopped.
func TestInspectOrphanedTurnsDistinguishesGeneratingFromArtifacts(t *testing.T) {
	e, mock, _, _ := orphanedTurnFixture(t)
	e.SetDetector(orphanStateDetector{state: status.StateWorking, source: status.StateSourceOSC, lastSeen: time.Now().UTC()})

	inspection, err := e.InspectOrphanedTurns(context.Background())
	if err != nil {
		t.Fatalf("InspectOrphanedTurns: %v", err)
	}
	if len(inspection.Turns) != 1 {
		t.Fatalf("orphaned turns = %+v, want exactly one", inspection.Turns)
	}
	turn := inspection.Turns[0]
	if turn.Class != OrphanedTurnGenerating || turn.AgentState != string(status.StateWorking) || turn.StateSource != string(status.StateSourceOSC) {
		t.Fatalf("generating classification = %+v, want generating/working/osc", turn)
	}
	if !turn.PaneVerified || turn.PaneID != "%7" || turn.AgentType != "pi" {
		t.Fatalf("pane evidence = %+v, want verified pane %%7 pi", turn)
	}
	if len(turn.Artifacts) != 1 || !strings.HasSuffix(turn.Artifacts[0].Path, filepath.Join("evidence", "demo", "tool-ox.md")) || turn.Artifacts[0].ModTime.IsZero() {
		t.Fatalf("artifact provenance = %+v, want the post-failure lane note", turn.Artifacts)
	}

	// The same pane afterwards: generator stopped, output and artifact grew.
	e.SetDetector(orphanStateDetector{state: status.StateIdle, source: status.StateSourceScrape})
	if err := mock.SetPaneOutput("%7", piStagePane("Pi v1.3.0\n\nold frame\nlane started\nlane note complete", "")); err != nil {
		t.Fatal(err)
	}
	inspection, err = e.InspectOrphanedTurns(context.Background())
	if err != nil {
		t.Fatalf("InspectOrphanedTurns after settle: %v", err)
	}
	if len(inspection.Turns) != 1 {
		t.Fatalf("orphaned turns after settle = %+v, want one", inspection.Turns)
	}
	turn = inspection.Turns[0]
	if turn.Class != OrphanedTurnArtifactLanded {
		t.Fatalf("settled classification = %+v, want %s", turn, OrphanedTurnArtifactLanded)
	}
	if turn.NewOutputBytes <= 0 || len(turn.Artifacts) != 1 {
		t.Fatalf("settled evidence = %+v, want new output bytes and the artifact", turn)
	}
}

// TestInspectOrphanedTurnsIgnoresStoppedAndQuietSteps pins the negative side:
// completed steps and failed steps with no post-failure evidence stay out of
// the report.
func TestInspectOrphanedTurnsIgnoresStoppedAndQuietSteps(t *testing.T) {
	e, _, projectDir, runID := orphanedTurnFixture(t)
	e.SetDetector(orphanStateDetector{state: status.StateIdle, source: status.StateSourceScrape})
	if err := os.RemoveAll(filepath.Join(projectDir, "evidence")); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(projectDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	state.Steps["evidence_lanes_ox"] = StepResult{StepID: "evidence_lanes_ox", Status: StatusCompleted, StartedAt: state.StartedAt, FinishedAt: state.FinishedAt}
	if err := SaveState(projectDir, state); err != nil {
		t.Fatal(err)
	}
	inspection, err := e.InspectOrphanedTurns(context.Background())
	if err != nil {
		t.Fatalf("InspectOrphanedTurns: %v", err)
	}
	if len(inspection.Turns) != 0 {
		t.Fatalf("completed step reported as orphaned: %+v", inspection.Turns)
	}
}

// TestStopOrphanedTurnInterruptsVerifiedPaneAndRecordsDecision covers the stop
// path: one interrupt, no prompt resend, and an append-only decision in state.
func TestStopOrphanedTurnInterruptsVerifiedPaneAndRecordsDecision(t *testing.T) {
	e, mock, projectDir, runID := orphanedTurnFixture(t)
	e.SetDetector(orphanStateDetector{state: status.StateWorking, source: status.StateSourceOSC, lastSeen: time.Now().UTC()})
	const reason = "Operator: ox lane kept generating after evidence_lanes failed"

	result, err := e.decideOrphanedTurn(context.Background(), "evidence_lanes_ox", reason, OrphanedTurnActionStop)
	if err != nil {
		t.Fatalf("stop orphaned turn: %v", err)
	}
	if result.Replayed || result.Decision.Action != OrphanedTurnActionStop || result.Decision.Reason != reason {
		t.Fatalf("stop result = %+v, want a fresh stopped decision", result)
	}
	interrupts := orphanInterrupts(mock, "%7")
	if len(interrupts) != 1 || interrupts[0].AgentType != "pi" || interrupts[0].Target != "%7" {
		t.Fatalf("interrupt history = %+v, want exactly one pi interrupt on %%7", interrupts)
	}
	if history, err := mock.PasteHistory("%7"); err != nil || len(history) != 0 {
		t.Fatalf("stop pasted prompt bytes (history=%+v err=%v); stopping must never resend", history, err)
	}
	loaded, err := LoadState(projectDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.OrphanedTurnDecisions) != 1 {
		t.Fatalf("persisted decisions = %+v, want one", loaded.OrphanedTurnDecisions)
	}
	decision := loaded.OrphanedTurnDecisions[0]
	if decision.Action != OrphanedTurnActionStop || decision.PaneID != "%7" || decision.PanePID != 4242 || len(decision.Artifacts) != 1 {
		t.Fatalf("persisted stop decision = %+v, want pane identity and artifact provenance", decision)
	}

	replay, err := e.decideOrphanedTurn(context.Background(), "evidence_lanes_ox", reason, OrphanedTurnActionStop)
	if err != nil || !replay.Replayed {
		t.Fatalf("repeated stop = %+v, %v; want an idempotent replay", replay, err)
	}
	if _, err := e.decideOrphanedTurn(context.Background(), "evidence_lanes_ox", "different reason", OrphanedTurnActionStop); err == nil {
		t.Fatal("conflicting stop decision was accepted")
	}
	if _, err := e.decideOrphanedTurn(context.Background(), "evidence_lanes_ox", reason, OrphanedTurnActionAdopt); err == nil {
		t.Fatal("adoption after a stop decision was accepted")
	}
}

// TestAdoptOrphanedTurnRecordsArtifactsWithoutInterruptOrPaste covers the
// adoption path: provenance lands in the run state and nothing is dispatched.
func TestAdoptOrphanedTurnRecordsArtifactsWithoutInterruptOrPaste(t *testing.T) {
	e, mock, projectDir, runID := orphanedTurnFixture(t)
	e.SetDetector(orphanStateDetector{state: status.StateIdle, source: status.StateSourceScrape})
	const reason = "Operator: adopting the lane note written after the step failed"

	result, err := e.decideOrphanedTurn(context.Background(), "evidence_lanes_ox", reason, OrphanedTurnActionAdopt)
	if err != nil {
		t.Fatalf("adopt orphaned turn: %v", err)
	}
	if result.Replayed || result.Decision.Action != OrphanedTurnActionAdopt || len(result.Decision.Artifacts) != 1 {
		t.Fatalf("adopt result = %+v, want a fresh adopted decision with artifact provenance", result)
	}
	if interrupts := orphanInterrupts(mock, "%7"); len(interrupts) != 0 {
		t.Fatalf("adoption interrupted the pane: %+v", interrupts)
	}
	if history, err := mock.PasteHistory("%7"); err != nil || len(history) != 0 {
		t.Fatalf("adoption pasted prompt bytes (history=%+v err=%v); adoption must never resend", history, err)
	}
	loaded, err := LoadState(projectDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.OrphanedTurnDecisions) != 1 || loaded.OrphanedTurnDecisions[0].Action != OrphanedTurnActionAdopt {
		t.Fatalf("persisted adoption = %+v, want one adopted decision", loaded.OrphanedTurnDecisions)
	}
	if loaded.Steps["evidence_lanes_ox"].Status != StatusFailed {
		t.Fatalf("adoption rewrote the step outcome: %+v", loaded.Steps["evidence_lanes_ox"])
	}
}
