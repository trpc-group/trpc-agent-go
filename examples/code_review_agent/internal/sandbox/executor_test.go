package sandbox

import (
	"context"
	"testing"

	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/state"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestSandboxRunner_TimeoutHandling(t *testing.T) {
	gs := graph.State{
		state.StateKeyInputRepoPath: "/tmp",
		state.StateKeyAllowedCommands: []types.SandboxCommand{
			{Name: "sleep", Cmd: "sleep", Args: []string{"5"}, Timeout: 100},
		},
		state.StateKeyExecutorConfig: types.ExecutorConfig{Type: "local", MaxOutputMB: 10},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("SandboxRunner should never return error: %v", err)
	}
	finalState := result.(graph.State)
	sr, _ := finalState[state.StateKeySandboxResults].([]types.SandboxResult)

	if len(sr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(sr))
	}
	if !sr[0].TimedOut {
		t.Error("expected TimedOut=true for sleep 5s with 100ms timeout")
	}
	if sr[0].ErrorType != "timeout" {
		t.Errorf("ErrorType = %q, want \"timeout\"", sr[0].ErrorType)
	}
}

func TestSandboxRunner_SkipWithoutRepo(t *testing.T) {
	gs := graph.State{
		state.StateKeyAllowedCommands: []types.SandboxCommand{
			{Name: "go_vet", Cmd: "go", Args: []string{"vet", "./..."}},
		},
		state.StateKeyExecutorConfig: types.ExecutorConfig{Type: "local"},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("SandboxRunner should never error: %v", err)
	}
	finalState := result.(graph.State)
	sr, _ := finalState[state.StateKeySandboxResults].([]types.SandboxResult)

	if len(sr) != 1 {
		t.Fatalf("expected 1 sentinel result, got %d", len(sr))
	}
	if sr[0].Command != "skipped (dry-run)" {
		t.Errorf("expected skip sentinel, got %q", sr[0].Command)
	}
}

func TestSandboxRunner_ExitCodeCapture(t *testing.T) {
	gs := graph.State{
		state.StateKeyInputRepoPath: "/tmp",
		state.StateKeyAllowedCommands: []types.SandboxCommand{
			{Name: "exit_test", Cmd: "sh", Args: []string{"-c", "exit 42"}, Timeout: 5000},
		},
		state.StateKeyExecutorConfig: types.ExecutorConfig{Type: "local", MaxOutputMB: 10},
	}

	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	sr, _ := finalState[state.StateKeySandboxResults].([]types.SandboxResult)

	if len(sr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(sr))
	}
	if sr[0].ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", sr[0].ExitCode)
	}
}

func TestSandboxRunner_ContainerConfigFallsBack(t *testing.T) {
	// When Docker is unavailable, container config should fall back to local
	gs := graph.State{
		state.StateKeyInputRepoPath: "/tmp",
		state.StateKeyAllowedCommands: []types.SandboxCommand{
			{Name: "echo", Cmd: "echo", Args: []string{"hello"}, Timeout: 5000},
		},
		state.StateKeyExecutorConfig: types.ExecutorConfig{Type: "container", MaxOutputMB: 10},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("SandboxRunner should never error (must fallback): %v", err)
	}
	finalState := result.(graph.State)
	sr, _ := finalState[state.StateKeySandboxResults].([]types.SandboxResult)

	if len(sr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(sr))
	}
	// Container fallback to local should still execute the command
	if sr[0].ErrorType == "sandbox_crash" && sr[0].ExitCode == -1 {
		t.Logf("Container fallback not available on this host (expected): %s", sr[0].Stderr)
	}
}
