package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/pipeline"
)

// TestPipelineRecoverListsOrphanedTurns exercises the recover command surface
// for ntm-l6cuz.8: a failed step whose declared artifact landed after the stop
// is reported as an orphaned turn, even when its pane is no longer reachable.
func TestPipelineRecoverListsOrphanedTurns(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ntm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ntm", "config.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	started := time.Date(2026, time.October, 28, 16, 49, 0, 0, time.UTC)
	finished := started.Add(7 * time.Minute)
	artifactPath := filepath.Join(dir, "evidence", "demo", "tool-ox.md")
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

	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflowYAML := `schema_version: "2.0"
name: orphan-cli-fixture
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

	const runID = "run-20261028-162735-39f3"
	state := &pipeline.ExecutionState{
		RunID: runID, WorkflowID: "orphan-cli-fixture", WorkflowFile: workflowPath, Session: "orphan-cli-session",
		Status: pipeline.StatusFailed, StartedAt: started, UpdatedAt: finished, FinishedAt: finished,
		Steps: map[string]pipeline.StepResult{
			"evidence_lanes_ox": {StepID: "evidence_lanes_ox", Status: pipeline.StatusFailed, StartedAt: started, FinishedAt: finished},
		},
		Variables: map[string]interface{}{"review_slug": "demo"},
		AgentDeliveries: map[string]pipeline.AgentDeliveryState{
			"evidence_lanes_ox": {
				// Values mirror the persisted v1 receipt encoding; the CLI test
				// intentionally exercises the same bytes a live run writes.
				Version: 1, StepID: "evidence_lanes_ox", Kind: "prompt",
				Session: "orphan-cli-session", Endpoint: "local", PaneID: "%7", PanePID: 4242, AgentType: "pi",
				StepHash: strings.Repeat("a", 64), PromptHash: strings.Repeat("b", 64),
				PromptEchoHash: strings.Repeat("c", 64), PromptEchoLength: 42,
				Status: "delivered", BeforeOutput: "old frame", Output: "old frame\nlane started",
				StartedAt: started, DeliveredAt: started.Add(time.Second),
			},
		},
	}
	if err := pipeline.SaveState(dir, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	cmd := newPipelineRecoverCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{runID})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("pipeline recover: %v\n%s", err, out.String())
	}
	var payload struct {
		Success       bool                    `json:"success"`
		OrphanedTurns []pipeline.OrphanedTurn `json:"orphaned_turns"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode recover output: %v\n%s", err, out.String())
	}
	if !payload.Success || len(payload.OrphanedTurns) != 1 {
		t.Fatalf("recover output = %s, want one orphaned turn", out.String())
	}
	turn := payload.OrphanedTurns[0]
	if turn.Class != pipeline.OrphanedTurnArtifactLanded || turn.StepID != "evidence_lanes_ox" || len(turn.Artifacts) != 1 {
		t.Fatalf("orphaned turn = %+v, want artifact_after_failure with provenance", turn)
	}
	if !strings.HasSuffix(turn.Artifacts[0].Path, filepath.Join("evidence", "demo", "tool-ox.md")) {
		t.Fatalf("artifact path = %q, want the declared lane note", turn.Artifacts[0].Path)
	}

	// The human surface reports the same turn and the available actions.
	jsonOutput = false
	human := newPipelineRecoverCmd()
	var text bytes.Buffer
	human.SetOut(&text)
	human.SetErr(&text)
	human.SetArgs([]string{runID})
	if err := human.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("pipeline recover (human): %v\n%s", err, text.String())
	}
	for _, want := range []string{"Orphaned turns:", "evidence_lanes_ox", pipeline.OrphanedTurnArtifactLanded, "tool-ox.md", "--adopt-orphan", "--stop-orphan"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("human recover output missing %q:\n%s", want, text.String())
		}
	}
}
