package pipeline

import (
	"fmt"
	"strings"
)

// maxProvisionPanes bounds a provision block so a typo cannot ask one command
// to create an unbounded number of panes.
const maxProvisionPanes = 64

// ProvisionSpec is the optional workflow block that declares the session
// layout a pipeline needs before it can run. It is inert unless the operator
// passes --provision to `ntm pipeline run`; the block never mutates tmux on
// its own.
type ProvisionSpec struct {
	// UserPane reserves the first pane for the operator's shell. Agent panes
	// then start at index 2 instead of 1.
	UserPane bool `yaml:"user_pane,omitempty" toml:"user_pane,omitempty" json:"user_pane,omitempty"`

	// Agents declares the agent panes in ascending, contiguous pane order.
	Agents []ProvisionAgent `yaml:"agents,omitempty" toml:"agents,omitempty" json:"agents,omitempty"`
}

// ProvisionAgent declares one agent pane, or a run of Count identical panes.
// An entry carries either Pane (one explicit index) or Count (a run starting
// at the next free index), never both. Entries are consumed in order, so the
// plan is deterministic and pane indices have no gaps.
type ProvisionAgent struct {
	Pane   int    `yaml:"pane,omitempty" toml:"pane,omitempty" json:"pane,omitempty"`
	Count  int    `yaml:"count,omitempty" toml:"count,omitempty" json:"count,omitempty"`
	Agent  string `yaml:"agent" toml:"agent" json:"agent"`
	Model  string `yaml:"model,omitempty" toml:"model,omitempty" json:"model,omitempty"`
	Effort string `yaml:"effort,omitempty" toml:"effort,omitempty" json:"effort,omitempty"`
}

// ProvisionPane is one normalized pane requirement: the exact tmux pane
// index and the agent that must occupy it.
type ProvisionPane struct {
	Index  int    `json:"index"`
	Agent  string `json:"agent"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// ProvisionPlan is the normalized, ordered layout a provision block asks for.
// Panes are sorted by Index with no gaps; the first index is 2 when the user
// pane is reserved and 1 otherwise.
type ProvisionPlan struct {
	UserPane bool            `json:"user_pane,omitempty"`
	Panes    []ProvisionPane `json:"panes"`
}

// BuildProvisionPlan expands and validates a provision block into the exact
// pane-index → agent mapping the run requires. Errors are field-scoped so
// `ntm pipeline lint` can point at the offending entry.
func BuildProvisionPlan(spec *ProvisionSpec) (ProvisionPlan, error) {
	plan := ProvisionPlan{}
	if spec == nil {
		return plan, fmt.Errorf("provision block is empty")
	}
	plan.UserPane = spec.UserPane

	next := 1
	if spec.UserPane {
		next = 2
	}
	for i, entry := range spec.Agents {
		field := fmt.Sprintf("provision.agents[%d]", i)
		agentType := strings.TrimSpace(entry.Agent)
		if agentType == "" {
			return ProvisionPlan{}, fmt.Errorf("%s: agent is required", field)
		}
		if !IsValidAgentType(agentType) {
			return ProvisionPlan{}, fmt.Errorf("%s: unknown agent %q", field, agentType)
		}
		if entry.Pane != 0 && entry.Count != 0 {
			return ProvisionPlan{}, fmt.Errorf("%s: pane and count are mutually exclusive", field)
		}
		switch {
		case entry.Pane != 0:
			if entry.Pane < next {
				return ProvisionPlan{}, fmt.Errorf("%s: pane %d is already claimed; the next agent pane is %d", field, entry.Pane, next)
			}
			if entry.Pane != next {
				return ProvisionPlan{}, fmt.Errorf("%s: pane %d leaves a gap; the next agent pane is %d (entries must be ascending and contiguous)", field, entry.Pane, next)
			}
			plan.Panes = append(plan.Panes, ProvisionPane{Index: next, Agent: agentType, Model: entry.Model, Effort: entry.Effort})
			next++
		case entry.Count > 0:
			for n := 0; n < entry.Count; n++ {
				plan.Panes = append(plan.Panes, ProvisionPane{Index: next, Agent: agentType, Model: entry.Model, Effort: entry.Effort})
				next++
			}
		default:
			return ProvisionPlan{}, fmt.Errorf("%s: pane or count is required", field)
		}
		if len(plan.Panes) > maxProvisionPanes {
			return ProvisionPlan{}, fmt.Errorf("provision declares more than %d agent panes", maxProvisionPanes)
		}
	}
	if len(plan.Panes) == 0 && !spec.UserPane {
		return ProvisionPlan{}, fmt.Errorf("provision must declare at least one agent pane or user_pane: true")
	}
	return plan, nil
}

// validateProvision folds BuildProvisionPlan into workflow validation so a
// malformed provision block fails lint/dry-run before any pane is touched.
func validateProvision(spec *ProvisionSpec, result *ValidationResult) {
	if spec == nil {
		return
	}
	if _, err := BuildProvisionPlan(spec); err != nil {
		result.addError(ParseError{
			Field:   "provision",
			Message: err.Error(),
			Hint:    "Declare agent panes in ascending contiguous order; each entry needs pane or count; user_pane: true reserves pane 1 and starts agents at pane 2.",
		})
	}
}
