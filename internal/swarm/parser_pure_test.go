package swarm

import (
	"testing"
)

// =============================================================================
// agent_launcher.go: parsePrimaryWindowIndex
// =============================================================================

func TestParsePrimaryWindowIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"empty string", "", 0, true},
		{"whitespace only", "   \n  \n  ", 0, true},
		{"single value", "5", 5, false},
		{"single value 1", "1", 1, false},
		{"multiple values min", "3\n5\n2\n7", 2, false},
		{"window 1 preferred", "3\n1\n5", 1, false},
		{"window 1 not min", "0\n1\n5", 1, false},
		{"no valid integers", "abc\ndef", 0, true},
		{"mixed valid invalid", "abc\n3\ndef\n7", 3, false},
		{"blank lines ignored", "\n\n4\n\n6\n\n", 4, false},
		{"leading trailing spaces", "  3  \n  5  ", 3, false},
		{"zero is valid", "0\n2\n3", 0, false},
		{"negative values", "-1\n2\n3", -1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePrimaryWindowIndex(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parsePrimaryWindowIndex(%q) expected error, got %d", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePrimaryWindowIndex(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("parsePrimaryWindowIndex(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// =============================================================================
// agent_launcher.go: parseMinIntOutput
// =============================================================================

func TestParseMinIntOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"empty string", "", 0, true},
		{"whitespace only", "  \n  ", 0, true},
		{"single value", "42", 42, false},
		{"multiple values", "10\n5\n20\n3\n15", 3, false},
		{"no valid integers", "abc\nxyz", 0, true},
		{"mixed with invalid lines", "abc\n7\ndef\n3\nghi", 3, false},
		{"blank lines ignored", "\n\n8\n\n", 8, false},
		{"all same value", "5\n5\n5", 5, false},
		{"zero is min", "3\n0\n1", 0, false},
		{"negative min", "-2\n1\n5", -2, false},
		{"spaces in lines", "  4  \n  2  \n  9  ", 2, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseMinIntOutput(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseMinIntOutput(%q) expected error, got %d", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMinIntOutput(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("parseMinIntOutput(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// =============================================================================
// agent_launcher.go: swarmTmuxPaneIndex
// =============================================================================

// =============================================================================
// agent_launcher.go: formatPaneTargetWithWindow
// =============================================================================

// =============================================================================
// agent_launcher.go: swarmPaneTargetFromPlanIndex
// =============================================================================
