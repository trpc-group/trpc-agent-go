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
Available tools: save_file, read_file, list_file, search_file, search_content, replace_content
==================================================
✅ Debug agent ready! Session: debug-session-1757927735

💡 Try asking questions like:
   - Save a file called 'hello.txt' with content 'Hello, World!'
   - Read the file 'hello.txt'
   - List all files in the directory
   - Search for files with pattern '*.txt'
   - Create a file called 'data.json' with JSON content
   - The current directory is a code project. Can you help me fix the bug?

ℹ️  Note: All file operations will be performed in the base directory

👤 You: The current directory is a code project. Can you help me fix the bug?
🤖 Assistant: I'll help you fix the bug in your code project. First, let me explore the project structure to understand what we're working with.

🔧 Tool call initiated:
   • list_file (ID: call_00_HbOP1G2E410C9TKlajhsPY6L)
     Arguments: {"path": "."}
🔄 Executing tools...
✅ Tool result (ID: call_00_HbOP1G2E410C9TKlajhsPY6L): {"base_directory":"project","path":".","files":["README.md","expect.txt","go.mod","input.txt","main.go","output.txt"],"folders":["counter"],"message":"Found 6 files and 1 folders in ."}

Let me examine the main files to understand the project structure and identify potential bugs:

🔧 Tool call initiated:
   • read_file (ID: call_00_HtevwNGY6sZVY0sGxym3txiJ)
     Arguments: {"file_name": "main.go"}
🔄 Executing tools...
✅ Tool result (ID: call_00_HtevwNGY6sZVY0sGxym3txiJ): {"base_directory":"project","file_name":"main.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n//\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package main is a example project with bug.\npackage main\n\nimport (\n\t\"log\"\n\t\"os\"\n\t\"strconv\"\n\t\"strings\"\n\n\t\"trpc.group/trpc-go/trpc-agent-go/examples/debugagent/project/counter\"\n)\n\nfunc main() {\n\tcontent, err := os.ReadFile(\"input.txt\")\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tn, err := strconv.Atoi(strings.TrimSpace(string(content)))\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tcounter := counter.GetCounter(n)\n\tos.WriteFile(\"output.txt\", []byte(strconv.Itoa(counter)), 0644)\n}\n","message":"Successfully read main.go, start line: 1, end line: 34, total lines: 34"}



🔧 Tool call initiated:
   • read_file (ID: call_00_sLkTomOHHTEkh4zHwjMTnTAa)
     Arguments: {"file_name": "go.mod"}
🔄 Executing tools...
✅ Tool result (ID: call_00_sLkTomOHHTEkh4zHwjMTnTAa): {"base_directory":"project","file_name":"go.mod","contents":"module trpc.group/trpc-go/trpc-agent-go/examples/debugagent/project\n\ngo 1.24.1\n","message":"Successfully read go.mod, start line: 1, end line: 4, total lines: 4"}



🔧 Tool call initiated:
   • read_file (ID: call_00_81p4bcqdUhV1hKtTuKFUASK8)
     Arguments: {"file_name": "counter/counter.go"}
🔄 Executing tools...
✅ Tool result (ID: call_00_81p4bcqdUhV1hKtTuKFUASK8): {"base_directory":"project","file_name":"counter/counter.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n//\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package counter implements a counter.\npackage counter\n\nimport \"sync/atomic\"\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tatomic.AddInt64(\u0026counter, 1)\n\t\t}()\n\t}\n\treturn int(counter)\n}\n","message":"Successfully read counter/counter.go, start line: 1, end line: 24, total lines: 24"}



🔧 Tool call initiated:
   • read_file (ID: call_00_vHV6YCLhCW70BW7YuU10ykGx)
     Arguments: {"file_name": "input.txt"}
🔄 Executing tools...
✅ Tool result (ID: call_00_vHV6YCLhCW70BW7YuU10ykGx): {"base_directory":"project","file_name":"input.txt","contents":"100000","message":"Successfully read input.txt, start line: 1, end line: 1, total lines: 1"}



🔧 Tool call initiated:
   • read_file (ID: call_00_WfDPQn8WomiYbRHTgCq0y3nI)
     Arguments: {"file_name": "expect.txt"}
🔄 Executing tools...
✅ Tool result (ID: call_00_WfDPQn8WomiYbRHTgCq0y3nI): {"base_directory":"project","file_name":"expect.txt","contents":"100000","message":"Successfully read expect.txt, start line: 1, end line: 1, total lines: 1"}



🔧 Tool call initiated:
   • read_file (ID: call_00_1VC04qSLOUSKYBVGvkAWJX2J)
     Arguments: {"file_name": "output.txt"}
🔄 Executing tools...
✅ Tool result (ID: call_00_1VC04qSLOUSKYBVGvkAWJX2J): {"base_directory":"project","file_name":"output.txt","contents":"97876","message":"Successfully read output.txt, start line: 1, end line: 1, total lines: 1"}

Now I can see the bug! The issue is in the `counter/counter.go` file. The problem is that the main function returns immediately after spawning goroutines, without waiting for them to complete. The goroutines are still running when the function returns, so the counter value is not fully incremented.

Let me also check if there's a README that might provide more context:

🔧 Tool call initiated:
   • read_file (ID: call_00_E8Os7PrurC09MQHPGcdy7X72)
     Arguments: {"file_name": "README.md"}
🔄 Executing tools...
✅ Tool result (ID: call_00_E8Os7PrurC09MQHPGcdy7X72): {"base_directory":"project","file_name":"README.md","contents":"input.txt: Program input\noutput.txt: Program output\nexpect.txt: Program expected output\n\nWe've discovered that the program's actual output doesn't match our expectations. Could you help us debug this?\n","message":"Successfully read README.md, start line: 1, end line: 6, total lines: 6"}

Perfect! The issue is clear now. The program reads 100000 from input.txt, spawns 100000 goroutines to increment a counter, but returns before all goroutines finish executing. This is why the output (97876) is less than the expected value (100000).

The fix is to add synchronization to wait for all goroutines to complete before returning. I'll use a `sync.WaitGroup` to fix this:

🔧 Tool call initiated:
   • replace_content (ID: call_00_EVnrOAunnIK53rENeMoRsbQ2)
     Arguments: {"file_name": "counter/counter.go", "old_string": "import \"sync/atomic\"\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tfor i := 0; i < n; i++ {\n\t\tgo func() {\n\t\t\tatomic.AddInt64(&counter, 1)\n\t\t}()\n\t}\n\treturn int(counter)\n}", "new_string": "import (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tvar wg sync.WaitGroup\n\t\n\tfor i := 0; i < n; i++ {\n\t\twg.Add(1)\n\t\tgo func() {\n\t\t\tdefer wg.Done()\n\t\t\tatomic.AddInt64(&counter, 1)\n\t\t}()\n\t}\n\t\n\twg.Wait()\n\treturn int(counter)\n}"}
🔄 Executing tools...
✅ Tool result (ID: call_00_EVnrOAunnIK53rENeMoRsbQ2): {"base_directory":"project","file_name":"counter/counter.go","message":"Successfully replaced 1 of 1 occurrence(s) in 'counter/counter.go'"}

Now let's test the fix by running the program:

🔧 Tool call initiated:
   • save_file (ID: call_00_GvwRuY58gI6EWVXfQUcAlOg3)
     Arguments: {"file_name": "run.sh", "contents": "#!/bin/bash\n# Clean up previous output\nrm -f output.txt\n\n# Run the program\ngo run .\n\n# Check the result\necho \"Expected: $(cat expect.txt)\"\necho \"Actual: $(cat output.txt)\"\n\n# Compare with expected output\nif [ \"$(cat expect.txt)\" = \"$(cat output.txt)\" ]; then\n    echo \"\u2705 Test PASSED - Output matches expected value\"\nelse\n    echo \"\u274c Test FAILED - Output does not match expected value\"\n    exit 1\nfi", "overwrite": true}
🔄 Executing tools...
✅ Tool result (ID: call_00_GvwRuY58gI6EWVXfQUcAlOg3): {"base_directory":"project","file_name":"run.sh","message":"Successfully saved: run.sh"}

```bash
bash ./run.sh
```
✅ Code execution: ```bash
bash ./run.sh
```


✅ Code execution: Code execution result:
Expected: 100000
Actual: 100000
✅ Test PASSED - Output matches expected value
``````

## How It Works

1. **Setup**: Creates an LLM agent with file tools and a local CodeExecutor.
2. **User Input**: You ask for debugging help or file tasks under `-base-dir`.
3. **Tool/Execution Decision**: The agent decides to use file tools or execute code:
   - File tools: `read_file`, `list_file`, `search_file`, `search_content`, `replace_content`, `save_file`.
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
