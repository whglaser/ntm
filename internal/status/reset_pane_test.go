package status

import (
	"testing"
	"time"
)

// TestUnifiedDetectorResetPaneDropsContentHistory pins the per-step reset:
// after ResetPane the next observation is a fresh baseline (hasHistory=false),
// so timestamp history from a previous step cannot leak into the next one.
func TestUnifiedDetectorResetPaneDropsContentHistory(t *testing.T) {
	detector := NewDetector()
	t0 := time.Now()

	changedAt, hasHistory := detector.observePaneContent("%0", "same content", t0)
	if hasHistory {
		t.Fatal("first observation reported history")
	}
	if !changedAt.Equal(t0) {
		t.Fatalf("first changedAt = %v, want %v", changedAt, t0)
	}

	changedAt, hasHistory = detector.observePaneContent("%0", "same content", t0.Add(time.Second))
	if !hasHistory || !changedAt.Equal(t0) {
		t.Fatalf("repeat observation = (%v, history=%v), want (%v, true)", changedAt, hasHistory, t0)
	}

	detector.ResetPane("%0")
	fresh := t0.Add(2 * time.Second)
	changedAt, hasHistory = detector.observePaneContent("%0", "same content", fresh)
	if hasHistory {
		t.Fatal("reset did not retire the pane's history")
	}
	if !changedAt.Equal(fresh) {
		t.Fatalf("post-reset changedAt = %v, want %v", changedAt, fresh)
	}
}

// TestUnifiedDetectorResetPaneIsPaneScoped: resetting one pane must not erase
// another pane's history.
func TestUnifiedDetectorResetPaneIsPaneScoped(t *testing.T) {
	detector := NewDetector()
	t0 := time.Now()
	_, _ = detector.observePaneContent("%0", "zero", t0)
	_, _ = detector.observePaneContent("%1", "one", t0)

	detector.ResetPane("%0")

	if _, hasHistory := detector.observePaneContent("%0", "zero", t0.Add(time.Second)); hasHistory {
		t.Fatal("pane %0 kept history after its reset")
	}
	if changedAt, hasHistory := detector.observePaneContent("%1", "one", t0.Add(time.Second)); !hasHistory || !changedAt.Equal(t0) {
		t.Fatalf("pane %%1 history was disturbed: (%v, history=%v)", changedAt, hasHistory)
	}
}

type resetRecordingFallback struct {
	fakeStatusDetector
	reset []string
}

func (f *resetRecordingFallback) ResetPane(paneID string) {
	f.reset = append(f.reset, paneID)
}

func TestOSCDetectorResetPaneForwardsToFallback(t *testing.T) {
	fallback := &resetRecordingFallback{}
	detector := NewOSCDetector(fallback, nil, DefaultOSCStatusTTL)

	detector.ResetPane("%0")
	detector.ResetPane("%1")

	if len(fallback.reset) != 2 || fallback.reset[0] != "%0" || fallback.reset[1] != "%1" {
		t.Fatalf("forwarded resets = %v, want [%%0 %%1]", fallback.reset)
	}
}

// A fallback without a reset hook must not panic the wrapper; the executor's
// optional-interface probe is what records the gap.
func TestOSCDetectorResetPaneToleratesFallbackWithoutHook(t *testing.T) {
	detector := NewOSCDetector(&fakeStatusDetector{}, nil, DefaultOSCStatusTTL)
	detector.ResetPane("%0")
}
