package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/status"
)

func writeArtifactFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write artifact %s: %v", path, err)
	}
}

func TestArtifactStabilityMissingNeverStable(t *testing.T) {
	watcher := newArtifactStability([]string{filepath.Join(t.TempDir(), "never.md")}, 10*time.Second)
	if watcher == nil {
		t.Fatal("watcher is nil for one real path")
	}
	start := time.Now()
	for i := 0; i <= 5; i++ {
		if watcher.satisfiedAt(start.Add(time.Duration(i) * 5 * time.Second)) {
			t.Fatal("missing artifact reported stable")
		}
	}
}

func TestArtifactStabilityRequiresFullWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.md")
	writeArtifactFile(t, path, "report body")
	watcher := newArtifactStability([]string{path}, 10*time.Second)

	t0 := time.Now()
	if watcher.satisfiedAt(t0) {
		t.Fatal("first observation must only establish the baseline")
	}
	if watcher.satisfiedAt(t0.Add(5 * time.Second)) {
		t.Fatal("artifact reported stable before the window elapsed")
	}
	if !watcher.satisfiedAt(t0.Add(10 * time.Second)) {
		t.Fatal("artifact not stable after the full window")
	}
}

func TestArtifactStabilityContentChangeResetsWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.md")
	writeArtifactFile(t, path, "v1")
	watcher := newArtifactStability([]string{path}, 10*time.Second)

	t0 := time.Now()
	if watcher.satisfiedAt(t0) {
		t.Fatal("baseline observation claimed stability")
	}
	writeArtifactFile(t, path, "v2")
	if watcher.satisfiedAt(t0.Add(5 * time.Second)) {
		t.Fatal("changed content was considered stable")
	}
	if watcher.satisfiedAt(t0.Add(12 * time.Second)) {
		t.Fatal("window did not reset from the content change")
	}
	if !watcher.satisfiedAt(t0.Add(15 * time.Second)) {
		t.Fatal("artifact not stable a full window after the change")
	}
}

func TestArtifactStabilityEmptyFileCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	writeArtifactFile(t, path, "")
	watcher := newArtifactStability([]string{path}, time.Second)

	t0 := time.Now()
	if watcher.satisfiedAt(t0) {
		t.Fatal("baseline observation claimed stability")
	}
	if !watcher.satisfiedAt(t0.Add(time.Second)) {
		t.Fatal("empty artifact never stabilized")
	}
}

func TestArtifactStabilityRemoveResetsWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.md")
	writeArtifactFile(t, path, "body")
	watcher := newArtifactStability([]string{path}, 10*time.Second)

	t0 := time.Now()
	_ = watcher.satisfiedAt(t0)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	if watcher.satisfiedAt(t0.Add(11 * time.Second)) {
		t.Fatal("removed artifact stayed stable")
	}
}

func TestNewArtifactStabilityDropsBlankAndDuplicatePaths(t *testing.T) {
	if watcher := newArtifactStability(nil, time.Second); watcher != nil {
		t.Fatal("nil paths should disable the watcher")
	}
	if watcher := newArtifactStability([]string{" ", "", "  "}, time.Second); watcher != nil {
		t.Fatal("blank paths should disable the watcher")
	}
	watcher := newArtifactStability([]string{"a", "a", " b "}, time.Second)
	if watcher == nil {
		t.Fatal("watcher is nil")
	}
	if len(watcher.paths) != 2 || watcher.paths[0] != "a" || watcher.paths[1] != "b" {
		t.Fatalf("dedupe/trim failed: %v", watcher.paths)
	}
}

// TestWaitIdleArtifactFallbackCompletesWithWorkingDetector is the acceptance
// shape for a long step whose pane never reads idle: the scrape detector
// reports working for every poll, and the step still completes because its
// declared output appeared and stopped changing.
func TestWaitIdleArtifactFallbackCompletesWithWorkingDetector(t *testing.T) {
	output := filepath.Join(t.TempDir(), "answer.md")
	cfg := DefaultExecutorConfig("test")
	cfg.ProgressInterval = MinProgressInterval
	e := NewExecutor(cfg)
	e.SetDetector(alwaysWorkingDetector{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create the declared output after the wait begins, so completion can
	// only come from the artifact watcher.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(output, []byte("final answer"), 0o600)
	}()

	opts := waitIdleOptions{Artifacts: newArtifactStability([]string{output}, 300*time.Millisecond)}
	started := time.Now()
	if err := e.waitForIdle(ctx, "%7", 8*time.Second, opts); err != nil {
		t.Fatalf("artifact fallback did not complete the step: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("artifact fallback took %v; expected the stability window, not the timeout", elapsed)
	}
}

// A declared output that never appears must never complete the wait, no
// matter how long the detector reports working.
func TestWaitIdleArtifactFallbackNeverCompletesWithoutOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "never.md")
	cfg := DefaultExecutorConfig("test")
	cfg.ProgressInterval = MinProgressInterval
	e := NewExecutor(cfg)
	e.SetDetector(alwaysWorkingDetector{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := waitIdleOptions{Artifacts: newArtifactStability([]string{output}, 200*time.Millisecond)}
	if err := e.waitForIdle(ctx, "%7", 2*time.Second, opts); err == nil {
		t.Fatal("a missing declared output completed the step")
	}
}

// TestWaitIdleStablePollsConfigurable pins the hysteresis knob: a detector
// that yields two idles before a working sample completes with StablePolls=2
// and never completes with StablePolls=3, because the streak resets.
func TestWaitIdleStablePollsConfigurable(t *testing.T) {
	newExecutor := func(t *testing.T) *Executor {
		t.Helper()
		cfg := DefaultExecutorConfig("test")
		cfg.ProgressInterval = MinProgressInterval
		e := NewExecutor(cfg)
		var calls int
		e.detector = &mockDetector{detectFunc: func(paneID string) (status.AgentStatus, error) {
			calls++
			if calls%4 == 1 || calls%4 == 2 {
				return status.AgentStatus{State: status.StateIdle, PaneID: paneID}, nil
			}
			return status.AgentStatus{State: status.StateWorking, PaneID: paneID}, nil
		}}
		return e
	}

	t.Run("streak met", func(t *testing.T) {
		e := newExecutor(t)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := e.waitForIdle(ctx, "%7", 6*time.Second, waitIdleOptions{StablePolls: 2}); err != nil {
			t.Fatalf("StablePolls=2 did not complete on two consecutive idles: %v", err)
		}
	})

	t.Run("streak reset", func(t *testing.T) {
		e := newExecutor(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.waitForIdle(ctx, "%7", 3*time.Second, waitIdleOptions{StablePolls: 3}); err == nil {
			t.Fatal("StablePolls=3 completed despite the idle streak resetting")
		}
	})
}

// TestWaitIdleOptionsForStepResolvesDeclaredOutputs proves step outputs are
// variable-substituted and empty declarations disable the watcher.
func TestWaitIdleOptionsForStepResolvesDeclaredOutputs(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig("test"))
	e.varMu.Lock()
	e.state = &ExecutionState{Variables: map[string]interface{}{"slug": "review-x"}}
	e.varMu.Unlock()

	step := &Step{Outputs: []OutputDecl{
		{Name: "report", Path: "reports/${vars.slug}.md"},
		{Name: "blank", Path: "  "},
	}}
	opts := e.waitIdleOptionsForStep(step)
	if opts.Artifacts == nil {
		t.Fatal("declared outputs did not create a watcher")
	}
	if got := opts.Artifacts.watchedPaths(); len(got) != 1 || got[0] != "reports/review-x.md" {
		t.Fatalf("watched paths = %v, want [reports/review-x.md]", got)
	}

	if opts := e.waitIdleOptionsForStep(&Step{}); opts.Artifacts != nil {
		t.Fatal("a step without declared outputs created a watcher")
	}
}

// TestLoadWorkflowAcceptsStepOutputs guards the strict YAML decoder
// (KnownFields(true)): a step-level outputs declaration must parse rather
// than being rejected as an unknown field.
func TestLoadWorkflowAcceptsStepOutputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	writeArtifactFile(t, path, `schema_version: "2.0"
name: step-outputs-test
steps:
  - id: work
    prompt: do the work
    outputs:
      - out/result.md
      - {name: report, path: out/report.md}
`)

	workflow, result, err := LoadAndValidate(path)
	if err != nil {
		t.Fatalf("LoadAndValidate: %v", err)
	}
	if !result.Valid {
		t.Fatalf("workflow invalid: %+v", result.Errors)
	}
	if len(workflow.Steps) != 1 || len(workflow.Steps[0].Outputs) != 2 {
		t.Fatalf("step outputs did not parse: %+v", workflow.Steps)
	}
	if workflow.Steps[0].Outputs[0].Path != "out/result.md" || workflow.Steps[0].Outputs[1].Path != "out/report.md" {
		t.Fatalf("step outputs = %+v", workflow.Steps[0].Outputs)
	}
}
