package container

import (
	"context"
	"os/exec"
	"testing"

	tcontainer "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func isDockerDaemonRunning() (bool, string) {
	// Check if docker command exists
	cmd := exec.Command("docker", "--version")
	if err := cmd.Run(); err != nil {
		return false, "Docker command not found. Please install Docker."
	}

	// Check if Docker daemon is running by using docker info
	// This will work regardless of the socket path as docker will use the correct one
	cmd = exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		return false, "Docker daemon is not running or not accessible. Please start Docker daemon."
	}

	return true, ""
}

// "unix:///Users/amdahliu/.colima/default/docker.sock"
const testHost = "unix:///Users/amdahliu/.colima/default/docker.sock" // "unix:///var/run/docker.sock"

func TestContainerCodeExecutor_Basic(t *testing.T) {
	available, reason := isDockerDaemonRunning()
	if !available {
		t.Skipf("Skipping container tests: %s", reason)
	}

	// Test with python:3.9-slim image (commonly available)
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running

		}),
		WithHost(testHost),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})

	// Test simple Python code execution
	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "print('Hello from container!')",
				Language: "python",
			},
		},
		ExecutionID: "test-1",
	}

	result, err := executor.ExecuteCode(context.Background(), input)
	if err != nil {
		t.Fatalf("Failed to execute code: %v", err)
	}

	expectedOutput := "Hello from container!\n"
	if result.Output != expectedOutput {
		t.Errorf("Expected output %q, got %q", expectedOutput, result.Output)
	}
}

func TestContainerCodeExecutor_WithOptions(t *testing.T) {
	// Test option functions
	host := "unix:///var/run/docker.sock"
	image := "python:3.8"
	dockerPath := "/tmp/test"

	executor := &CodeExecutor{}

	WithHost(host)(executor)
	if executor.host != host {
		t.Errorf("Expected host %s, got %s", host, executor.host)
	}

	WithContainerConfig(tcontainer.Config{
		Image: image,
	})(executor)
	if executor.containerConfig.Image != image {
		t.Errorf("Expected image %s, got %s", image, executor.containerConfig.Image)
	}

	WithDockerFilePath(dockerPath)(executor)
	// Note: dockerFilePath gets converted to absolute path, so we just check it's not empty
	if executor.dockerFilePath == "" {
		t.Error("Expected dockerFilePath to be set")
	}
}

func TestContainerCodeExecutor_ExecuteCode(t *testing.T) {
	available, reason := isDockerDaemonRunning()
	if !available {
		t.Skipf("Skipping container tests: %s", reason)
	}

	tests := []struct {
		name     string
		input    codeexecutor.CodeExecutionInput
		expected struct {
			outputContains string
			shouldError    bool
		}
	}{
		{
			name: "python hello world",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "print('Hello from Container!')",
						Language: "python",
					},
				},
				ExecutionID: "test-container-python-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Hello from Container!",
				shouldError:    false,
			},
		},
		{
			name: "bash echo",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "echo 'Hello from Bash Container!'",
						Language: "bash",
					},
				},
				ExecutionID: "test-container-bash-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Hello from Bash Container!",
				shouldError:    false,
			},
		},
		{
			name: "multiple code blocks",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "echo 'First container block'",
						Language: "bash",
					},
					{
						Code:     "print('Second container block')",
						Language: "python",
					},
				},
				ExecutionID: "test-container-multiple-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "First container block",
				shouldError:    false,
			},
		},
		{
			name: "unsupported language",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "puts 'Hello, Ruby!'",
						Language: "ruby",
					},
				},
				ExecutionID: "test-container-unsupported-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "unsupported language: ruby",
				shouldError:    false,
			},
		},
	}
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running

		}),
		WithHost(testHost),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			require.NoError(t, err)
			ctx := context.Background()

			result, err := executor.ExecuteCode(ctx, tt.input)

			if tt.expected.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Debug output for failed tests
			t.Logf("Test: %s", tt.name)
			t.Logf("Output: %q", result.Output)

			if tt.expected.outputContains != "" {
				assert.Contains(t, result.Output, tt.expected.outputContains,
					"Expected output to contain '%s', but got: '%s'", tt.expected.outputContains, result.Output)
			}

			// OutputFiles should be empty for now
			assert.Empty(t, result.OutputFiles)
		})
	}
}

func TestContainerCodeExecutor_CodeBlockDelimiter(t *testing.T) {
	if available, reason := isDockerDaemonRunning(); !available {
		t.Skipf("Skipping container tests: %s", reason)

	}
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running

		}),
		WithHost(testHost),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})
	delimiter := executor.CodeBlockDelimiter()

	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestContainerCodeExecutor_IntegrationTest(t *testing.T) {
	available, reason := isDockerDaemonRunning()
	if !available {
		t.Skipf("Skipping container integration tests: %s", reason)
	}

	input := `Let's test container execution with multiple languages:

` + "```python" + `
print("Python in container")
` + "```" + `

` + "```bash" + `
echo "Bash in container"
` + "```"

	// Step 1: Extract code blocks
	delimiter := codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
	blocks := codeexecutor.ExtractCodeBlock(input, delimiter)
	assert.Len(t, blocks, 2)

	// Step 2: Execute in containers
	executor, err := New(
		WithContainerConfig(tcontainer.Config{
			Image: "python:3.9-slim",
			Cmd:   []string{"tail", "-f", "/dev/null"}, // Keep container running
		}),
		WithHost(testHost),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		executor.Close()
	})
	ctx := context.Background()

	executionInput := codeexecutor.CodeExecutionInput{
		CodeBlocks:  blocks,
		ExecutionID: "container-integration-test",
	}

	result, err := executor.ExecuteCode(ctx, executionInput)
	assert.NoError(t, err)

	// Step 3: Format and verify result
	formattedResult := result.String()

	assert.Contains(t, result.Output, "Python in container")
	assert.Contains(t, result.Output, "Bash in container")
	assert.Contains(t, formattedResult, "Code execution result:")

	t.Logf("Container execution result: %s", result.Output)
	t.Logf("Formatted result: %s", formattedResult)
}
