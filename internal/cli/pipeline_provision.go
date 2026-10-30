package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/dispatch"
	"github.com/Dicklesworthstone/ntm/internal/pipeline"
	"github.com/Dicklesworthstone/ntm/internal/plugins"
	sessionPkg "github.com/Dicklesworthstone/ntm/internal/session"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// `ntm pipeline run --provision` satisfies the workflow's provision block
// before the executor starts: create the session when missing, spawn the
// declared panes with the same launch specs as `ntm spawn`, verify the exact
// pane-index layout, and wait (retrying) until every agent pane is
// delivery-ready. Provisioning is opt-in and never dispatches work; the
// pipeline's own dispatcher owns every prompt (ntm-pnc64.1).

// Test seams for the tmux/spawn boundary. Production values are the real
// helpers; tests replace them to exercise planning and classification without
// touching a tmux server.
var (
	provisionSessionExists = tmux.SessionExistsContext
	provisionListPanes     = tmux.GetPanesContext
	pipelineProvisionSpawn = spawnSessionLogicComposable
	provisionPaneReady     = func(ctx context.Context, pane tmux.Pane) error {
		_, err := dispatch.PrepareTMUXDelivery(ctx, dispatch.Delivery{Target: dispatch.Target{
			Pane: pane, Ref: pane.Ref(), AgentType: pane.Type,
		}})
		return err
	}
)

const (
	// provisionReadyTimeout bounds the wait for launched agents to become
	// delivery-ready. It is deliberately generous: ntm-pnc64.2 showed cold pi
	// starts can pass the old 15s window, and this wait retries.
	provisionReadyTimeout = 90 * time.Second
	// provisionReadyPollInterval is the re-check cadence while panes boot.
	provisionReadyPollInterval = 2 * time.Second
)

// provisionLayoutState is the classification of an existing session against a
// provision plan.
type provisionLayoutState int

const (
	// provisionLayoutFill means every existing plan pane is a bare shell and
	// the rest are missing; the spawn path can satisfy the plan exactly.
	provisionLayoutFill provisionLayoutState = iota
	// provisionLayoutSatisfied means every declared pane already hosts the
	// declared agent.
	provisionLayoutSatisfied
	// provisionLayoutConflict means at least one index cannot be repaired
	// safely; provisioning must fail loudly rather than clobber a process.
	provisionLayoutConflict
)

// provisionLayout is a classified snapshot of a session against a plan.
type provisionLayout struct {
	State     provisionLayoutState
	Satisfied []int
	Bare      []int
	Missing   []int
	Conflicts []string
}

// classifyProvisionLayout compares the session's panes with the plan. Only a
// bare shell may be filled; a dead, service, wrong-agent, or (unrecognized)
// occupied pane is a contradiction. A mix of running planned agents and
// unprovisioned panes is also a contradiction: tmux cannot repair a partial
// layout around live agents without relaunching them.
func classifyProvisionLayout(plan pipeline.ProvisionPlan, panes []tmux.Pane) provisionLayout {
	byIndex := make(map[int]tmux.Pane, len(panes))
	maxIndex := 0
	for _, pane := range panes {
		byIndex[pane.Index] = pane
		if pane.Index > maxIndex {
			maxIndex = pane.Index
		}
	}

	var layout provisionLayout
	for _, want := range plan.Panes {
		pane, ok := byIndex[want.Index]
		switch {
		case !ok:
			layout.Missing = append(layout.Missing, want.Index)
			if maxIndex > want.Index {
				layout.Conflicts = append(layout.Conflicts, fmt.Sprintf(
					"pane %d is missing below an existing pane (session reaches index %d); tmux cannot insert a pane below the end of a layout", want.Index, maxIndex))
			}
		case pane.Dead:
			layout.Conflicts = append(layout.Conflicts, fmt.Sprintf("pane %d is dead and cannot host %s", want.Index, want.Agent))
		case pane.Service != "":
			layout.Conflicts = append(layout.Conflicts, fmt.Sprintf("pane %d hosts service %q, not %s", want.Index, pane.Service, want.Agent))
		case pipeline.NormalizeAgentType(string(pane.Type)) == pipeline.NormalizeAgentType(want.Agent):
			layout.Satisfied = append(layout.Satisfied, want.Index)
		case tmux.PaneCommandIsShell(pane.Command):
			layout.Bare = append(layout.Bare, want.Index)
		default:
			observed := strings.TrimSpace(string(pane.Type))
			if observed == "" {
				observed = "an unrecognized process"
			}
			detail := fmt.Sprintf("pane %d must host %s but currently runs %s", want.Index, want.Agent, observed)
			if pane.Variant != "" {
				detail += fmt.Sprintf(" (%s)", pane.Variant)
			}
			if pane.Command != "" {
				detail += fmt.Sprintf(" [%s]", pane.Command)
			}
			layout.Conflicts = append(layout.Conflicts, detail)
		}
	}

	switch {
	case len(layout.Conflicts) > 0:
		layout.State = provisionLayoutConflict
	case len(layout.Bare) == 0 && len(layout.Missing) == 0:
		layout.State = provisionLayoutSatisfied
	case len(layout.Satisfied) == 0:
		layout.State = provisionLayoutFill
	default:
		layout.State = provisionLayoutConflict
		layout.Conflicts = append(layout.Conflicts, fmt.Sprintf(
			"the session mixes running agents (%s) with unprovisioned panes (%s); partial provisioning around a running agent is not repair-safe, so stop the session or complete it manually",
			formatProvisionPanes(layout.Satisfied), formatProvisionPanes(append(append([]int{}, layout.Bare...), layout.Missing...))))
	}
	return layout
}

func formatProvisionPanes(indices []int) string {
	parts := make([]string, len(indices))
	for i, index := range indices {
		parts[i] = fmt.Sprintf("%d", index)
	}
	return strings.Join(parts, ", ")
}

// provisionSpawnOptions converts a validated plan into the exact SpawnOptions
// shape `ntm spawn` builds from flags, so launch specs, readiness gates, and
// pane titles stay identical.
func provisionSpawnOptions(plan pipeline.ProvisionPlan, session, projectDir string) (SpawnOptions, error) {
	agents := make([]FlatAgent, 0, len(plan.Panes))
	for i, want := range plan.Panes {
		agentType, ok := canonicalSpawnAgentType(want.Agent)
		if !ok {
			return SpawnOptions{}, fmt.Errorf("provision pane %d: unsupported agent %q", want.Index, want.Agent)
		}
		agents = append(agents, FlatAgent{Type: agentType, Index: i + 1, Model: want.Model, ReasoningEffort: want.Effort})
	}
	loaded, _ := plugins.LoadAgentPlugins(pluginAgentsDirForArgs(os.Args[1:]))
	pluginMap := make(map[string]plugins.AgentPlugin, len(loaded))
	for _, p := range loaded {
		pluginMap[p.Name] = p
	}
	return SpawnOptions{
		Session:            session,
		ProjectDirOverride: projectDir,
		Agents:             agents,
		UserPane:           plan.UserPane,
		PluginMap:          pluginMap,
		// Provisioning must not dispatch work. Recovery and CASS context are
		// prompt sources; the pipeline's dispatcher owns every prompt.
		NoRecovery:    true,
		NoCassContext: true,
	}, nil
}

// resolvePipelineProvisionSession resolves --session for --provision. Unlike
// plain `pipeline run`, a missing session is not an error: provisioning
// creates it. Prefix ambiguity and invalid names still fail.
func resolvePipelineProvisionSession(ctx context.Context, session string, w io.Writer) (SessionResolution, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return SessionResolution{}, fmt.Errorf("session is required")
	}
	res, err := ResolveSessionWithOptionsContext(ctx, session, w, SessionResolveOptions{TreatAsJSON: IsJSONOutput()})
	if err == nil {
		return res, nil
	}
	var resolveErr *sessionPkg.ResolveExplicitSessionNameError
	if errors.As(err, &resolveErr) &&
		(resolveErr.Kind == sessionPkg.ResolveExplicitSessionNameErrorNoSessions ||
			resolveErr.Kind == sessionPkg.ResolveExplicitSessionNameErrorNotFound) {
		if verr := tmux.ValidateSessionName(session); verr != nil {
			return SessionResolution{}, fmt.Errorf("invalid session name: %w", verr)
		}
		return SessionResolution{Session: session, Reason: "provision target"}, nil
	}
	return SessionResolution{}, err
}

// runPipelineProvision satisfies wf.Provision for session. It is a no-op for a
// session that already matches the plan, fills a session whose only plan
// panes are bare shells or missing, and fails loudly on any contradiction.
// With dryRun it prints the plan and returns before touching tmux.
func runPipelineProvision(ctx context.Context, wf *pipeline.Workflow, session, projectDir string, dryRun bool, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if wf == nil {
		return errors.New("workflow is required")
	}
	if wf.Provision == nil {
		return fmt.Errorf("workflow %q has no provision block; add one or drop --provision", wf.Name)
	}
	plan, err := pipeline.BuildProvisionPlan(wf.Provision)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	exists, err := provisionSessionExists(ctx, session)
	if err != nil {
		return fmt.Errorf("check session %s: %w", session, err)
	}

	if dryRun {
		printProvisionPlan(out, session, plan, exists)
		return nil
	}

	layout := provisionLayout{State: provisionLayoutFill}
	if exists {
		panes, err := provisionListPanes(ctx, session)
		if err != nil {
			return fmt.Errorf("list panes for %s: %w", session, err)
		}
		layout = classifyProvisionLayout(plan, panes)
		if layout.State == provisionLayoutConflict {
			return fmt.Errorf("session %s does not match the provision layout:\n  - %s", session, strings.Join(layout.Conflicts, "\n  - "))
		}
	}

	switch layout.State {
	case provisionLayoutSatisfied:
		fmt.Fprintf(out, "✓ Session %s already matches the provision layout (%d agent panes)\n", session, len(plan.Panes))
	case provisionLayoutFill:
		if exists {
			fmt.Fprintf(out, "  Filling %d unprovisioned pane(s) in session %s\n", len(layout.Bare)+len(layout.Missing), session)
		}
		opts, err := provisionSpawnOptions(plan, session, projectDir)
		if err != nil {
			return err
		}
		if err := pipelineProvisionSpawn(ctx, opts); err != nil {
			return fmt.Errorf("provisioning session %s failed: %w", session, err)
		}
	}

	return waitProvisionReady(ctx, plan, session, provisionReadyTimeout, out)
}

// waitProvisionReady polls until every planned pane hosts its agent and can
// accept pipeline delivery, or the timeout lapses with a per-pane reason.
func waitProvisionReady(ctx context.Context, plan pipeline.ProvisionPlan, session string, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	for {
		err := provisionReadySnapshot(ctx, plan, session)
		if err == nil {
			if out != nil {
				fmt.Fprintf(out, "✓ Session %s ready: %d/%d agent pane(s) match the provision layout\n", session, len(plan.Panes), len(plan.Panes))
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("session %s was provisioned but its panes did not become ready within %s: %v", session, timeout, err)
		}
		timer := time.NewTimer(provisionReadyPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// provisionReadySnapshot reports every planned pane that is missing, has the
// wrong agent, or cannot accept delivery yet.
func provisionReadySnapshot(ctx context.Context, plan pipeline.ProvisionPlan, session string) error {
	panes, err := provisionListPanes(ctx, session)
	if err != nil {
		return fmt.Errorf("list panes: %w", err)
	}
	byIndex := make(map[int]tmux.Pane, len(panes))
	for _, pane := range panes {
		byIndex[pane.Index] = pane
	}
	var problems []string
	for _, want := range plan.Panes {
		pane, ok := byIndex[want.Index]
		if !ok {
			problems = append(problems, fmt.Sprintf("pane %d missing", want.Index))
			continue
		}
		if got := pipeline.NormalizeAgentType(string(pane.Type)); got != pipeline.NormalizeAgentType(want.Agent) {
			problems = append(problems, fmt.Sprintf("pane %d reports %q, want %s", want.Index, got, want.Agent))
			continue
		}
		if err := provisionPaneReady(ctx, pane); err != nil {
			problems = append(problems, fmt.Sprintf("pane %d not ready: %v", want.Index, err))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// printProvisionPlan renders what --provision would do. It never prints
// model/effort values in a form that could be mistaken for a command.
func printProvisionPlan(out io.Writer, session string, plan pipeline.ProvisionPlan, sessionExists bool) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, "Provision plan for session %s (dry-run; no tmux changes):\n", session)
	if plan.UserPane {
		fmt.Fprintln(out, "  user pane: 1 (reserved shell)")
	}
	for _, want := range plan.Panes {
		detail := want.Agent
		if want.Model != "" {
			detail += " model=" + want.Model
		}
		if want.Effort != "" {
			detail += " effort=" + want.Effort
		}
		fmt.Fprintf(out, "  pane %d: %s\n", want.Index, detail)
	}
	if sessionExists {
		fmt.Fprintf(out, "  session %s exists: would verify the layout and fill unprovisioned panes\n", session)
	} else {
		fmt.Fprintf(out, "  session %s is missing: would create it and spawn %d agent pane(s)\n", session, len(plan.Panes))
	}
}
