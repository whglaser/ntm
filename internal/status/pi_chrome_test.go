package status

import (
	"strings"
	"testing"
)

func piRule(width int) string { return strings.Repeat("─", width) }

// piIdlePane reproduces a live pi 1.3.0 idle capture at the pane sizes used by
// ntm (the composer is bottom-pinned; status rows sit below the bottom rule).
func piIdlePane() string {
	return strings.Join([]string{
		"",
		piRule(26),
		"",
		piRule(26),
		"~/projects/demo (main)",
		"0.0%/1.0M (auto)  DeepSeek-V4.1-Flash",
	}, "\n")
}

func TestPiTranscriptDropsIdleComposer(t *testing.T) {
	if got := AgentTranscript(piIdlePane(), "pi"); strings.TrimSpace(got) != "" {
		t.Fatalf("idle composer leaked into transcript: %q", got)
	}
}

func TestPiTranscriptDropsWorkingComposerKeepsTranscript(t *testing.T) {
	pane := strings.Join([]string{
		"user: run the intake step",
		"assistant: reading the charter and registers",
		"",
		"── ⠹ Working ─────────────",
		"",
		piRule(26),
		"~/projects/demo (main)",
		"↑444k ↓36k R377k CH99.9%",
	}, "\n")
	want := "user: run the intake step\nassistant: reading the charter and registers"
	if got := strings.TrimRight(AgentTranscript(pane, "pi"), "\n"); got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}

func TestPiTranscriptDropsMultiLineDraft(t *testing.T) {
	pane := strings.Join([]string{
		"kept transcript line",
		piRule(26),
		"first draft line",
		"second draft line",
		"third draft line",
		piRule(26),
		"~/projects/demo (main)",
		"0.0%/1.0M (auto)",
	}, "\n")
	if got := strings.TrimRight(AgentTranscript(pane, "pi"), "\n"); got != "kept transcript line" {
		t.Fatalf("draft composer leaked: %q", got)
	}
}

// A rule inside the transcript must be kept: only the composer's rule pair is
// chrome. The bottom rule anchor is found first, and the walk stops at the
// first rule above it (the composer top), not at a transcript rule above that.
func TestPiTranscriptKeepsRulesAboveComposer(t *testing.T) {
	pane := strings.Join([]string{
		"section one",
		piRule(26),
		"section two",
		"",
		"── ⠙ Working ─────────────",
		piRule(26),
		"~/projects/demo (main)",
		"0.0%/1.0M (auto)",
	}, "\n")
	want := "section one\n" + piRule(26) + "\nsection two"
	if got := strings.TrimRight(AgentTranscript(pane, "pi"), "\n"); got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}

// A completely empty capture must stay empty rather than being treated as
// chrome, and a pane with no recognizable composer must pass through unchanged.
func TestPiTranscriptPassthroughWithoutComposer(t *testing.T) {
	if got := AgentTranscript("", "pi"); got != "" {
		t.Fatalf("empty capture changed: %q", got)
	}
	plain := "plain transcript\nsecond line"
	if got := AgentTranscript(plain, "pi"); got != plain {
		t.Fatalf("plain capture changed: %q", got)
	}
}

// The shell launch command ntm types above the startup banner is cleared by
// pi on the first turn. The transcript must begin at the banner so a saved
// pre-dispatch capture remains a prefix of later captures.
func TestPiTranscriptDropsLaunchCommandBeforeBanner(t *testing.T) {
	pane := strings.Join([]string{
		"[user@host demo]$ cd '/home/user/projects/demo' && pi --approve --model 'x'",
		"",
		" ▀▀█  v1.3.0",
		" █▀ █ escape interrupt ·",
		"",
		"── ⠙ Working ──",
		piRule(26),
		"~/projects/demo (main)",
		"0.0%/1.0M (auto)",
	}, "\n")
	want := " ▀▀█  v1.3.0\n █▀ █ escape interrupt ·"
	if got := strings.TrimRight(AgentTranscript(pane, "pi"), "\n"); got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}
