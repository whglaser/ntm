package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunPipelineLintRejectsUnsafePiPrompt covers the CLI lint surface of
// ntm-l6cuz.6: lint fails before execution and names the step and construct.
func TestRunPipelineLintRejectsUnsafePiPrompt(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = oldJSON })

	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	content := `
schema_version: "2.0"
name: pi-lint-hazard
steps:
  - id: lane
    agent: pi
    prompt: |
      Inspect the source.

      <!-- operator note -->
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	err := runPipelineLint(path, &out, &errOut)
	if err == nil {
		t.Fatalf("runPipelineLint accepted an unattributable pi prompt:\nstdout=%s\nstderr=%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "lane") || !strings.Contains(errOut.String(), "HTMLBlock") {
		t.Fatalf("lint failure must name the step and construct; stderr=%q", errOut.String())
	}
}

// TestRunPipelineLintAcceptsPiSafePrompt pins that the new preflight does not
// reject the supported subset.
func TestRunPipelineLintAcceptsPiSafePrompt(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = oldJSON })

	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yaml")
	content := `
schema_version: "2.0"
name: pi-lint-safe
steps:
  - id: lane
    agent: pi
    prompt: |
      Inspect **changes** and report findings.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runPipelineLint(path, &out, &errOut); err != nil {
		t.Fatalf("runPipelineLint rejected a supported pi prompt: %v\nstderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "Validation: ok") {
		t.Fatalf("lint output = %q, want Validation: ok", out.String())
	}
}
