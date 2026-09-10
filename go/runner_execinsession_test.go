package agentkit

// runner_execinsession_test.go — O6b's engine change: Runner.ExecInSession, the
// exported seam that turns a session id into a runnable exec.
//
// It exists because nothing else on the Runner interface could do that.
// WriteWorkspaceFile can put a file IN; getting one OUT (which is what
// cmd/agentd's dataset pull pipeline has to do) had no exported path at all —
// the resolution it needs, r.get + r.workerEnvFor, is unexported on
// *runnerImpl.
//
// The three things worth a test are the three things a caller can be wrong
// about: that argv reaches the environment unchanged (there is no shell, so a
// path with a space or a semicolon in it is one argument, not a command line);
// that a non-zero ExitCode comes back as a RESULT rather than an error, since
// `test -f` exiting 1 is an answer; and that a session with no running
// instance is refused rather than silently execing somewhere else.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/execenv"
)

// runningSession provisions a real (mock-backed) instance so ExecInSession has
// something to resolve.
func runningSession(t *testing.T, r *runnerImpl, store interface {
	Seed(*agentdb.Session)
}, id string) {
	t.Helper()
	store.Seed(&agentdb.Session{ID: id, Customer: "acme", Job: "j1", UserEmail: "u@x"})
	if _, err := r.CreateSession(context.Background(), CreateSessionRequest{
		SessionID: id, Customer: "acme", Job: "j1", UserEmail: "u@x",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

// TestExecInSession_PassesArgvThroughUnchanged is the security-relevant one.
// The dataset pull pipeline hands this method attacker-supplied paths; if any
// layer between here and the environment joined the arguments into a string, a
// path containing `;` would become a second command.
func TestExecInSession_PassesArgvThroughUnchanged(t *testing.T) {
	ctx := context.Background()
	r, env, _, store, _, _ := newTestRunner(t)
	runningSession(t, r, store, "sess-exec-1")

	argv := []string{"cat", "/workspace/a file; rm -rf /.csv"}
	if _, err := r.ExecInSession(ctx, SessionRef{SessionID: "sess-exec-1"}, argv, execenv.ExecOptions{}); err != nil {
		t.Fatalf("ExecInSession: %v", err)
	}

	var got []string
	for _, call := range env.CallsTo("Exec") {
		for _, a := range call.Args {
			if ss, ok := a.([]string); ok && len(ss) > 0 && ss[0] == "cat" {
				got = ss
			}
		}
	}
	if len(got) != 2 || got[0] != argv[0] || got[1] != argv[1] {
		t.Fatalf("argv reached the environment as %#v, want %#v", got, argv)
	}
}

// TestExecInSession_ReturnsResultAndDoesNotInterpretExitCode: a command that
// ran and exited non-zero is a SUCCESSFUL exec. Only the caller knows whether
// that is an error — `test -f` exiting 1 means "no such file", which is an
// answer, and `cat` exiting 1 means the pull failed, which is not.
func TestExecInSession_ReturnsResultAndDoesNotInterpretExitCode(t *testing.T) {
	ctx := context.Background()
	r, env, _, store, _, _ := newTestRunner(t)
	runningSession(t, r, store, "sess-exec-2")

	env.ExecExitByCmd = map[string]int{"test -f /workspace/missing.csv": 1}
	env.ExecStdoutByCmd = map[string][]byte{"cat /workspace/prices.csv": []byte("timestamp,value\n")}

	res, err := r.ExecInSession(ctx, SessionRef{SessionID: "sess-exec-2"},
		[]string{"test", "-f", "/workspace/missing.csv"}, execenv.ExecOptions{})
	if err != nil {
		t.Fatalf("a non-zero exit must not be an error: %v", err)
	}
	if res == nil || res.ExitCode != 1 {
		t.Fatalf("res = %+v, want ExitCode 1", res)
	}

	res, err = r.ExecInSession(ctx, SessionRef{SessionID: "sess-exec-2"},
		[]string{"cat", "/workspace/prices.csv"}, execenv.ExecOptions{})
	if err != nil {
		t.Fatalf("ExecInSession: %v", err)
	}
	if res.ExitCode != 0 || string(res.Stdout) != "timestamp,value\n" {
		t.Fatalf("res = %+v, want exit 0 and the seeded stdout", res)
	}
}

// TestExecInSession_NoRunningInstanceIsRefused pins the message, because the
// dataset tools surface it to a model that has to decide whether to retry.
func TestExecInSession_NoRunningInstanceIsRefused(t *testing.T) {
	ctx := context.Background()
	r, _, _, _, _, _ := newTestRunner(t)

	res, err := r.ExecInSession(ctx, SessionRef{SessionID: "sess-nowhere"},
		[]string{"cat", "/workspace/x.csv"}, execenv.ExecOptions{})
	if err == nil {
		t.Fatalf("expected a refusal, got %+v", res)
	}
	if res != nil {
		t.Fatalf("res = %+v, want nil alongside the error", res)
	}
	if !strings.Contains(err.Error(), `exec-in-session: session "sess-nowhere" has no running instance`) {
		t.Fatalf("err = %q, want the pinned no-running-instance sentence", err)
	}
}

// TestExecInSession_EnvironmentErrorSurfaces: the environment's own failure is
// returned unwrapped-in-meaning — the caller must be able to tell "the exec
// itself broke" from "the command exited non-zero".
func TestExecInSession_EnvironmentErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	r, env, _, store, _, _ := newTestRunner(t)
	runningSession(t, r, store, "sess-exec-3")

	boom := errors.New("docker daemon is not reachable")
	env.Err = boom
	_, err := r.ExecInSession(ctx, SessionRef{SessionID: "sess-exec-3"},
		[]string{"cat", "/workspace/x.csv"}, execenv.ExecOptions{})
	if err == nil {
		t.Fatal("expected the environment error to surface")
	}
	if !strings.Contains(err.Error(), boom.Error()) {
		t.Fatalf("err = %q, want it to carry %q", err, boom)
	}
}
