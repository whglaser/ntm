package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/Dicklesworthstone/ntm/internal/agent"
	"github.com/Dicklesworthstone/ntm/internal/config"
	"github.com/Dicklesworthstone/ntm/internal/plugins"
	"github.com/Dicklesworthstone/ntm/internal/tmux"
	"github.com/Dicklesworthstone/ntm/tests/testutil"
)

// Tests for validateSpawnAgentCommands (ntm-akaq): an explicit N:model spec
// against an [agents] command that hardcodes -m must abort before any
// worktree/session/pane mutation, and template-based configs must pass
// preflight so the variant is honored at launch.
func TestValidateSpawnAgentCommandsPreflight(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })

	hardcoded := `codex --dangerously-bypass-approvals-and-sandbox -m gpt-5.6-sol -c model_reasoning_effort=ultra`

	cases := []struct {
		name      string
		codexCmd  string
		model     string
		wantError string
	}{
		{
			name:      "variant against hardcoded -m errors before mutation",
			codexCmd:  hardcoded,
			model:     "gpt-5.6-terra",
			wantError: "model override",
		},
		{
			name:     "no variant against hardcoded command passes",
			codexCmd: hardcoded,
			model:    "",
		},
		{
			name:     "variant against templated command passes",
			codexCmd: config.DefaultAgentTemplates().Codex,
			model:    "gpt-5.6-terra",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg = &config.Config{}
			cfg.Agents.Codex = tc.codexCmd

			opts := SpawnOptions{
				Session: "preflight-test",
				Agents: []FlatAgent{
					{Type: AgentTypeCodex, Index: 1, Model: tc.model},
				},
			}
			err := validateSpawnAgentCommands(opts, "")
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("expected preflight to pass, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected preflight error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantError)
			}
			if !strings.Contains(err.Error(), "preflight") {
				t.Fatalf("error should identify itself as a preflight failure: %q", err.Error())
			}
		})
	}
}

func TestSpawnAgentCommandTemplateUnknownType(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = &config.Config{}

	if _, _, err := spawnAgentCommandTemplate(AgentType("nope"), nil, ""); err == nil {
		t.Fatal("expected error for unknown agent type")
	}
}

// TestProgramStatusLaunchPrefixPersistsForPi pins ntm-bh677: ntm-spawned pi
// panes carry a PI_PROGRAM_STATUS=1 assignment inside the persisted launch
// command, so the authoritative OSC 7501 channel exists without the operator
// exporting the variable, and sessions save/resume replay it. Other plugins
// and the documented PI_PROGRAM_STATUS=0 off-switch are untouched.
func TestProgramStatusLaunchPrefixPersistsForPi(t *testing.T) {
	if err := agentpkg.RegisterPlugin("pi", nil, nil, nil); err != nil {
		t.Fatalf("register pi plugin: %v", err)
	}
	pluginMap := map[string]plugins.AgentPlugin{
		"pi":     {Name: "pi", Command: "pi --approve"},
		"hermes": {Name: "hermes", Command: "hermes run"},
	}

	tmpl, env, err := spawnAgentCommandTemplate(AgentType("pi"), pluginMap, "")
	if err != nil || env != nil {
		t.Fatalf("pi template = (%q, %v, %v)", tmpl, env, err)
	}
	if tmpl != "PI_PROGRAM_STATUS=1 pi --approve" {
		t.Fatalf("pi template = %q, want the persisted program-status prefix", tmpl)
	}
	rendered, err := config.GenerateAgentCommand(tmpl, config.AgentTemplateVars{AgentType: "pi"})
	if err != nil {
		t.Fatalf("render pi command: %v", err)
	}
	spec, err := captureAgentLaunchSpec(AgentType("pi"), rendered, "", "", "", "", "")
	if err != nil {
		t.Fatalf("capture pi launch spec: %v", err)
	}
	if !strings.HasPrefix(spec.Command, "PI_PROGRAM_STATUS=1 pi ") {
		t.Fatalf("persisted pi command = %q, want the program-status prefix", spec.Command)
	}
	if err := spec.ValidateReplay(tmux.AgentType("pi")); err != nil {
		t.Fatalf("prefixed pi launch spec must stay replay-safe: %v", err)
	}
	if len(spec.OmittedEnv) != 0 {
		t.Fatalf("program-status prefix must not be persisted as omitted env: %+v", spec.OmittedEnv)
	}

	if got, _, err := resolveAddAgentCommandTemplate(AgentType("pi"), pluginMap, ""); err != nil || got != tmpl {
		t.Fatalf("add pi template = (%q, %v), want %q", got, err, tmpl)
	}
	if got, _, _ := spawnAgentCommandTemplate(AgentType("hermes"), pluginMap, ""); got != "hermes run" {
		t.Fatalf("non-pi plugin template = %q, want unchanged", got)
	}

	t.Setenv("PI_PROGRAM_STATUS", "0")
	if got, _, _ := spawnAgentCommandTemplate(AgentType("pi"), pluginMap, ""); got != "pi --approve" {
		t.Fatalf("PI_PROGRAM_STATUS=0 template = %q, want no prefix", got)
	}
}

// TestSpawnMissingDirNonTTYFailsFast (ntm-5ni5): a non-TTY spawn against a
// missing project directory must return a structured error naming
// --create-dir instead of blocking on the interactive [y/N] prompt. Test
// processes have non-TTY stdin, so reaching the error (rather than hanging
// or auto-aborting with exit 0) is exactly the contract under test.
func TestSpawnMissingDirNonTTYFailsFast(t *testing.T) {
	testutil.RequireTmuxThrottled(t)

	tmpDir, err := os.MkdirTemp("", "ntm-test-nodir")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldCfg := cfg
	oldJSON := jsonOutput
	t.Cleanup(func() { cfg = oldCfg; jsonOutput = oldJSON })
	cfg = newTmuxIntegrationTestConfig(tmpDir)
	jsonOutput = false
	cfg.Agents.Claude = testAgentCatCommandTemplate

	opts := SpawnOptions{
		Session: fmt.Sprintf("ntm-test-nodir-%d", time.Now().UnixNano()),
		Agents:  []FlatAgent{{Type: AgentTypeClaude, Index: 1}},
		CCCount: 1,
	}
	spawnErr := spawnSessionLogicContext(t.Context(), opts)
	if spawnErr == nil {
		_ = tmux.KillSession(opts.Session)
		t.Fatal("spawn against missing directory succeeded; want fail-fast error")
	}
	if !strings.Contains(spawnErr.Error(), "--create-dir") {
		t.Fatalf("error %q should name --create-dir", spawnErr.Error())
	}
}
