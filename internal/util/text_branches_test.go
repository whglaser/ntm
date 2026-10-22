package util

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Truncate — cover final return (line 133): all rune boundaries ≤ targetLen
// ---------------------------------------------------------------------------

func TestTruncate_MultibyteFinalReturn(t *testing.T) {
	t.Parallel()

	// "🎉🎉" is 8 bytes (4 per emoji). With n=7, targetLen=4.
	// Rune starts at byte 0 and 4 — both ≤ 4. The for-range loop completes
	// without triggering the early return, falling through to line 133.
	input := "🎉🎉"
	n := 7
	got := Truncate(input, n)
	want := "🎉..."
	if got != want {
		t.Errorf("Truncate(%q, %d) = %q, want %q", input, n, got, want)
	}
}

func TestTruncate_ThreeMultibyteChars(t *testing.T) {
	t.Parallel()

	// "日本語" is 9 bytes (3 per char). With n=8, targetLen=5.
	// Rune at 0 (≤5) → prevI=0; rune at 3 (≤5) → prevI=3; rune at 6 (>5) → return s[:3]+"..."
	input := "日本語"
	n := 8
	got := Truncate(input, n)
	want := "日..."
	if got != want {
		t.Errorf("Truncate(%q, %d) = %q, want %q", input, n, got, want)
	}
}
