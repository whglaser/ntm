package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// TestKillServerTargetsPrivateServer pins the socket-resolution rule behind
// the ambient-server guard: only sockets that are provably not the ambient or
// default user server may be killed without an explicit operator opt-in.
func TestKillServerTargetsPrivateServer(t *testing.T) {
	uid := os.Getuid()
	defaultSock := filepath.Join("/tmp", fmt.Sprintf("tmux-%d", uid), "default")
	ambient := "/tmp/tmux-9999/default"
	privateRoot := t.TempDir()
	privateSocket := filepath.Join(privateRoot, "socket")

	for _, test := range []struct {
		name    string
		args    []string
		env     map[string]string
		ambient string
		want    bool
	}{
		{
			name: "no environment resolves to the default server",
			args: []string{"kill-server"},
		},
		{
			name:    "ambient TMUX socket is refused",
			args:    []string{"kill-server"},
			env:     map[string]string{"TMUX": ambient + ",123,0"},
			ambient: ambient,
		},
		{
			name: "TMUX pointing at the default socket is refused",
			args: []string{"kill-server"},
			env:  map[string]string{"TMUX": defaultSock + ",1,0"},
		},
		{
			name: "isolated TMUX_TMPDIR root is allowed",
			args: []string{"kill-server"},
			env:  map[string]string{"TMUX_TMPDIR": privateRoot},
			want: true,
		},
		{
			name:    "cleared TMUX plus private root is allowed",
			args:    []string{"kill-server"},
			env:     map[string]string{"TMUX": "", "TMUX_TMPDIR": privateRoot},
			ambient: ambient,
			want:    true,
		},
		{
			name:    "explicit private -S wins over the ambient server",
			args:    []string{"-S", privateSocket, "kill-server"},
			env:     map[string]string{"TMUX": ambient + ",1,0"},
			ambient: ambient,
			want:    true,
		},
		{
			name: "explicit -S at the default socket is refused",
			args: []string{"-S", defaultSock, "kill-server"},
		},
		{
			name:    "explicit -S at the ambient socket is refused",
			args:    []string{"-S", ambient, "kill-server"},
			env:     map[string]string{"TMUX": ambient + ",1,0"},
			ambient: ambient,
		},
		{
			name:    "attached -S form is parsed",
			args:    []string{"-S" + privateSocket, "kill-server"},
			ambient: ambient,
			want:    true,
		},
		{
			name: "equals -S form is parsed and still refused at default",
			args: []string{"-S=" + defaultSock, "kill-server"},
		},
		{
			name:    "named -L server under a private root is allowed",
			args:    []string{"-L", "mine", "kill-server"},
			env:     map[string]string{"TMUX_TMPDIR": privateRoot},
			ambient: ambient,
			want:    true,
		},
		{
			name: "named -L default on the default root is refused",
			args: []string{"-L", "default", "kill-server"},
		},
		{
			name:    "named -L canary on the default root is private",
			args:    []string{"-L", "canary", "kill-server"},
			ambient: ambient,
			want:    true,
		},
		{
			name: "empty -S value is refused",
			args: []string{"-S", "", "kill-server"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := killServerTargetsPrivateServer(test.args, mapGetenv(test.env), test.ambient)
			if got != test.want {
				t.Fatalf("killServerTargetsPrivateServer(%v, %v, ambient=%q) = %v, want %v",
					test.args, test.env, test.ambient, got, test.want)
			}
		})
	}
}

func TestGuardAmbientKillServerOnlyGuardsKillServer(t *testing.T) {
	t.Setenv(AllowKillServerEnv, "")
	if err := guardAmbientKillServer([]string{"kill-session", "-t", "=ntm:0"}); err != nil {
		t.Fatalf("kill-session must not be guarded: %v", err)
	}
	if err := guardAmbientKillServer([]string{"list-sessions"}); err != nil {
		t.Fatalf("list-sessions must not be guarded: %v", err)
	}
}

func TestGuardAmbientKillServerRefusesAndExplainsOptIn(t *testing.T) {
	t.Setenv(AllowKillServerEnv, "")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", "")

	err := guardAmbientKillServer([]string{"kill-server"})
	if !errors.Is(err, ErrKillServerRefused) {
		t.Fatalf("guardAmbientKillServer() error = %v, want ErrKillServerRefused", err)
	}
	if !strings.Contains(err.Error(), AllowKillServerEnv) {
		t.Fatalf("refusal %q does not name the %s opt-in", err, AllowKillServerEnv)
	}
}

func TestGuardAmbientKillServerHonorsOperatorOptIn(t *testing.T) {
	t.Setenv(AllowKillServerEnv, "1")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", "")

	if err := guardAmbientKillServer([]string{"kill-server"}); err != nil {
		t.Fatalf("explicit operator opt-in must allow kill-server: %v", err)
	}
}

// TestRunLocalContextRefusesAmbientKillServer proves the refusal happens
// before any process is spawned: with ambient/empty tmux environment the
// command is rejected even though a real `tmux` binary is installed.
func TestRunLocalContextRefusesAmbientKillServer(t *testing.T) {
	t.Setenv(AllowKillServerEnv, "")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", "")

	if _, err := runLocalContext(context.Background(), "kill-server"); !errors.Is(err, ErrKillServerRefused) {
		t.Fatalf("runLocalContext(kill-server) error = %v, want ErrKillServerRefused", err)
	}
}

func TestClassifyKillServerRefusalIsNotInfrastructure(t *testing.T) {
	class := ClassifyCommandError(fmt.Errorf("wrapped: %w", ErrKillServerRefused))
	if class.Infrastructure {
		t.Fatalf("kill-server refusal classified as infrastructure %+v; it would trip the circuit breaker", class)
	}
	if class.Kind != CommandErrorCommandFailed {
		t.Fatalf("kill-server refusal kind = %q, want %q", class.Kind, CommandErrorCommandFailed)
	}
}
