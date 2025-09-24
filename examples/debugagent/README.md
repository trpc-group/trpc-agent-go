# Debug Agent Example

This example demonstrates how to use file operation tools with an AI agent for interactive conversations. The tool provides comprehensive file system capabilities including saving files, reading files, listing files, and searching files.

## Background

The debugagent project contains a Go program with a bug, and builds an AI agent with file operation tools to help fix this bug.

There's a classic Go concurrency issue in `project/counter/counter.go`:

```go
func GetCounter(n int) int {
	var counter int64
	for i := 0; i < n; i++ {
		go func() {
			atomic.AddInt64(&counter, 1)
		}()
	}
	return int(counter)
}
```

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work. The AI agent will use file operation tools to help fix bugs in the project.

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |
| `-base-dir` | Base directory for file operations | `.` |

## Running the Example

### Using environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Optional
go run main.go
```

### Using custom model:

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go -model gpt-4o-mini
```

### Example with different base URL (for OpenAI-compatible APIs):

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
go run main.go -model deepseek-chat
```

### Specifying base directory:

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go -base-dir ./project
```

## Example

``````log
🐞 Debug Agent Demo
Model: deepseek-chat
Base Directory: ./project
Type 'exit' to end the conversation
Available tools: save_file, read_file, read_multiple_files, list_file, search_file, search_content, replace_content
==================================================
✅ Debug agent ready! Session: debug-session-1758004914

💡 Try asking questions like:
   - Save a file called 'hello.txt' with content 'Hello, World!'
   - Read the file 'hello.txt'
   - List all files in the directory
   - Search for files with pattern '*.txt'
   - Create a file called 'data.json' with JSON content
   - The current directory is a code project. Can you help me fix the bug?

ℹ️  Note: All file operations will be performed in the base directory

👤 You: 🤖 Assistant: I'd be happy to help you fix the bug! To get started, I need to understand the project structure and the codebase. Let me first explore the current directory to see what kind of project this is.

🔧 Tool call initiated:
   • list_file (ID: call_00_NgmwIy6qMBDas1fm0CYPU9PZ)
     Arguments: {"path": ""}
🔄 Executing tools...
✅ Tool result (ID: call_00_NgmwIy6qMBDas1fm0CYPU9PZ): {"base_directory":"project","path":"","files":["README.md","expect.txt","go.mod","input.txt","main.go","output.txt"],"folders":["counter"],"message":"Found 6 files and 1 folders in base directory"}

This appears to be a Go project. Let me examine the main files to understand the structure and identify the bug:

🔧 Tool call initiated:
   • read_multiple_files (ID: call_00_IPpAT8AFnUG3ZmzcKstPJoZ5)
     Arguments: {"patterns": ["README.md", "go.mod", "main.go"]}
🔄 Executing tools...
✅ Tool result (ID: call_00_IPpAT8AFnUG3ZmzcKstPJoZ5): {"base_directory":"project","files":[{"file_name":"README.md","contents":"input.txt: Program input\noutput.txt: Program output\nexpect.txt: Program expected output\n\nWe've discovered that the program's actual output doesn't match our expectations. Could you help us debug this?\n","message":"Successfully read README.md, total lines: 6"},{"file_name":"go.mod","contents":"module trpc.group/trpc-go/trpc-agent-go/examples/debugagent/project\n\ngo 1.24.1\n","message":"Successfully read go.mod, total lines: 4"},{"file_name":"main.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n//\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package main is a example project with bug.\npackage main\n\nimport (\n\t\"log\"\n\t\"os\"\n\t\"strconv\"\n\t\"strings\"\n\n\t\"trpc.group/trpc-go/trpc-agent-go/examples/debugagent/project/counter\"\n)\n\nfunc main() {\n\tcontent, err := os.ReadFile(\"input.txt\")\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tn, err := strconv.Atoi(strings.TrimSpace(string(content)))\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tcounter := counter.GetCounter(n)\n\tos.WriteFile(\"output.txt\", []byte(strconv.Itoa(counter)), 0644)\n}\n","message":"Successfully read main.go, total lines: 34"}],"message":"Read 3 file(s)"}

Now let me check the counter package and the input/output files to understand the issue:

🔧 Tool call initiated:
   • read_multiple_files (ID: call_00_07r9jkkklLsKwFQNFF7rc9Yw)
     Arguments: {"patterns": ["counter/*.go", "input.txt", "output.txt", "expect.txt"]}
🔄 Executing tools...
✅ Tool result (ID: call_00_07r9jkkklLsKwFQNFF7rc9Yw): {"base_directory":"project","files":[{"file_name":"counter/counter.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n//\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package counter implements a counter.\npackage counter\n\nimport \"sync/atomic\"\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tatomic.AddInt64(\u0026counter, 1)\n\t\t}()\n\t}\n\treturn int(counter)\n}\n","message":"Successfully read counter/counter.go, total lines: 24"},{"file_name":"expect.txt","contents":"100000","message":"Successfully read expect.txt, total lines: 1"},{"file_name":"input.txt","contents":"100000","message":"Successfully read input.txt, total lines: 1"},{"file_name":"output.txt","contents":"97876","message":"Successfully read output.txt, total lines: 1"}],"message":"Read 4 file(s)"}

Now I can see the issue! The problem is in the `counter/counter.go` file. The program is spawning multiple goroutines to increment a counter using `atomic.AddInt64`, but it's not waiting for all goroutines to complete before returning the result.

The bug is that the `GetCounter` function returns immediately after starting all the goroutines, without waiting for them to finish. This means the counter value returned is incomplete - some goroutines haven't had time to execute yet.

Let me fix this by adding proper synchronization using a WaitGroup:

🔧 Tool call initiated:
   • replace_content (ID: call_00_i62KTD9uzX0enr32vjsGKkrW)
     Arguments: {"file_name": "counter/counter.go", "old_string": "import \"sync/atomic\"\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tfor i := 0; i < n; i++ {\n\t\tgo func() {\n\t\t\tatomic.AddInt64(&counter, 1)\n\t\t}()\n\t}\n\treturn int(counter)\n}", "new_string": "import (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tvar wg sync.WaitGroup\n\t\n\tfor i := 0; i < n; i++ {\n\t\twg.Add(1)\n\t\tgo func() {\n\t\t\tdefer wg.Done()\n\t\t\tatomic.AddInt64(&counter, 1)\n\t\t}()\n\t}\n\t\n\twg.Wait()\n\treturn int(counter)\n}"}
🔄 Executing tools...
✅ Tool result (ID: call_00_i62KTD9uzX0enr32vjsGKkrW): {"base_directory":"project","file_name":"counter/counter.go","message":"Successfully replaced 1 of 1 occurrence(s) in 'counter/counter.go'"}

Now let me run the program to test the fix:

🔧 Tool call initiated:
   • save_file (ID: call_00_utm8PWsrTczD4BNZYQzT9RmJ)
     Arguments: {"file_name": "run.sh", "contents": "#!/bin/bash\n# Run the Go program\ngo run .\n# Compare output with expected result\necho \"Expected: $(cat expect.txt)\"\necho \"Actual: $(cat output.txt)\"\nif diff -q output.txt expect.txt > /dev/null; then\n    echo \"\u2705 Test passed! Output matches expected result.\"\nelse\n    echo \"\u274c Test failed! Output does not match expected result.\"\n    diff output.txt expect.txt\nfi", "overwrite": true}
🔄 Executing tools...
✅ Tool result (ID: call_00_utm8PWsrTczD4BNZYQzT9RmJ): {"base_directory":"project","file_name":"run.sh","message":"Successfully saved: run.sh"}

```bash
bash ./run.sh
```
✅ Code execution:
 ```bash
bash ./run.sh
```


✅ Code execution:
 Code execution result:
Expected: 100000
Actual: 100000
✅ Test passed! Output matches expected result.
``````

## How It Works

1. **Setup**: Creates an LLM agent with file tools and a local CodeExecutor.
2. **User Input**: You ask for debugging help or file tasks under `-base-dir`.
3. **Tool/Execution Decision**: The agent decides to use file tools or execute code:
   - File tools: `read_file`, `list_file`, `search_file`, `search_content`, `replace_content`, `save_file`, `read_multiple_files`.
   - Code execution: emit a single fenced `bash` block, or write a script then immediately run it.
4. **File Operation Execution** (when applicable): File tools run and return structured results.
5. **Code Execution** (when applicable): The CodeExecution processor extracts the fenced block and runs it in the working dir, then emits two post‑processing events — one with the code, one with the result. The UI prints the result cleanly without duplicating the code block.
6. **Response Generation**: The agent summarizes results and proposes next steps (further reads/edits or another run).

## Security Features

### Path Validation

- **Relative Paths**: Only allows paths relative to the base directory
- **Path Traversal Protection**: Prevents directory traversal using `..`
- **Absolute Path Restrictions**: Does not allow absolute paths

### Permission Control

- **Base Directory Limitation**: All file operations are restricted to the specified base directory
- **File Permissions**: New files are created with 644 permissions (rw-r--r--)
- **Directory Permissions**: New directories are created with 755 permissions (rwxr-xr-x)

## Interactive Features

- **Streaming Response**: Real-time display of file operation process and results
- **Tool Visualization**: Clear indication when file operations are performed
- **Multi-turn Conversation**: Maintains context across multiple operations
- **Error Handling**: Graceful handling of file operation failures or empty results

This example showcases how AI agents can be enhanced with file operation capabilities to help users manage and fix code files, particularly in debugging and code maintenance scenarios.
