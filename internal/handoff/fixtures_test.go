package handoff

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type fixtureCase struct {
	name            string
	expectParseErr  bool
	expectedErrKeys []string
	assertFields    func(t *testing.T, h *Handoff)
}

func TestHandoffFixtures(t *testing.T) {
	root := findRepoRoot(t)
	reader := NewReader(".")
	fixtures := []fixtureCase{
		{
			name: "valid_complete.yaml",
			assertFields: func(t *testing.T, h *Handoff) {
				if h.Session != "myproject" {
					t.Errorf("expected session=myproject, got %q", h.Session)
				}
				if len(h.DoneThisSession) != 1 {
					t.Errorf("expected 1 done_this_session entry, got %d", len(h.DoneThisSession))
				}
				if h.Decisions["alg"] != "RS256" {
					t.Errorf("expected decisions.alg=RS256, got %q", h.Decisions["alg"])
				}
				if h.ReservationTransfer == nil || len(h.ReservationTransfer.Reservations) != 1 {
					t.Errorf("expected reservation transfer with 1 reservation, got %+v", h.ReservationTransfer)
				}
			},
		},
		{
			name: "valid_minimal.yaml",
		},
		{
			name:            "invalid_missing_goal.yaml",
			expectedErrKeys: []string{"goal"},
		},
		{
			name:            "invalid_missing_now.yaml",
			expectedErrKeys: []string{"now"},
		},
		{
			name:           "invalid_malformed.yaml",
			expectParseErr: true,
		},
		{
			name: "valid_special_chars.yaml",
			assertFields: func(t *testing.T, h *Handoff) {
				if h.Session != "my-project_123" {
					t.Errorf("expected session=my-project_123, got %q", h.Session)
				}
				if h.Goal == "" || h.Now == "" {
					t.Errorf("expected goal/now to be populated, got goal=%q now=%q", h.Goal, h.Now)
				}
			},
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, "testdata", "handoffs", tc.name)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", path, err)
			}
			t.Logf("fixture=%s bytes=%d", path, len(data))
			t.Logf("fixture contents:\n%s", string(data))

			// Quick YAML parse for early diagnostics.
			var raw Handoff
			if err := yaml.Unmarshal(data, &raw); err != nil {
				if !tc.expectParseErr {
					t.Fatalf("unexpected YAML parse error: %v", err)
				}
				t.Logf("expected parse error: %v", err)
				return
			}

			// Read via Reader to match production behavior.
			h, err := reader.Read(path)
			if tc.expectParseErr {
				if err == nil {
					t.Fatalf("expected parse error for %s, got nil", tc.name)
				}
				t.Logf("expected reader error: %v", err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected reader error: %v", err)
			}

			t.Logf("parsed handoff: session=%q goal=%q now=%q status=%q outcome=%q",
				h.Session, h.Goal, h.Now, h.Status, h.Outcome)

			errs := h.Validate()
			t.Logf("validation errors: %v", errs)

			if len(tc.expectedErrKeys) == 0 && len(errs) > 0 {
				t.Fatalf("expected no validation errors, got %v", errs)
			}
			for _, field := range tc.expectedErrKeys {
				if len(validationErrorsForField(errs, field)) == 0 {
					t.Errorf("expected validation error for field %q, got %v", field, errs)
				}
			}

			if tc.assertFields != nil {
				tc.assertFields(t, h)
			}
		})
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found; cannot locate repo root")
		}
		dir = parent
	}
}
