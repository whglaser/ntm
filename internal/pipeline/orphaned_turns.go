package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Dicklesworthstone/ntm/internal/status"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
)

// Orphaned-turn classes. A failed or cancelled step whose pane never stopped
// working is not a delivery bug — ntm deliberately stops observing instead of
// killing the agent — but it is an operator-visibility and reconciliation gap
// (ntm-l6cuz.8). The class distinguishes the two states an operator acts on
// differently: interrupt a live turn, or adopt artifacts that landed after
// the step stopped.
const (
	// OrphanedTurnGenerating means the pane's agent is still producing now.
	OrphanedTurnGenerating = "generating"
	// OrphanedTurnArtifactLanded means the pane is no longer producing, but
	// output or declared artifacts appeared after the step's failure.
	OrphanedTurnArtifactLanded = "artifact_after_failure"
)

// Orphaned-turn decisions. Both are explicit operator actions; neither sends a
// prompt, restarts a step, or rewrites a receipt.
const (
	OrphanedTurnActionAdopt = "adopted"
	OrphanedTurnActionStop  = "stopped"
)

// OrphanedArtifact is one declared step output that exists with a modification
// time after the step stopped. Only path, size and mod time are recorded;
// file contents are never captured.
type OrphanedArtifact struct {
	Path    string    `json:"path"`
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size,omitempty"`
}

// OrphanedTurn is a failed or cancelled step with post-failure evidence.
type OrphanedTurn struct {
	StepID         string             `json:"step_id"`
	StepStatus     string             `json:"step_status"`
	PaneID         string             `json:"pane_id,omitempty"`
	AgentType      string             `json:"agent_type,omitempty"`
	StartedAt      time.Time          `json:"started_at,omitempty"`
	FinishedAt     time.Time          `json:"finished_at,omitempty"`
	Class          string             `json:"class"`
	AgentState     string             `json:"agent_state,omitempty"`
	StateSource    string             `json:"state_source,omitempty"`
	LastActive     time.Time          `json:"last_active,omitempty"`
	NewOutputBytes int                `json:"new_output_bytes,omitempty"`
	Artifacts      []OrphanedArtifact `json:"artifacts,omitempty"`
	PaneVerified   bool               `json:"pane_verified"`
	// Decision and DecidedAt mark a turn already reconciled by an operator
	// (adopted or stopped); the turn stays listed so the decision is visible.
	Decision  string    `json:"decision,omitempty"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}

// OrphanedTurnInspection is the read-only orphan report for one run.
type OrphanedTurnInspection struct {
	RunID      string         `json:"run_id"`
	WorkflowID string         `json:"workflow_id"`
	Session    string         `json:"session"`
	Turns      []OrphanedTurn `json:"orphaned_turns"`
}

// OrphanedTurnDecision is one append-only operator decision persisted in the
// run state. It records provenance without changing any step result.
type OrphanedTurnDecision struct {
	StepID    string             `json:"step_id"`
	Action    string             `json:"action"`
	Class     string             `json:"class,omitempty"`
	Reason    string             `json:"reason"`
	PaneID    string             `json:"pane_id,omitempty"`
	PanePID   int                `json:"pane_pid,omitempty"`
	DecidedAt time.Time          `json:"decided_at"`
	Artifacts []OrphanedArtifact `json:"artifacts,omitempty"`
}

// OrphanedTurnActionResult reports one adopted or stopped turn.
type OrphanedTurnActionResult struct {
	RunID      string               `json:"run_id"`
	WorkflowID string               `json:"workflow_id"`
	Session    string               `json:"session"`
	Decision   OrphanedTurnDecision `json:"decision"`
	Replayed   bool                 `json:"replayed"`
}

// InspectOrphanedTurnsContext loads a run and reports turns that kept
// producing after their step failed or was cancelled. Inspection only reads:
// it never interrupts a pane, adopts an artifact, or writes state.
func InspectOrphanedTurnsContext(ctx context.Context, projectDir, runID string) (*OrphanedTurnInspection, error) {
	cfg := DefaultExecutorConfig("")
	cfg.ProjectDir, cfg.RunID = projectDir, runID
	return NewExecutor(cfg).InspectOrphanedTurns(ctx)
}

// InspectOrphanedTurns is the executor-scoped inspection entry point used
// directly by tests with an injected tmux client and detector.
func (e *Executor) InspectOrphanedTurns(ctx context.Context) (*OrphanedTurnInspection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(e.config.ProjectDir) == "" {
		return nil, fmt.Errorf("project directory is required for pipeline recovery")
	}
	e.config.ProjectDir = normalizeLockRoot(e.config.ProjectDir)
	state, err := LoadState(e.config.ProjectDir, e.config.RunID)
	if err != nil {
		return nil, err
	}
	return e.inspectOrphanedTurnsFromState(ctx, state)
}

func (e *Executor) inspectOrphanedTurnsFromState(ctx context.Context, state *ExecutionState) (*OrphanedTurnInspection, error) {
	if state == nil {
		return nil, fmt.Errorf("execution state is required for orphaned-turn inspection")
	}
	for id, saved := range state.AgentDeliveries {
		if err := validateAgentDelivery(id, saved, state.Session); err != nil {
			return nil, err
		}
	}
	e.config.Session = state.Session
	result := &OrphanedTurnInspection{
		RunID: state.RunID, WorkflowID: state.WorkflowID, Session: state.Session,
		Turns: make([]OrphanedTurn, 0),
	}
	// A missing or dead session still reports file artifacts; only the live
	// pane evidence is dropped.
	panes, paneErr := e.orphanPaneSnapshot(ctx)
	stepIDs := make([]string, 0, len(state.Steps))
	for id := range state.Steps {
		stepIDs = append(stepIDs, id)
	}
	sort.Strings(stepIDs)
	for _, stepID := range stepIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		step, record, ok := failedStepWithDelivery(state, stepID)
		if !ok {
			continue
		}
		turn := OrphanedTurn{
			StepID: stepID, StepStatus: string(step.Status),
			PaneID: record.PaneID, AgentType: record.AgentType,
			StartedAt: step.StartedAt, FinishedAt: step.FinishedAt,
		}
		turn.Artifacts = e.declaredArtifactsAfter(state, stepID, step.FinishedAt)
		if paneErr == nil {
			if pane, found := orphanPaneByID(panes, record.PaneID); found && !pane.Dead && pane.PID > 0 && (record.PanePID <= 0 || pane.PID == record.PanePID) {
				turn.PaneVerified = true
				if agentState, err := e.detector.Detect(record.PaneID); err == nil {
					turn.AgentState = string(agentState.State)
					turn.StateSource = string(agentState.StateSource)
					turn.LastActive = agentState.LastActive
					if agentState.State != status.StateIdle && agentState.State != "" {
						turn.Class = OrphanedTurnGenerating
					}
				}
				if turn.Class == "" {
					if current, err := e.captureAgentDelivery(ctx, record); err == nil {
						if grew := orphanOutputGrowth(record.Output, current); grew > 0 {
							turn.NewOutputBytes = grew
							turn.Class = OrphanedTurnArtifactLanded
						}
					}
				}
			}
		}
		if decision, ok := orphanDecisionForStep(state, stepID); ok {
			turn.Decision, turn.DecidedAt = decision.Action, decision.DecidedAt
			turn.Artifacts = mergeOrphanArtifacts(turn.Artifacts, decision.Artifacts)
		}
		if turn.Class == "" && len(turn.Artifacts) > 0 {
			turn.Class = OrphanedTurnArtifactLanded
		}
		if turn.Class == "" {
			continue
		}
		result.Turns = append(result.Turns, turn)
	}
	// Operators need to see reconciled turns too: a decision that was already
	// recorded stays in the listing, marked with its action, so a later
	// inspection does not look as if the orphan vanished silently.
	emitted := make(map[string]struct{}, len(result.Turns))
	for _, turn := range result.Turns {
		emitted[turn.StepID] = struct{}{}
	}
	for _, decision := range state.OrphanedTurnDecisions {
		if _, exists := emitted[decision.StepID]; exists {
			continue
		}
		class := decision.Class
		if class == "" {
			class = OrphanedTurnArtifactLanded
		}
		step := state.Steps[decision.StepID]
		turn := OrphanedTurn{
			StepID: decision.StepID, StepStatus: string(step.Status),
			PaneID: decision.PaneID, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt,
			Class: class, Artifacts: decision.Artifacts,
			Decision: decision.Action, DecidedAt: decision.DecidedAt,
		}
		if record, ok := state.AgentDeliveries[decision.StepID]; ok {
			turn.AgentType = record.AgentType
		}
		result.Turns = append(result.Turns, turn)
	}
	sort.Slice(result.Turns, func(i, j int) bool { return result.Turns[i].StepID < result.Turns[j].StepID })
	return result, nil
}

// failedStepWithDelivery returns the step and receipt for a step that stopped
// being observed, when the pair can be reasoned about at all.
func failedStepWithDelivery(state *ExecutionState, stepID string) (StepResult, AgentDeliveryState, bool) {
	step, ok := state.Steps[stepID]
	if !ok || (step.Status != StatusFailed && step.Status != StatusCancelled) {
		return StepResult{}, AgentDeliveryState{}, false
	}
	record, ok := state.AgentDeliveries[stepID]
	if !ok || strings.TrimSpace(record.PaneID) == "" {
		return StepResult{}, AgentDeliveryState{}, false
	}
	return step, record, true
}

func (e *Executor) orphanPaneSnapshot(ctx context.Context) ([]tmux.Pane, error) {
	if client, ok := e.tmuxClient().(interface {
		GetPanesContext(context.Context, string) ([]tmux.Pane, error)
	}); ok {
		return client.GetPanesContext(ctx, e.config.Session)
	}
	return e.tmuxClient().GetPanes(e.config.Session)
}

func orphanPaneByID(panes []tmux.Pane, paneID string) (tmux.Pane, bool) {
	for _, pane := range panes {
		if pane.ID == paneID {
			return pane, true
		}
	}
	return tmux.Pane{}, false
}

// orphanOutputGrowth reports output added since the receipt's last capture. A
// retained prefix yields its byte count; a repainted frame reports 1 so the
// class stays "changed" without inventing a count.
func orphanOutputGrowth(before, after string) int {
	if after == "" || after == before {
		return 0
	}
	if strings.HasPrefix(after, before) {
		return len(after) - len(before)
	}
	if strings.TrimSpace(after) == strings.TrimSpace(before) {
		return 0
	}
	return 1
}

// declaredArtifactsAfter reports declared step outputs that exist and were
// modified after the step stopped. Paths are variable-substituted exactly like
// the runtime output-stability check; unreadable or unchanged paths are not
// evidence.
func (e *Executor) declaredArtifactsAfter(state *ExecutionState, stepID string, finished time.Time) []OrphanedArtifact {
	if state == nil || finished.IsZero() || strings.TrimSpace(state.WorkflowFile) == "" {
		return nil
	}
	workflow, err := ParseFile(state.WorkflowFile)
	if err != nil || workflow == nil {
		return nil
	}
	step := findWorkflowStepByID(workflow, stepID)
	if step == nil || len(step.Outputs) == 0 {
		return nil
	}
	sub := NewSubstitutor(state, state.Session, state.WorkflowID)
	sub.SetDefaults(workflow.Defaults)
	var artifacts []OrphanedArtifact
	for _, decl := range step.Outputs {
		if strings.TrimSpace(decl.Path) == "" {
			continue
		}
		resolved, err := sub.Substitute(decl.Path)
		if err != nil || strings.TrimSpace(resolved) == "" {
			continue
		}
		path, info, ok := e.orphanArtifactPath(resolved)
		if !ok || !info.ModTime().After(finished) {
			continue
		}
		artifacts = append(artifacts, OrphanedArtifact{
			Path: path, ModTime: info.ModTime().UTC(), Size: info.Size(),
		})
	}
	if len(artifacts) == 0 {
		return nil
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts
}

// orphanArtifactPath resolves a substituted declared-output path against the
// project directory, falling back to the process working directory.
func (e *Executor) orphanArtifactPath(resolved string) (string, os.FileInfo, bool) {
	candidates := make([]string, 0, 2)
	if filepath.IsAbs(resolved) {
		candidates = append(candidates, resolved)
	} else {
		if e.config.ProjectDir != "" {
			candidates = append(candidates, filepath.Join(e.config.ProjectDir, resolved))
		}
		candidates = append(candidates, resolved)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate, info, true
		}
	}
	return "", nil, false
}

// findWorkflowStepByID resolves a runtime step ID to its workflow declaration.
// Foreach and parallel children execute under materialized IDs
// ("parent_child"), so an exact match is tried first and then each parent's
// nested body with the parent prefix stripped.
func findWorkflowStepByID(workflow *Workflow, stepID string) *Step {
	if workflow == nil || stepID == "" {
		return nil
	}
	if step := findStepInList(workflow.Steps, stepID); step != nil {
		return step
	}
	var found *Step
	piPromptSteps(workflow.Steps, "steps", func(step *Step, _ string) {
		if found != nil || step.ID == "" {
			return
		}
		for _, sep := range []string{"_", "/", "#", ":", "."} {
			prefix := step.ID + sep
			if !strings.HasPrefix(stepID, prefix) {
				continue
			}
			if child := findStepInList(nestedBodySteps(step), strings.TrimPrefix(stepID, prefix)); child != nil {
				found = child
				return
			}
		}
	})
	return found
}

func findStepInList(steps []Step, id string) *Step {
	if id == "" {
		return nil
	}
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
	}
	return nil
}

// nestedBodySteps returns the steps a parent can materialize with a prefix.
func nestedBodySteps(step *Step) []Step {
	if step == nil {
		return nil
	}
	body := append([]Step{}, step.Parallel.Steps...)
	for _, fc := range []*ForeachConfig{step.Foreach, step.ForeachPane} {
		if fc == nil {
			continue
		}
		body = append(body, fc.Steps...)
		body = append(body, fc.Body...)
	}
	if step.Loop != nil {
		body = append(body, step.Loop.Steps...)
		body = append(body, step.Loop.Body...)
	}
	body = append(body, step.OnSuccess...)
	return body
}

// AdoptOrphanedTurnContext records post-failure artifact provenance for one
// failed or cancelled step. explicitArtifacts lets an operator attest to files
// the automatic inspection cannot see (for example a run whose frozen workflow
// predates output declarations). It never sends a prompt, restarts the step,
// or rewrites the step outcome.
func AdoptOrphanedTurnContext(ctx context.Context, projectDir, runID, stepID, reason string, explicitArtifacts []string) (*OrphanedTurnActionResult, error) {
	cfg := DefaultExecutorConfig("")
	cfg.ProjectDir, cfg.RunID = projectDir, runID
	return NewExecutor(cfg).decideOrphanedTurn(ctx, stepID, reason, OrphanedTurnActionAdopt, explicitArtifacts)
}

// StopOrphanedTurnContext interrupts a pane whose verified turn is still
// generating after its step failed or was cancelled. The decision is recorded
// in the run state so a later resume treats the stop as intentional; it never
// resends the prompt or restarts the step.
func StopOrphanedTurnContext(ctx context.Context, projectDir, runID, stepID, reason string) (*OrphanedTurnActionResult, error) {
	cfg := DefaultExecutorConfig("")
	cfg.ProjectDir, cfg.RunID = projectDir, runID
	return NewExecutor(cfg).decideOrphanedTurn(ctx, stepID, reason, OrphanedTurnActionStop, nil)
}

func (e *Executor) decideOrphanedTurn(ctx context.Context, stepID, reason, action string, explicitArtifacts []string) (*OrphanedTurnActionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if action != OrphanedTurnActionAdopt && action != OrphanedTurnActionStop {
		return nil, fmt.Errorf("unknown orphaned-turn action %q", action)
	}
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(e.config.ProjectDir) == "" || strings.TrimSpace(stepID) == "" || stepID != strings.TrimSpace(stepID) {
		return nil, fmt.Errorf("pipeline recovery requires a project directory and exact step ID")
	}
	if reason == "" || len(reason) > agentDeliveryMaxReasonBytes {
		return nil, fmt.Errorf("pipeline recovery reason must contain 1 to %d bytes", agentDeliveryMaxReasonBytes)
	}
	if e.config.DryRun {
		return nil, fmt.Errorf("orphaned-turn reconciliation cannot be a dry run; inspect pipeline recovery instead")
	}
	e.config.ProjectDir = normalizeLockRoot(e.config.ProjectDir)
	control, err := AcquireRunControl(ctx, e.config.ProjectDir, e.config.RunID)
	if err != nil {
		return nil, err
	}
	defer control.Close()
	ctx = control.Context()
	state, err := LoadState(e.config.ProjectDir, e.config.RunID)
	if err != nil {
		return nil, err
	}
	record, ok := state.AgentDeliveries[stepID]
	if !ok {
		return nil, fmt.Errorf("step %q has no saved agent delivery", stepID)
	}
	if err := validateAgentDelivery(stepID, record, state.Session); err != nil {
		return nil, err
	}
	if step, ok := state.Steps[stepID]; !ok || (step.Status != StatusFailed && step.Status != StatusCancelled) {
		return nil, fmt.Errorf("step %q is not failed or cancelled; orphaned-turn reconciliation only applies to stopped steps", stepID)
	}
	// Resolve operator-named artifacts before replay so a retry with different
	// provenance cannot silently replay the earlier decision.
	explicit, err := e.explicitOrphanArtifacts(explicitArtifacts)
	if err != nil {
		return nil, err
	}
	for _, prior := range state.OrphanedTurnDecisions {
		if prior.StepID != stepID {
			continue
		}
		if prior.Action == action && prior.Reason == reason && orphanDecisionCovers(prior.Artifacts, explicit) {
			return &OrphanedTurnActionResult{
				RunID: state.RunID, WorkflowID: state.WorkflowID, Session: state.Session,
				Decision: prior, Replayed: true,
			}, nil
		}
		return nil, fmt.Errorf("step %q already has an immutable orphaned-turn decision (%s); a different action, reason, or artifact set is refused", stepID, prior.Action)
	}
	inspection, err := e.inspectOrphanedTurnsFromState(ctx, state)
	if err != nil {
		return nil, err
	}
	turn, found := orphanTurnByStep(inspection, stepID)
	if !found && len(explicit) == 0 {
		return nil, fmt.Errorf("step %q has no live orphaned-turn evidence; inspect before deciding, or adopt named files with --artifact", stepID)
	}
	decision := OrphanedTurnDecision{
		StepID: stepID, Action: action, Reason: reason,
		PaneID: record.PaneID, PanePID: record.PanePID,
		DecidedAt: time.Now().UTC(), Artifacts: mergeOrphanArtifacts(turn.Artifacts, explicit),
	}
	switch {
	case found:
		decision.Class = turn.Class
	case len(decision.Artifacts) > 0:
		decision.Class = OrphanedTurnArtifactLanded
	}
	if action == OrphanedTurnActionStop {
		if !turn.PaneVerified {
			return nil, fmt.Errorf("agent pane %s for step %q cannot be verified; refusing to interrupt a pane that may have been reused", record.PaneID, stepID)
		}
		if turn.Class != OrphanedTurnGenerating {
			return nil, fmt.Errorf("step %q has no generating turn to stop (observed class %s)", stepID, turn.Class)
		}
		release, err := e.acquirePaneLockCrossProcess(ctx, record.PaneID)
		if err != nil {
			return nil, err
		}
		defer release()
		if _, err := e.agentDeliveryPane(ctx, record.PaneID, record.PanePID, record.Endpoint); err != nil {
			return nil, err
		}
		if err := e.tmuxClient().InterruptPane(record.PaneID, record.AgentType); err != nil {
			return nil, fmt.Errorf("interrupt orphaned pane %s: %w", record.PaneID, err)
		}
	}
	state.OrphanedTurnDecisions = append(state.OrphanedTurnDecisions, decision)
	state.UpdatedAt = time.Now().UTC()
	if err := SaveState(e.config.ProjectDir, state); err != nil {
		return nil, err
	}
	return &OrphanedTurnActionResult{
		RunID: state.RunID, WorkflowID: state.WorkflowID, Session: state.Session, Decision: decision,
	}, nil
}

// orphanDecisionForStep returns the operator decision already recorded for a
// step, if any.
func orphanDecisionForStep(state *ExecutionState, stepID string) (OrphanedTurnDecision, bool) {
	if state == nil {
		return OrphanedTurnDecision{}, false
	}
	for _, decision := range state.OrphanedTurnDecisions {
		if decision.StepID == stepID {
			return decision, true
		}
	}
	return OrphanedTurnDecision{}, false
}

func orphanTurnByStep(inspection *OrphanedTurnInspection, stepID string) (OrphanedTurn, bool) {
	if inspection == nil {
		return OrphanedTurn{}, false
	}
	for _, turn := range inspection.Turns {
		if turn.StepID == stepID {
			return turn, true
		}
	}
	return OrphanedTurn{}, false
}

// explicitOrphanArtifacts validates operator-named adoption artifacts. Each
// path must name an existing regular file; only path, size and mod time are
// kept.
func (e *Executor) explicitOrphanArtifacts(paths []string) ([]OrphanedArtifact, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	artifacts := make([]OrphanedArtifact, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, ref := range paths {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, fmt.Errorf("--artifact requires a non-empty path")
		}
		path, info, ok := e.orphanArtifactPath(ref)
		if !ok {
			return nil, fmt.Errorf("--artifact %q is not an existing regular file", ref)
		}
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		artifacts = append(artifacts, OrphanedArtifact{Path: path, ModTime: info.ModTime().UTC(), Size: info.Size()})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

// orphanDecisionCovers reports whether the recorded decision already contains
// every artifact the retry names. An empty request replays on action+reason
// alone, matching the accept paths' decision identity.
func orphanDecisionCovers(recorded, requested []OrphanedArtifact) bool {
	if len(requested) == 0 {
		return true
	}
	present := make(map[string]struct{}, len(recorded))
	for _, artifact := range recorded {
		present[artifact.Path] = struct{}{}
	}
	for _, artifact := range requested {
		if _, ok := present[artifact.Path]; !ok {
			return false
		}
	}
	return true
}

// mergeOrphanArtifacts unions automatic and operator-named provenance by path.
func mergeOrphanArtifacts(automatic, explicit []OrphanedArtifact) []OrphanedArtifact {
	merged := append([]OrphanedArtifact{}, automatic...)
	seen := make(map[string]struct{}, len(merged))
	for _, artifact := range merged {
		seen[artifact.Path] = struct{}{}
	}
	for _, artifact := range explicit {
		if _, dup := seen[artifact.Path]; dup {
			continue
		}
		seen[artifact.Path] = struct{}{}
		merged = append(merged, artifact)
	}
	if len(merged) == 0 {
		return nil
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Path < merged[j].Path })
	return merged
}
