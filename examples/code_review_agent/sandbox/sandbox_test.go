package sandbox

import (
	"context"
	"testing"
	"time"
)

func TestLocalSandbox_RunCommand_Success(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	result, err := sandbox.RunCommand(context.Background(), "echo hello", DefaultConfig)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if result.Output != "hello\n" {
		t.Errorf("Expected output 'hello\\n', got '%s'", result.Output)
	}

	if result.TimedOut {
		t.Error("Expected not timed out")
	}
}

func TestLocalSandbox_RunCommand_Failure(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	result, err := sandbox.RunCommand(context.Background(), "false", DefaultConfig)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if result.ExitCode != 1 {
		t.Errorf("Expected exit code 1, got %d", result.ExitCode)
	}
}

func TestLocalSandbox_RunCommand_Timeout(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	config := SandboxConfig{
		Timeout:         100 * time.Millisecond,
		OutputSizeLimit: 1024,
	}

	result, err := sandbox.RunCommand(context.Background(), "sleep 1", config)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if !result.TimedOut {
		t.Error("Expected timed out")
	}
}

func TestLocalSandbox_RunCommand_OutputLimit(t *testing.T) {
	sandbox, err := NewLocalSandbox(".")
	if err != nil {
		t.Fatalf("Failed to create sandbox: %v", err)
	}
	defer sandbox.Close()

	config := SandboxConfig{
		Timeout:         10 * time.Second,
		OutputSizeLimit: 10,
	}

	result, err := sandbox.RunCommand(context.Background(), "echo '1234567890abcdef'", config)
	if err != nil {
		t.Fatalf("Failed to run command: %v", err)
	}

	if len(result.Output) > 30 {
		t.Errorf("Expected output to be truncated to ~10 chars, got %d", len(result.Output))
	}

	if !contains(result.Output, "[truncated]") {
		t.Error("Expected output to contain '[truncated]'")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
