package handoff

import (
	"testing"
)

// ---------------------------------------------------------------------------
// IsValid — 0% → 100%
// ---------------------------------------------------------------------------

func TestIsValid_ValidHandoff(t *testing.T) {
	t.Parallel()

	h := New("test-session").
		WithGoalAndNow("Implement feature", "Write tests").
		WithStatus(StatusComplete, OutcomeSucceeded)

	if !h.IsValid() {
		t.Error("expected valid handoff to return true")
	}
}

func TestIsValid_InvalidHandoff(t *testing.T) {
	t.Parallel()

	h := &Handoff{} // empty, missing required fields
	if h.IsValid() {
		t.Error("expected invalid handoff to return false")
	}
}

// ---------------------------------------------------------------------------
// MustValidate — 0% → 100%
// ---------------------------------------------------------------------------

func TestValidate_ValidHandoff(t *testing.T) {
	t.Parallel()

	h := New("test-session").
		WithGoalAndNow("Implement feature", "Write tests").
		WithStatus(StatusComplete, OutcomeSucceeded)

	if errs := h.Validate(); len(errs) > 0 {
		t.Fatalf("expected no validation errors, got %v", errs)
	}
}
