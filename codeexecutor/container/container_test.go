package container_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

// isDockerAvailable checks if Docker is available for testing
func isDockerAvailable() bool {
	cmd := exec.Command("docker", "version")
	return cmd.Run() == nil
}

func TestContainerCodeExecutor_ExecuteCode(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available, skipping container tests")
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := container.New()
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

func TestContainerCodeExecutor_ExecuteCode_WithWorkDir(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available, skipping container tests")
	}

	// Create a temporary directory for testing in user's home for Docker compatibility
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	tempDir, err := os.MkdirTemp(filepath.Join(homeDir, ".tmp"), "test-container-workdir-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	executor := container.New(
		container.WithContainerWorkDir(tempDir),
	)

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "echo 'Testing Container WorkDir' > test_output.txt\ncat test_output.txt",
				Language: "bash",
			},
		},
		ExecutionID: "test-container-workdir-1",
	}

	ctx := context.Background()
	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err)
	assert.Contains(t, result.Output, "Testing Container WorkDir")
	assert.Empty(t, result.OutputFiles)

	// Verify that the file was created in the specified work directory
	outputFile := filepath.Join(tempDir, "test_output.txt")
	_, err = os.Stat(outputFile)
	assert.NoError(t, err, "File should exist in work directory")
}

func TestContainerCodeExecutor_ExecuteCode_WithTimeout(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available, skipping container tests")
	}

	executor := container.New(
		container.WithContainerTimeout(2 * time.Second),
	)

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "import time\ntime.sleep(5)", // Sleep longer than timeout
				Language: "python",
			},
		},
		ExecutionID: "test-container-timeout-1",
	}

	ctx := context.Background()
	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err) // ExecuteCode itself doesn't return error for block execution failures
	assert.Contains(t, result.Output, "Error executing code block")
}

func TestContainerCodeExecutor_CodeBlockDelimiter(t *testing.T) {
	executor := container.New()
	delimiter := executor.CodeBlockDelimiter()

	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestContainerCodeExecutor_WithOptions(t *testing.T) {
	// Test creating executor with multiple options
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	tempDir, err := os.MkdirTemp(filepath.Join(homeDir, ".tmp"), "test-container-options-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	executor := container.New(
		container.WithContainerWorkDir(tempDir),
		container.WithContainerTimeout(10*time.Second),
		container.WithCleanContainers(false),
	)

	// Verify the options were set correctly
	assert.Equal(t, tempDir, executor.WorkDir)
	assert.Equal(t, 10*time.Second, executor.Timeout)
	assert.False(t, executor.CleanContainers)
}

func TestContainerCodeExecutor_NoDocker(t *testing.T) {
	// Mock a scenario where Docker is not available by creating an executor
	// and then testing the error case (this test will run even without Docker)

	// We can't easily mock isDockerAvailable without refactoring, so this test
	// is more of a documentation of expected behavior
	executor := container.New()

	// Test that the executor is created successfully
	assert.NotNil(t, executor)
	assert.Equal(t, 60*time.Second, executor.Timeout) // Default timeout
	assert.True(t, executor.CleanContainers)          // Default cleanup behavior
}

func TestContainerCodeExecutor_IntegrationTest(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available, skipping container integration tests")
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
	executor := container.New()
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
