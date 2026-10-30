package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	agentpkg "github.com/Dicklesworthstone/ntm/internal/agent"
	"github.com/Dicklesworthstone/ntm/internal/pipeline"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// registerPiForProvisionTest makes the plugin agent type used by the SDR
// provision block resolvable in-process. Real commands register plugin types
// from the root pre-run; unit tests exercise the provision path directly.
func registerPiForProvisionTest(t *testing.T) {
	t.Helper()
	if err := agentpkg.RegisterPlugin("pi", nil, nil, nil); err != nil {
		t.Fatalf("register pi plugin: %v", err)
	}
}

func provisionPlanForTest(t *testing.T, agents ...pipeline.ProvisionAgent) pipeline.ProvisionPlan {
	t.Helper()
	plan, err := pipeline.BuildProvisionPlan(&pipeline.ProvisionSpec{UserPane: true, Agents: agents})
	if err != nil {
		t.Fatalf("BuildProvisionPlan: %v", err)
	}
	return plan
}

func provisionWorkflowForTest(agents ...pipeline.ProvisionAgent) *pipeline.Workflow {
	return &pipeline.Workflow{
		Name:      "provision-test",
		Provision: &pipeline.ProvisionSpec{UserPane: true, Agents: agents},
	}
}

// readyProvisionPanes returns user pane 1 plus a running pi pane at each of
// the plan's agent indices.
func readyProvisionPanes(plan pipeline.ProvisionPlan) []tmux.Pane {
	panes := []tmux.Pane{{ID: "%1", Index: 1, Command: "bash", Type: tmux.AgentUser}}
	for _, want := range plan.Panes {
		panes = append(panes, tmux.Pane{
			ID:      "%" + string(rune('0'+want.Index)),
			Index:   want.Index,
			Command: "pi",
			Type:    tmux.AgentType("pi"),
		})
	}
	return panes
}

type provisionSeams struct {
	spawned  []SpawnOptions
	lists    int
	spawnErr error
}

// installProvisionSeams replaces the tmux/spawn boundary. ready controls
// whether the readiness gate accepts every pane after a successful spawn.
func installProvisionSeams(t *testing.T, exists bool, panes []tmux.Pane, ready bool) *provisionSeams {
	t.Helper()
	seams := &provisionSeams{}
	oldExists, oldList, oldSpawn, oldReady := provisionSessionExists, provisionListPanes, pipelineProvisionSpawn, provisionPaneReady
	t.Cleanup(func() {
		provisionSessionExists, provisionListPanes, pipelineProvisionSpawn, provisionPaneReady = oldExists, oldList, oldSpawn, oldReady
	})
	provisionSessionExists = func(context.Context, string) (bool, error) { return exists, nil }
	provisionListPanes = func(context.Context, string) ([]tmux.Pane, error) {
		seams.lists++
		return panes, nil
	}
	pipelineProvisionSpawn = func(_ context.Context, opts SpawnOptions) error {
		seams.spawned = append(seams.spawned, opts)
		return seams.spawnErr
	}
	provisionPaneReady = func(context.Context, tmux.Pane) error {
		if ready {
			return nil
		}
		return errors.New("fixture pane not ready")
	}
	return seams
}

func TestClassifyProvisionLayout(t *testing.T) {
	plan := provisionPlanForTest(t,
		pipeline.ProvisionAgent{Pane: 2, Agent: "pi"},
		pipeline.ProvisionAgent{Pane: 3, Agent: "pi"},
		pipeline.ProvisionAgent{Pane: 4, Agent: "pi"},
	)
	user := tmux.Pane{ID: "%1", Index: 1, Command: "bash", Type: tmux.AgentUser}
	pi := func(index int) tmux.Pane {
		return tmux.Pane{ID: "%" + string(rune('0'+index)), Index: index, Command: "pi", Type: tmux.AgentType("pi")}
	}
	shell := func(index int) tmux.Pane {
		return tmux.Pane{ID: "%" + string(rune('0'+index)), Index: index, Command: "bash", Type: tmux.AgentUnknown}
	}

	tests := []struct {
		name         string
		panes        []tmux.Pane
		wantState    provisionLayoutState
		wantConflict string
	}{
		{
			name:      "satisfied",
			panes:     []tmux.Pane{user, pi(2), pi(3), pi(4)},
			wantState: provisionLayoutSatisfied,
		},
		{
			name:      "missing trailing panes are fillable",
			panes:     []tmux.Pane{user},
			wantState: provisionLayoutFill,
		},
		{
			name:      "bare shell panes are fillable",
			panes:     []tmux.Pane{user, shell(2)},
			wantState: provisionLayoutFill,
		},
		{
			name:         "wrong agent at an index is a contradiction",
			panes:        []tmux.Pane{user, {ID: "%2", Index: 2, Command: "claude", Type: tmux.AgentClaude}, pi(3), pi(4)},
			wantState:    provisionLayoutConflict,
			wantConflict: "must host pi but currently runs cc [claude]",
		},
		{
			name:         "hole below an existing pane is a contradiction",
			panes:        []tmux.Pane{user, pi(2), {ID: "%4", Index: 4, Command: "claude", Type: tmux.AgentClaude}},
			wantState:    provisionLayoutConflict,
			wantConflict: "missing below an existing pane",
		},
		{
			name:         "running agent mixed with unprovisioned panes is not repair-safe",
			panes:        []tmux.Pane{user, pi(2), shell(3)},
			wantState:    provisionLayoutConflict,
			wantConflict: "mixes running agents",
		},
		{
			name:         "service pane is a contradiction",
			panes:        []tmux.Pane{user, {ID: "%2", Index: 2, Command: "cm", Service: "cm"}, pi(3), pi(4)},
			wantState:    provisionLayoutConflict,
			wantConflict: "hosts service",
		},
		{
			name:         "dead pane is a contradiction",
			panes:        []tmux.Pane{user, {ID: "%2", Index: 2, Command: "bash", Dead: true}, pi(3), pi(4)},
			wantState:    provisionLayoutConflict,
			wantConflict: "dead",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := classifyProvisionLayout(plan, tt.panes)
			if layout.State != tt.wantState {
				t.Fatalf("state = %v, want %v (conflicts=%v)", layout.State, tt.wantState, layout.Conflicts)
			}
			if tt.wantConflict != "" {
				joined := strings.Join(layout.Conflicts, "\n")
				if !strings.Contains(joined, tt.wantConflict) {
					t.Fatalf("conflicts = %q, want %q", joined, tt.wantConflict)
				}
			}
		})
	}
}

func TestProvisionSpawnOptionsMirrorPlan(t *testing.T) {
	registerPiForProvisionTest(t)
	plan := provisionPlanForTest(t,
		pipeline.ProvisionAgent{Pane: 2, Agent: "pi", Model: "azure-foundry/DeepSeek-V4.1-Flash", Effort: "max"},
		pipeline.ProvisionAgent{Pane: 3, Agent: "claude", Model: "claude-sonnet-4-5"},
	)
	opts, err := provisionSpawnOptions(plan, "sdr", "/projects/sdr")
	if err != nil {
		t.Fatalf("provisionSpawnOptions: %v", err)
	}
	if opts.Session != "sdr" || opts.ProjectDirOverride != "/projects/sdr" || !opts.UserPane {
		t.Fatalf("options lost session/project/user-pane identity: %+v", opts)
	}
	if !opts.NoRecovery || !opts.NoCassContext {
		t.Fatalf("provisioning may inject a prompt source: %+v", opts)
	}
	if len(opts.Agents) != 2 {
		t.Fatalf("agents = %+v, want 2", opts.Agents)
	}
	if opts.Agents[0].Type != AgentType("pi") || opts.Agents[0].Model != "azure-foundry/DeepSeek-V4.1-Flash" || opts.Agents[0].ReasoningEffort != "max" || opts.Agents[0].Index != 1 {
		t.Fatalf("first agent = %+v", opts.Agents[0])
	}
	if opts.Agents[1].Type != AgentTypeClaude || opts.Agents[1].Model != "claude-sonnet-4-5" || opts.Agents[1].Index != 2 {
		t.Fatalf("second agent = %+v", opts.Agents[1])
	}
}

func TestRunPipelineProvisionFreshSessionSpawnsOnce(t *testing.T) {
	registerPiForProvisionTest(t)
	wf := provisionWorkflowForTest(
		pipeline.ProvisionAgent{Pane: 2, Agent: "pi", Model: "m1", Effort: "max"},
		pipeline.ProvisionAgent{Count: 2, Agent: "pi", Model: "m2", Effort: "max"},
	)
	plan, err := pipeline.BuildProvisionPlan(wf.Provision)
	if err != nil {
		t.Fatal(err)
	}
	seams := installProvisionSeams(t, false, readyProvisionPanes(plan), true)

	var out bytes.Buffer
	if err := runPipelineProvision(context.Background(), wf, "sdr", "/projects/sdr", false, &out); err != nil {
		t.Fatalf("runPipelineProvision: %v", err)
	}
	if len(seams.spawned) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(seams.spawned))
	}
	opts := seams.spawned[0]
	if opts.Session != "sdr" || !opts.UserPane || opts.ProjectDirOverride != "/projects/sdr" {
		t.Fatalf("spawn options = %+v", opts)
	}
	if len(opts.Agents) != 3 || opts.Agents[1].Model != "m2" || opts.Agents[2].ReasoningEffort != "max" {
		t.Fatalf("spawn agents = %+v", opts.Agents)
	}
	if !strings.Contains(out.String(), "ready: 3/3") {
		t.Fatalf("readiness success not reported: %q", out.String())
	}
}

func TestRunPipelineProvisionExistingSatisfiedIsNoop(t *testing.T) {
	registerPiForProvisionTest(t)
	wf := provisionWorkflowForTest(pipeline.ProvisionAgent{Pane: 2, Agent: "pi"})
	plan, err := pipeline.BuildProvisionPlan(wf.Provision)
	if err != nil {
		t.Fatal(err)
	}
	seams := installProvisionSeams(t, true, readyProvisionPanes(plan), true)

	var out bytes.Buffer
	if err := runPipelineProvision(context.Background(), wf, "sdr", "/projects/sdr", false, &out); err != nil {
		t.Fatalf("runPipelineProvision: %v", err)
	}
	if len(seams.spawned) != 0 {
		t.Fatalf("satisfied layout was re-spawned: %+v", seams.spawned)
	}
	if !strings.Contains(out.String(), "already matches") {
		t.Fatalf("no-op was not reported: %q", out.String())
	}
}

func TestRunPipelineProvisionFillsBareLayout(t *testing.T) {
	registerPiForProvisionTest(t)
	wf := provisionWorkflowForTest(pipeline.ProvisionAgent{Pane: 2, Agent: "pi"})
	plan, err := pipeline.BuildProvisionPlan(wf.Provision)
	if err != nil {
		t.Fatal(err)
	}
	// The session exists with only its user pane; the spawn path must fill
	// pane 2, and the post-spawn listing reports the satisfied layout.
	seams := installProvisionSeams(t, true, []tmux.Pane{{ID: "%1", Index: 1, Command: "bash", Type: tmux.AgentUser}}, true)
	// The readiness wait lists again after the spawn; return the ready frame
	// on every probe.
	provisionListPanes = func(context.Context, string) ([]tmux.Pane, error) {
		seams.lists++
		if seams.lists > 1 {
			return readyProvisionPanes(plan), nil
		}
		return []tmux.Pane{{ID: "%1", Index: 1, Command: "bash", Type: tmux.AgentUser}}, nil
	}

	if err := runPipelineProvision(context.Background(), wf, "sdr", "/projects/sdr", false, nil); err != nil {
		t.Fatalf("runPipelineProvision: %v", err)
	}
	if len(seams.spawned) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(seams.spawned))
	}
}

func TestRunPipelineProvisionConflictFailsClosed(t *testing.T) {
	registerPiForProvisionTest(t)
	wf := provisionWorkflowForTest(pipeline.ProvisionAgent{Pane: 2, Agent: "pi"})
	seams := installProvisionSeams(t, true, []tmux.Pane{
		{ID: "%1", Index: 1, Command: "bash", Type: tmux.AgentUser},
		{ID: "%2", Index: 2, Command: "claude", Type: tmux.AgentClaude},
	}, true)

	err := runPipelineProvision(context.Background(), wf, "sdr", "/projects/sdr", false, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match the provision layout") || !strings.Contains(err.Error(), "must host pi") {
		t.Fatalf("conflicting index did not fail closed: %v", err)
	}
	if len(seams.spawned) != 0 {
		t.Fatalf("conflicting layout reached spawn: %+v", seams.spawned)
	}
}

func TestRunPipelineProvisionRequiresProvisionBlock(t *testing.T) {
	wf := &pipeline.Workflow{Name: "no-provision"}
	err := runPipelineProvision(context.Background(), wf, "sdr", "/projects/sdr", false, nil)
	if err == nil || !strings.Contains(err.Error(), "no provision block") {
		t.Fatalf("missing provision block was accepted: %v", err)
	}
}

func TestRunPipelineProvisionDryRunDoesNotTouchTmux(t *testing.T) {
	registerPiForProvisionTest(t)
	wf := provisionWorkflowForTest(pipeline.ProvisionAgent{Pane: 2, Agent: "pi", Model: "m1", Effort: "max"})
	seams := installProvisionSeams(t, false, nil, true)

	var out bytes.Buffer
	if err := runPipelineProvision(context.Background(), wf, "sdr", "/projects/sdr", true, &out); err != nil {
		t.Fatalf("dry-run provisioning: %v", err)
	}
	if len(seams.spawned) != 0 || seams.lists != 0 {
		t.Fatalf("dry-run touched tmux: spawned=%d lists=%d", len(seams.spawned), seams.lists)
	}
	for _, want := range []string{"dry-run", "pane 2: pi model=m1 effort=max", "would create it"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output %q is missing %q", out.String(), want)
		}
	}
}

// TestPipelineRunHasProvisionFlag pins the CLI wiring so the flag cannot be
// dropped without failing here.
func TestPipelineRunHasProvisionFlag(t *testing.T) {
	cmd := newPipelineRunCmd()
	flag := cmd.Flags().Lookup("provision")
	if flag == nil {
		t.Fatal("pipeline run lost its --provision flag")
	}
	if flag.DefValue != "false" || !strings.Contains(flag.Usage, "provision block") {
		t.Fatalf("--provision flag = %+v", flag)
	}
}
