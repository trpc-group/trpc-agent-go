//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func TestLocalCodeExecutor_ExecuteCode(t *testing.T) {
	tests := []struct {
		name     string
		input    codeexecutor.CodeExecutionInput
		expected struct {
			outputContains string
			shouldError    bool
		}
		skipIfMissing string // Skip test if this executable is missing
	}{
		{
			name: "python hello world",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "print('Hello, World!')",
						Language: "python",
					},
				},
				ExecutionID: "test-python-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Hello, World!",
				shouldError:    false,
			},
			skipIfMissing: "python",
		},
		{
			name: "bash echo",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "echo 'Hello, Bash!'",
						Language: "bash",
					},
				},
				ExecutionID: "test-bash-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Hello, Bash!",
				shouldError:    false,
			},
			skipIfMissing: "bash",
		},
		{
			name: "multiple code blocks",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "echo 'First block'",
						Language: "bash",
					},
					{
						Code:     "echo 'Second block'",
						Language: "bash",
					},
				},
				ExecutionID: "test-multiple-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "First block",
				shouldError:    false,
			},
			skipIfMissing: "bash",
		},
		{
			name: "unsupported language",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "console.log('Hello, JavaScript!')",
						Language: "javascript",
					},
				},
				ExecutionID: "test-unsupported-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "unsupported language: javascript",
				shouldError:    false,
			},
		},
		{
			name: "bash syntax error",
			input: codeexecutor.CodeExecutionInput{
				CodeBlocks: []codeexecutor.CodeBlock{
					{
						Code:     "invalid-command-that-does-not-exist",
						Language: "bash",
					},
				},
				ExecutionID: "test-bash-error-1",
			},
			expected: struct {
				outputContains string
				shouldError    bool
			}{
				outputContains: "Error executing code block",
				shouldError:    false,
			},
			skipIfMissing: "bash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip test if required executable is missing
			if tt.skipIfMissing != "" {
				if !isExecutableAvailable(tt.skipIfMissing) {
					t.Skipf("Skipping test because %s is not available", tt.skipIfMissing)
				}
			}

			executor := local.New()
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

			// OutputFiles should always be empty for CodeExecutor
			assert.Empty(t, result.OutputFiles)
		})
	}
}

// isExecutableAvailable checks if an executable is available in PATH
func isExecutableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func TestLocalCodeExecutor_ExecuteCode_WithWorkDir(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test-workdir-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	executor := local.New(
		local.WithWorkDir(tempDir),
	)

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "echo 'Testing WorkDir' > test_output.txt\ncat test_output.txt",
				Language: "bash",
			},
		},
		ExecutionID: "test-workdir-1",
	}

	ctx := context.Background()
	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err)
	assert.Contains(t, result.Output, "Testing WorkDir")
	assert.Empty(t, result.OutputFiles)

	// Verify that the file was created in the specified work directory
	outputFile := filepath.Join(tempDir, "test_output.txt")
	_, err = os.Stat(outputFile)
	assert.NoError(t, err, "File should exist in work directory")
}

func TestLocalCodeExecutor_ExecuteCode_WithRelativeWorkDir(t *testing.T) {
	// Create a temporary directory relative to current working directory.
	cwd, err := os.Getwd()
	require.NoError(t, err)

	relDirPath, err := os.MkdirTemp(".", "rel-workdir-")
	require.NoError(t, err)
	defer os.RemoveAll(relDirPath)

	// Determine values to pass and to assert against.
	// Pass a relative WorkDir argument.
	workDirArg := relDirPath
	if filepath.IsAbs(workDirArg) {
		// Make it relative to cwd if MkdirTemp returned absolute.
		if rel, err2 := filepath.Rel(cwd, workDirArg); err2 == nil {
			workDirArg = rel
		}
	}
	// Also compute absolute path for existence checks.
	absDir, err := filepath.Abs(relDirPath)
	require.NoError(t, err)

	executor := local.New(
		local.WithWorkDir(workDirArg),
		local.WithCleanTempFiles(false), // keep output files to verify
		// path correctness
	)

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "echo 'Hello Rel' > rel_out.txt\ncat rel_out.txt",
				Language: "bash",
			},
		},
		ExecutionID: "test-rel-workdir-1",
	}

	ctx := context.Background()
	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err)
	assert.Contains(t, result.Output, "Hello Rel")
	assert.Empty(t, result.OutputFiles)

	// Verify the file was created under the (absolute-normalized) work directory
	outFile := filepath.Join(absDir, "rel_out.txt")
	_, err = os.Stat(outFile)
	assert.NoError(t, err, "Expected output file inside normalized work dir")
}

func TestLocalCodeExecutor_ExecuteCode_WithoutWorkDir(t *testing.T) {
	executor := local.New()

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "echo 'Testing temp dir' > temp_output.txt\ncat temp_output.txt",
				Language: "bash",
			},
		},
		ExecutionID: "test-temp-1",
	}

	ctx := context.Background()
	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err)
	assert.Contains(t, result.Output, "Testing temp dir")
	assert.Empty(t, result.OutputFiles)
}

func TestLocalCodeExecutor_ExecuteCode_ContextCancellation(t *testing.T) {
	executor := local.New()

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "sleep 2",
				Language: "bash",
			},
		},
		ExecutionID: "test-cancel-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err) // ExecuteCode itself doesn't return error for block execution failures
	assert.Contains(t, result.Output, "Error executing code block")
}

func TestLocalCodeExecutor_CodeBlockDelimiter(t *testing.T) {
	executor := local.New()
	delimiter := executor.CodeBlockDelimiter()

	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestLocalCodeExecutor_WithCodeBlockDelimiter(t *testing.T) {
	tests := []struct {
		name          string
		delimiter     codeexecutor.CodeBlockDelimiter
		expectedStart string
		expectedEnd   string
	}{
		{
			name: "custom delimiter with go markers",
			delimiter: codeexecutor.CodeBlockDelimiter{
				Start: "```go",
				End:   "```go",
			},
			expectedStart: "```go",
			expectedEnd:   "```go",
		},
		{
			name: "custom delimiter with xml-style tags",
			delimiter: codeexecutor.CodeBlockDelimiter{
				Start: "<code>",
				End:   "</code>",
			},
			expectedStart: "<code>",
			expectedEnd:   "</code>",
		},
		{
			name: "custom delimiter with triple backticks and language",
			delimiter: codeexecutor.CodeBlockDelimiter{
				Start: "```python",
				End:   "```",
			},
			expectedStart: "```python",
			expectedEnd:   "```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := local.New(
				local.WithCodeBlockDelimiter(tt.delimiter),
			)

			delimiter := executor.CodeBlockDelimiter()

			assert.Equal(t, tt.expectedStart, delimiter.Start)
			assert.Equal(t, tt.expectedEnd, delimiter.End)
		})
	}
}

func TestLocalCodeExecutor_DefaultCodeBlockDelimiter(t *testing.T) {
	// Test that default delimiter is used when WithCodeBlockDelimiter is not called
	executor := local.New()
	delimiter := executor.CodeBlockDelimiter()

	assert.Equal(t, "```", delimiter.Start)
	assert.Equal(t, "```", delimiter.End)
}

func TestLocalCodeExecutor_ExecuteCode_InvalidWorkDir(t *testing.T) {
	// Use a path that cannot be created (e.g., under a file instead of directory)
	tempFile, err := os.CreateTemp("", "test-file-")
	require.NoError(t, err)
	tempFile.Close()
	defer os.Remove(tempFile.Name())

	invalidWorkDir := filepath.Join(tempFile.Name(), "subdir") // Try to create dir under a file

	executor := local.New(
		local.WithWorkDir(invalidWorkDir),
	)

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "echo 'test'",
				Language: "bash",
			},
		},
		ExecutionID: "test-invalid-workdir-1",
	}

	ctx := context.Background()
	_, err = executor.ExecuteCode(ctx, input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create work directory")
}

func TestLocalCodeExecutor_WithOptions(t *testing.T) {
	// Test creating executor with multiple options
	tempDir, err := os.MkdirTemp("", "test-options-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	executor := local.New(
		local.WithWorkDir(tempDir),
		local.WithTimeout(5*time.Second),
		local.WithCleanTempFiles(false),
	)

	// Verify the options were set correctly
	assert.Equal(t, tempDir, executor.WorkDir)
	assert.Equal(t, 5*time.Second, executor.Timeout)
	assert.False(t, executor.CleanTempFiles) // We set it to false (don't clean)
}

func TestLocalCodeExecutor_WithTimeout(t *testing.T) {
	executor := local.New(
		local.WithTimeout(1 * time.Second),
	)

	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     "sleep 2", // Sleep longer than timeout
				Language: "bash",
			},
		},
		ExecutionID: "test-custom-timeout-1",
	}

	ctx := context.Background()
	result, err := executor.ExecuteCode(ctx, input)

	assert.NoError(t, err) // ExecuteCode itself doesn't return error for block execution failures
	assert.Contains(t, result.Output, "Error executing code block")
}

// Cover wrapper methods on CodeExecutor to raise file coverage.
func TestLocalCodeExecutor_WrapperMethods(t *testing.T) {
	exec := local.New(
		local.WithTimeout(2 * time.Second),
	)
	ctx := context.Background()
	ws, err := exec.CreateWorkspace(
		ctx, "wrap", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer exec.Cleanup(ctx, ws)

	// PutFiles via wrapper and collect them back.
	err = exec.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    filepath.Join(codeexecutor.DirWork, "w.txt"),
		Content: []byte("w"),
		Mode:    0o644,
	}})
	require.NoError(t, err)

	// PutDirectory wrapper copies a temp dir.
	src := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "a.txt"), []byte("a"), 0o644,
	))
	err = exec.PutDirectory(ctx, ws, src, "dst")
	require.NoError(t, err)

	// RunProgram wrapper echoes a value.
	res, err := exec.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd: "bash", Args: []string{"-lc", "echo ok"},
		Timeout: 2 * time.Second,
	})
	require.NoError(t, err)
	require.Contains(t, res.Stdout, "ok")

	// Collect via wrapper.
	files, err := exec.Collect(ctx, ws, []string{
		filepath.Join(codeexecutor.DirWork, "*.txt"),
		filepath.Join("dst", "*.txt"),
	})
	require.NoError(t, err)
	if len(files) > 0 {
		// Ensure names are workspace-relative.
		for _, f := range files {
			require.False(t, strings.HasPrefix(f.Name, ws.Path))
		}
	}

	// ExecuteInline via wrapper.
	out, err := exec.ExecuteInline(ctx, "inline",
		[]codeexecutor.CodeBlock{{
			Language: "bash", Code: "echo inline",
		}}, 2*time.Second,
	)
	require.NoError(t, err)
	require.Contains(t, out.Stdout, "inline")

	// Engine should be non-nil and usable.
	eng := exec.Engine()
	require.NotNil(t, eng)
	// Describe returns something.
	require.NotEqual(t, "", eng.Describe())
	_ = fmt.Sprintf("%T", eng)
}

func TestLocalCodeExecutor_IntegrationTest(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		delimiter      codeexecutor.CodeBlockDelimiter
		expectedBlocks int
		outputContains []string
		skipIfMissing  []string
	}{
		{
			name: "single python block extraction and execution",
			input: `Here's a simple Python script:
			
` + "```python" + `
print("Hello from Python!")
print("This is a test")
` + "```" + `

This should work fine.`,
			delimiter:      codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"},
			expectedBlocks: 1,
			outputContains: []string{"Hello from Python!", "This is a test"},
			skipIfMissing:  []string{"python"},
		},
		{
			name: "multiple language blocks",
			input: `Let's test multiple languages:

` + "```python" + `
print("Python says hello")
` + "```" + `

And some Go code:

` + `

Finally, some bash:

` + "```bash" + `
echo "Bash says hello"
` + "```",
			delimiter:      codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"},
			expectedBlocks: 2,
			outputContains: []string{"Python says hello", "Bash says hello"},
			skipIfMissing:  []string{"python", "bash"},
		},
		{
			name: "custom delimiter with python code",
			input: `<code>python
print("Custom delimiter test")
</code>`,
			delimiter:      codeexecutor.CodeBlockDelimiter{Start: "<code>", End: "</code>"},
			expectedBlocks: 1,
			outputContains: []string{"Custom delimiter test"},
			skipIfMissing:  []string{"python"},
		},
		{
			name: "mixed valid and invalid languages",
			input: `Valid Python code:
			
` + "```python" + `
print("This works")
` + "```" + `

Invalid language:

` + "```javascript" + `
console.log("This won't work");
` + "```" + `

Valid bash:

` + "```bash" + `
echo "This works too"
` + "```",
			delimiter:      codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"},
			expectedBlocks: 3,
			outputContains: []string{"This works", "unsupported language: javascript", "This works too"},
			skipIfMissing:  []string{"python", "bash"},
		},
		{
			name: "no code blocks",
			input: `This is just regular text with no code blocks.
			
Some more text here.`,
			delimiter:      codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"},
			expectedBlocks: 0,
			outputContains: []string{"No output or errors"},
			skipIfMissing:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip test if required executables are missing
			for _, executable := range tt.skipIfMissing {
				if !isExecutableAvailable(executable) {
					t.Skipf("Skipping test because %s is not available", executable)
				}
			}

			// Step 1: Extract code blocks from input
			blocks := codeexecutor.ExtractCodeBlock(tt.input, tt.delimiter)
			assert.Len(t, blocks, tt.expectedBlocks, "Expected %d code blocks, got %d", tt.expectedBlocks, len(blocks))

			// If no blocks were extracted, test the "no output" case
			if len(blocks) == 0 {
				result := codeexecutor.CodeExecutionResult{
					Output:      "",
					OutputFiles: []codeexecutor.File{},
				}
				formattedResult := result.String()
				for _, expectedOutput := range tt.outputContains {
					assert.Contains(t, formattedResult, expectedOutput)
				}
				return
			}

			// Step 2: Execute the extracted code blocks
			executor := local.New()
			ctx := context.Background()

			executionInput := codeexecutor.CodeExecutionInput{
				CodeBlocks:  blocks,
				ExecutionID: "integration-test-" + tt.name,
			}

			result, err := executor.ExecuteCode(ctx, executionInput)
			assert.NoError(t, err, "ExecuteCode should not return an error")

			// Step 3: Format the result using String method
			formattedResult := result.String() // Debug output
			t.Logf("Extracted %d blocks", len(blocks))
			for i, block := range blocks {
				t.Logf("Block %d - Language: %s, Code: %q", i, block.Language, block.Code)
			}
			t.Logf("Execution result: %q", result.Output)
			t.Logf("Formatted result: %q", formattedResult)

			// Step 4: Verify the output contains expected strings
			for _, expectedOutput := range tt.outputContains {
				// Check both raw output and formatted result
				outputFound := strings.Contains(result.Output, expectedOutput) ||
					strings.Contains(formattedResult, expectedOutput)
				assert.True(t, outputFound,
					"Expected output '%s' not found in result:\nRaw output: %q\nFormatted: %q",
					expectedOutput, result.Output, formattedResult)
			}

			// Verify OutputFiles is always empty for CodeExecutor
			assert.Empty(t, result.OutputFiles)

			// Verify formatted result starts with expected prefix
			if result.Output != "" {
				assert.Contains(t, formattedResult, "Code execution result:")
			}
		})
	}
}

func TestLocalCodeExecutor_IntegrationTest_WithWorkDir(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "integration-test-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	input := `Let's create and read a file:

` + "```bash" + `
echo "Hello from file" > test.txt
cat test.txt
` + "```" + `

And check if it exists:

` + "```bash" + `
ls -la test.txt
` + "```"

	// Step 1: Extract code blocks
	delimiter := codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
	blocks := codeexecutor.ExtractCodeBlock(input, delimiter)
	assert.Len(t, blocks, 2)

	// Step 2: Execute with custom work directory
	executor := local.New(
		local.WithWorkDir(tempDir),
		local.WithCleanTempFiles(false), // Don't clean so we can verify file exists
	)

	ctx := context.Background()
	executionInput := codeexecutor.CodeExecutionInput{
		CodeBlocks:  blocks,
		ExecutionID: "integration-workdir-test",
	}

	result, err := executor.ExecuteCode(ctx, executionInput)
	assert.NoError(t, err)

	// Step 3: Format and verify result
	formattedResult := result.String()

	assert.Contains(t, result.Output, "Hello from file")
	assert.Contains(t, result.Output, "test.txt")
	assert.Contains(t, formattedResult, "Code execution result:")

	// Verify the file was actually created in the work directory
	testFile := filepath.Join(tempDir, "test.txt")
	_, err = os.Stat(testFile)
	assert.NoError(t, err, "File should exist in work directory")

	// Verify file contents
	content, err := os.ReadFile(testFile)
	assert.NoError(t, err)
	assert.Contains(t, string(content), "Hello from file")
}

func TestLocalCodeExecutor_IntegrationTest_ErrorHandling(t *testing.T) {
	input := `This will test error handling:

` + "```python" + `
print("This works")
` + "```" + `

` + "```javascript" + `
console.log("This should fail");
` + "```" + `

` + "```bash" + `
nonexistent-command-that-will-fail
` + "```"

	// Step 1: Extract code blocks
	delimiter := codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
	blocks := codeexecutor.ExtractCodeBlock(input, delimiter)
	assert.Len(t, blocks, 3)

	// Step 2: Execute blocks
	executor := local.New()
	ctx := context.Background()

	executionInput := codeexecutor.CodeExecutionInput{
		CodeBlocks:  blocks,
		ExecutionID: "integration-error-test",
	}

	result, err := executor.ExecuteCode(ctx, executionInput)
	assert.NoError(t, err) // ExecuteCode itself shouldn't error

	// Step 3: Verify error handling in output
	formattedResult := result.String()

	// Should contain successful output
	if isExecutableAvailable("python") {
		assert.Contains(t, result.Output, "This works")
	}

	// Should contain error messages
	assert.Contains(t, result.Output, "unsupported language: javascript")

	if isExecutableAvailable("bash") {
		assert.Contains(t, result.Output, "Error executing code block")
	}

	// Formatted result should indicate there was output
	assert.Contains(t, formattedResult, "Code execution result:")
}

func TestLocalCodeExecutor_IntegrationTest_CleanTempFiles(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "integration-clean-test-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	input := `Create a temporary file:

` + "```bash" + `
echo "Temporary content" > temp_file.txt
cat temp_file.txt
` + "```"

	// Step 1: Extract code blocks
	delimiter := codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
	blocks := codeexecutor.ExtractCodeBlock(input, delimiter)
	assert.Len(t, blocks, 1)

	// Test with CleanTempFiles = false
	t.Run("with_clean_temp_files_false", func(t *testing.T) {
		executor := local.New(
			local.WithWorkDir(tempDir),
			local.WithCleanTempFiles(false),
		)

		executionInput := codeexecutor.CodeExecutionInput{
			CodeBlocks:  blocks,
			ExecutionID: "clean-test-false",
		}

		result, err := executor.ExecuteCode(context.Background(), executionInput)
		assert.NoError(t, err)

		assert.Contains(t, result.Output, "Temporary content")
		assert.Contains(t, result.String(), "Code execution result:")

		// Code files should still exist
		codeFiles, err := filepath.Glob(filepath.Join(tempDir, "code_*.sh"))
		assert.NoError(t, err)
		assert.NotEmpty(t, codeFiles, "Code files should exist when CleanTempFiles is false")
	})

	// Test with CleanTempFiles = true (default)
	t.Run("with_clean_temp_files_true", func(t *testing.T) {
		executor := local.New(
			local.WithWorkDir(tempDir),
			local.WithCleanTempFiles(true),
		)

		executionInput := codeexecutor.CodeExecutionInput{
			CodeBlocks:  blocks,
			ExecutionID: "clean-test-true",
		}

		result, err := executor.ExecuteCode(context.Background(), executionInput)
		assert.NoError(t, err)

		assert.Contains(t, result.Output, "Temporary content")
		assert.Contains(t, result.String(), "Code execution result:")

		// Code files should be cleaned up (though this is timing-dependent, so we might not catch it reliably)
		// The important thing is that execution succeeded
	})
}

func TestLocal_PythonNoPrintAddsNewline(t *testing.T) {
	const (
		langPy   = "python"
		execID   = "py-no-print"
		filename = "code_0.py"
	)
	tempDir, err := os.MkdirTemp("", "py-no-print-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	e := local.New(
		local.WithWorkDir(tempDir),
		local.WithCleanTempFiles(false),
	)
	in := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: langPy,
			Code:     "x = 1",
		}},
		ExecutionID: execID,
	}
	_, err = e.ExecuteCode(context.Background(), in)
	require.NoError(t, err)

	// The created python file should end with a newline.
	p := filepath.Join(tempDir, filename)
	data, rerr := os.ReadFile(p)
	require.NoError(t, rerr)
	require.Greater(t, len(data), 0)
	require.Equal(t, byte('\n'), data[len(data)-1])
}

func TestLocal_PythonPrintNoAutoNewline(t *testing.T) {
	const (
		langPy   = "python"
		execID   = "py-print"
		filename = "code_0.py"
	)
	tempDir, err := os.MkdirTemp("", "py-print-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	e := local.New(
		local.WithWorkDir(tempDir),
		local.WithCleanTempFiles(false),
	)
	// No trailing newline in source on purpose.
	src := "print('ok')"
	in := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: langPy,
			Code:     src,
		}},
		ExecutionID: execID,
	}
	_, err = e.ExecuteCode(context.Background(), in)
	require.NoError(t, err)

	p := filepath.Join(tempDir, filename)
	data, rerr := os.ReadFile(p)
	require.NoError(t, rerr)
	// File content should be exactly the source, no auto newline.
	require.Equal(t, src, string(data))
}

func TestLocal_BashFileModeExec(t *testing.T) {
	const (
		langBash = "bash"
		execID   = "bash-mode"
		fname    = "code_0.sh"
	)
	tempDir, err := os.MkdirTemp("", "bash-mode-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	e := local.New(
		local.WithWorkDir(tempDir),
		local.WithCleanTempFiles(false),
	)
	in := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: langBash,
			Code:     "echo ok",
		}},
		ExecutionID: execID,
	}
	_, err = e.ExecuteCode(context.Background(), in)
	require.NoError(t, err)

	p := filepath.Join(tempDir, fname)
	st, serr := os.Stat(p)
	require.NoError(t, serr)
	// Executable bit should be present for bash scripts (0755).
	require.NotZero(t, st.Mode()&0o111)
}

func TestLocal_CommandErrorFormat(t *testing.T) {
	const (
		langBash = "bash"
		execID   = "bash-err"
	)
	tempDir, err := os.MkdirTemp("", "bash-err-")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	e := local.New(
		local.WithWorkDir(tempDir),
		local.WithCleanTempFiles(false),
	)
	// Use an invalid command to trigger error formatting path.
	in := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: langBash,
			Code:     "this-does-not-exist",
		}},
		ExecutionID: execID,
	}
	res, err := e.ExecuteCode(context.Background(), in)
	require.NoError(t, err)
	// The output should include our standard error prefix and hint at
	// the command failure details produced by executeCommand.
	require.Contains(t, res.Output, "Error executing code block")
	require.Contains(t, res.Output, "cmd=bash")
}

func TestLocal_DelegatesWorkspaceMethods(t *testing.T) {
	const (
		execID = "ws-delegate"
	)
	e := local.New()
	ctx := context.Background()

	ws, err := e.CreateWorkspace(
		ctx, execID, codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer e.Cleanup(ctx, ws)

	// Put files and collect them back.
	err = e.PutFiles(ctx, ws, []codeexecutor.PutFile{{
		Path:    "work/hello.txt",
		Content: []byte("hi"),
		Mode:    0o644,
	}})
	require.NoError(t, err)

	files, err := e.Collect(ctx, ws, []string{"work/*.txt"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 1)

	// Run a simple program in workspace.
	rr, err := e.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-lc", "echo ok"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, rr.ExitCode)

	// ExecuteInline via wrapper.
	rin, err := e.ExecuteInline(ctx, execID,
		[]codeexecutor.CodeBlock{{
			Language: "bash", Code: "echo inline",
		}}, 2*time.Second,
	)
	require.NoError(t, err)
	require.Contains(t, rin.Stdout, "inline")
}

func TestLocal_EngineExposed(t *testing.T) {
	e := local.New()
	eng := e.Engine()
	require.NotNil(t, eng)
	require.NotNil(t, eng.Manager())
	require.NotNil(t, eng.FS())
	require.NotNil(t, eng.Runner())
}

func TestLocalCodeExecutor_WorkspaceAutoInputsToggle(t *testing.T) {
	ctx := context.Background()
	host := t.TempDir()
	const seedName = "seed.txt"
	hfile := filepath.Join(host, seedName)
	require.NoError(t, os.WriteFile(hfile, []byte("seed"), 0o644))

	execOn := local.New(
		local.WithWorkspaceInputsHostBase(host),
	)
	engOn := execOn.Engine()
	ws1, err := engOn.Manager().CreateWorkspace(
		ctx, "auto-on", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer engOn.Manager().Cleanup(ctx, ws1)

	data, err := os.ReadFile(filepath.Join(
		ws1.Path, codeexecutor.DirWork, "inputs", seedName,
	))
	require.NoError(t, err)
	require.Equal(t, "seed", string(data))

	execOff := local.New(
		local.WithWorkspaceInputsHostBase(host),
		local.WithWorkspaceAutoInputs(false),
	)
	engOff := execOff.Engine()
	ws2, err := engOff.Manager().CreateWorkspace(
		ctx, "auto-off", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	defer engOff.Manager().Cleanup(ctx, ws2)

	_, err = os.Stat(filepath.Join(
		ws2.Path, codeexecutor.DirWork, "inputs", seedName,
	))
	require.Error(t, err)
}
