package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProvisionPlanExpandsCountsAndPreservesOrder(t *testing.T) {
	spec := &ProvisionSpec{
		UserPane: true,
		Agents: []ProvisionAgent{
			{Pane: 2, Agent: "pi", Model: "azure-foundry/DeepSeek-V4.1-Flash", Effort: "max"},
			{Pane: 3, Agent: "pi", Model: "FW-GLM-5.3-Pro", Effort: "high"},
			{Count: 5, Agent: "pi", Model: "FW-GLM-5.3-Flash", Effort: "max"},
		},
	}
	plan, err := BuildProvisionPlan(spec)
	if err != nil {
		t.Fatalf("BuildProvisionPlan: %v", err)
	}
	if !plan.UserPane {
		t.Fatal("user_pane was dropped")
	}
	if len(plan.Panes) != 7 || plan.Panes[0].Index != 2 || plan.Panes[len(plan.Panes)-1].Index != 8 {
		t.Fatalf("plan = %+v, want panes 2..8", plan)
	}
	want := []ProvisionPane{
		{Index: 2, Agent: "pi", Model: "azure-foundry/DeepSeek-V4.1-Flash", Effort: "max"},
		{Index: 3, Agent: "pi", Model: "FW-GLM-5.3-Pro", Effort: "high"},
		{Index: 4, Agent: "pi", Model: "FW-GLM-5.3-Flash", Effort: "max"},
		{Index: 5, Agent: "pi", Model: "FW-GLM-5.3-Flash", Effort: "max"},
		{Index: 6, Agent: "pi", Model: "FW-GLM-5.3-Flash", Effort: "max"},
		{Index: 7, Agent: "pi", Model: "FW-GLM-5.3-Flash", Effort: "max"},
		{Index: 8, Agent: "pi", Model: "FW-GLM-5.3-Flash", Effort: "max"},
	}
	for i := range want {
		if plan.Panes[i] != want[i] {
			t.Fatalf("pane %d = %+v, want %+v", i, plan.Panes[i], want[i])
		}
	}
}

func TestBuildProvisionPlanStartsAtOneWithoutUserPane(t *testing.T) {
	plan, err := BuildProvisionPlan(&ProvisionSpec{
		Agents: []ProvisionAgent{
			{Count: 2, Agent: "cc"},
			{Pane: 3, Agent: "cod", Model: "gpt-5.2-codex"},
		},
	})
	if err != nil {
		t.Fatalf("BuildProvisionPlan: %v", err)
	}
	if plan.UserPane {
		t.Fatal("user_pane reported true when not requested")
	}
	if len(plan.Panes) != 3 || plan.Panes[0].Index != 1 || plan.Panes[2].Index != 3 {
		t.Fatalf("plan = %+v, want contiguous panes 1..3", plan)
	}
	if plan.Panes[2].Agent != "cod" || plan.Panes[2].Model != "gpt-5.2-codex" {
		t.Fatalf("explicit pane lost its agent identity: %+v", plan.Panes[2])
	}
}

func TestBuildProvisionPlanRejectsInvalidLayouts(t *testing.T) {
	tests := []struct {
		name    string
		spec    *ProvisionSpec
		wantErr string
	}{
		{
			name:    "gap before explicit pane",
			spec:    &ProvisionSpec{UserPane: true, Agents: []ProvisionAgent{{Pane: 3, Agent: "pi"}}},
			wantErr: "next agent pane is 2",
		},
		{
			name:    "pane and count together",
			spec:    &ProvisionSpec{UserPane: true, Agents: []ProvisionAgent{{Pane: 2, Count: 3, Agent: "pi"}}},
			wantErr: "mutually exclusive",
		},
		{
			name:    "unknown agent",
			spec:    &ProvisionSpec{UserPane: true, Agents: []ProvisionAgent{{Pane: 2, Agent: "not-an-agent"}}},
			wantErr: "unknown agent",
		},
		{
			name:    "agent pane collides with user pane",
			spec:    &ProvisionSpec{UserPane: true, Agents: []ProvisionAgent{{Pane: 1, Agent: "pi"}}},
			wantErr: "next agent pane is 2",
		},
		{
			name:    "neither pane nor count",
			spec:    &ProvisionSpec{Agents: []ProvisionAgent{{Agent: "cc"}}},
			wantErr: "pane or count is required",
		},
		{
			name:    "empty block",
			spec:    &ProvisionSpec{},
			wantErr: "at least one agent pane",
		},
		{
			name:    "missing agent",
			spec:    &ProvisionSpec{Agents: []ProvisionAgent{{Pane: 1}}},
			wantErr: "agent is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildProvisionPlan(tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestLoadAndValidateProvisionBlock is the lint half of ntm-pnc64.1: a
// usable SDR-style block validates, and a gapped block fails with a
// provision-scoped error before any pane is touched.
func TestLoadAndValidateProvisionBlock(t *testing.T) {
	dir := t.TempDir()
	base := fmt.Sprintf("schema_version: %q\nname: provision-test\n", SchemaVersion)

	good := filepath.Join(dir, "good.yaml")
	goodBody := base + `provision:
  user_pane: true
  agents:
    - pane: 2
      agent: pi
      model: azure-foundry/DeepSeek-V4.1-Flash
      effort: max
    - count: 5
      agent: pi
      model: FW-GLM-5.3-Flash
      effort: max
steps:
  - id: work
    prompt: do the work
    pane: 2
`
	if err := os.WriteFile(good, []byte(goodBody), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, result, err := LoadAndValidate(good)
	if err != nil {
		t.Fatalf("LoadAndValidate(good): %v", err)
	}
	if !result.Valid {
		t.Fatalf("valid provision block rejected: %+v", result.Errors)
	}
	if wf.Provision == nil || len(wf.Provision.Agents) != 2 {
		t.Fatalf("provision block not parsed: %+v", wf.Provision)
	}

	bad := filepath.Join(dir, "bad.yaml")
	badBody := base + `provision:
  user_pane: true
  agents:
    - pane: 4
      agent: pi
steps:
  - id: work
    prompt: do the work
    pane: 2
`
	if err := os.WriteFile(bad, []byte(badBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_, result, err = LoadAndValidate(bad)
	if err != nil {
		t.Fatalf("LoadAndValidate(bad): %v", err)
	}
	if result.Valid {
		t.Fatal("gapped provision block validated")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "provision" && strings.Contains(e.Message, "next agent pane is 2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no provision-scoped error: %+v", result.Errors)
	}
}
