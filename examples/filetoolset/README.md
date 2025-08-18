# File Tool Set Example

This example demonstrates how to use file operation tools with an AI agent for interactive conversations. The tool provides comprehensive file system capabilities including saving files, reading files, listing files, and searching files.

## Background

The filetoolset project contains a Go program with a bug, and builds an AI agent with file operation tools to help fix this bug.

### The Bug in the Project

There's a classic Go concurrency issue in `project/counter/counter.go`:

```go
func GetCounter(n int) int {
	counter := 0
	for i := 0; i < n; i++ {
		go func() {
			counter++  // Data race: multiple goroutines accessing shared variable
		}()
	}
	return counter
}
```

**Problem Analysis:**
- Multiple goroutines are accessing the shared variable `counter` without synchronization
- This causes a data race, resulting in inaccurate output
- Expected output: 100000, Actual output: 97876 (or other random values)

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

## Features

### 📁 File Operation Tools

The tool provides comprehensive file system operation capabilities:

#### 1. `save_file` - Save File

**Input:**
```json
{
  "contents": "string",
  "file_name": "string",
  "overwrite": "boolean"
}
```

**Output:**
```json
{
  "base_directory": "string",
  "file_name": "string",
  "message": "string"
}
```

#### 2. `read_file` - Read File

**Input:**
```json
{
  "file_name": "string"
}
```

**Output:**
```json
{
  "base_directory": "string",
  "file_name": "string",
  "contents": "string",
  "message": "string"
}
```

#### 3. `list_file` - List File

**Input:**
```json
{
  "path": "string"
}
```

**Output:**
```json
{
  "base_directory": "string",
  "path": "string",
  "files": ["string"],
  "folders": ["string"],
  "message": "string"
}
```

#### 4. `search_file` - Search File

**Input:**
```json
{
  "path": "string",
  "pattern": "string"
}
```

**Output:**
```json
{
  "base_directory": "string",
  "path": "string",
  "pattern": "string",
  "files": ["string"],
  "folders": ["string"],
  "message": "string"
}
```

#### 5. `search_content` - Search Content

**Input:**
```json
{
  "path": "string",
  "file_pattern": "string",
  "file_case_sensitive": "bool",
  "content_pattern": "string",
  "content_case_sensitive": "bool"
}
```

**Output:**
```json
{
  "base_directory": "string",
  "path": "string",
  "file_pattern": "string",
  "content_pattern": "string",
  "file_matches": [
    {
      "file_path": "string",
      "matches": [
         {
            "line_number": "integer",
            "line_content": "string",
         },
      ],
      "message": "string",
    },
  ],
  "message": "string"
}
```

#### 6. `replace_content` - Replace Content

**Input:**
```json
{
  "file_name": "string",
  "old_string": "string",
  "new_string": "string"
}
```

**Output:**
```json
{
  "base_directory": "string",
  "file_name": "string",
  "message": "string"
}
```

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
📁 File Operations Chat Demo
Model: deepseek-chat
Base Directory: ./project
Type 'exit' to end the conversation
Available tools: save_file, read_file, list_file, search_file, search_content, replace_content
==================================================
✅ File operations chat ready! Session: file-session-1755522576

💡 Try asking questions like:
   - Save a file called 'hello.txt' with content 'Hello, World!'
   - Read the file 'hello.txt'
   - List all files in the directory
   - Search for files with pattern '*.txt'
   - Create a file called 'data.json' with JSON content
   - The current directory is a code project. Can you help me fix the bug?

ℹ️  Note: All file operations will be performed in the base directory

👤 You: The current directory is a code project. Can you help me fix the bug?
🤖 Assistant: To help you fix the bug, I'll first need to understand the project structure and identify potential issues. I'll start by listing the files in the current directory to get an overview.
📁 File operation initiated:
   • list_file (ID: call_0_3d274e35-4fe7-47b8-94e9-4e5258f797be)
     Arguments: {}

🔄 Processing file operation...
✅ File operation result (ID: call_0_3d274e35-4fe7-47b8-94e9-4e5258f797be): {"base_directory":"project","path":"","files":["README.md","expect.txt","go.mod","input.txt","main.go","output.txt"],"folders":["counter"],"message":"Found 6 files and 1 folders in base directory"}
I see the project has the following files and folders:

**Files:**
1. `README.md`
2. `expect.txt`
3. `go.mod`
4. `input.txt`
5. `main.go`
6. `output.txt`

**Folders:**
1. `counter`

To identify potential bugs, I'll first inspect the `main.go` file, as it's likely the main entry point of the project. Let me read its contents.
📁 File operation initiated:
   • read_file (ID: call_0_a03f22e0-4b66-4948-ba78-80b61af2a714)
     Arguments: {"file_name":"main.go"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_a03f22e0-4b66-4948-ba78-80b61af2a714): {"base_directory":"project","file_name":"main.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.\n// All rights reserved.\n//\n// If you have downloaded a copy of the tRPC source code from Tencent,\n// please note that tRPC source code is licensed under the  Apache 2.0 License,\n// A copy of the Apache 2.0 License is included in this file.\n//\n//\n\n// package main is a example project with bug.\npackage main\n\nimport (\n\t\"log\"\n\t\"os\"\n\t\"strconv\"\n\t\"strings\"\n\n\t\"trpc.group/trpc-go/trpc-agent-go/examples/filetoolset/project/counter\"\n)\n\nfunc main() {\n\tcontent, err := os.ReadFile(\"input.txt\")\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tn, err := strconv.Atoi(strings.TrimSpace(string(content)))\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tcounter := counter.GetCounter(n)\n\tos.WriteFile(\"output.txt\", []byte(strconv.Itoa(counter)), 0644)\n}\n","message":"Successfully read main.go, start line: 1, end line: 37, total lines: 37"}
The `main.go` file reads an integer from `input.txt`, processes it using a `counter` package, and writes the result to `output.txt`. Here's a summary of the logic:

1. **Input Handling**: Reads an integer from `input.txt`.
2. **Processing**: Uses the `counter.GetCounter` function from the `counter` package to process the integer.
3. **Output Handling**: Writes the result to `output.txt`.

### Potential Issues:
1. **Error Handling**: The `os.WriteFile` call does not handle errors. If writing to `output.txt` fails, the program will silently ignore it.
2. **File Permissions**: The file permissions (`0644`) for `output.txt` might not be suitable for all environments.
3. **Dependency**: The `counter` package is imported, but its implementation is not visible. If there's a bug in the `counter` package, it could affect the program.

### Next Steps:
1. **Inspect the `counter` Package**: I'll check the `counter` package to understand its logic.
2. **Review `input.txt` and `expect.txt`**: These files might provide context about expected behavior.

Let me first inspect the `counter` package.
📁 File operation initiated:
   • list_file (ID: call_0_8950cfcc-36ad-4f1e-89b8-b775468efe40)
     Arguments: {"path":"counter"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_8950cfcc-36ad-4f1e-89b8-b775468efe40): {"base_directory":"project","path":"counter","files":["counter.go"],"folders":null,"message":"Found 1 files and 0 folders in counter"}
Now, let's read the `counter.go` file in the `counter` package to understand its logic.
📁 File operation initiated:
   • read_file (ID: call_0_d6bddefc-fa1c-43c3-bc84-ae218bdea5b2)
     Arguments: {"file_name":"counter/counter.go"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_d6bddefc-fa1c-43c3-bc84-ae218bdea5b2): {"base_directory":"project","file_name":"counter/counter.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.\n// All rights reserved.\n//\n// If you have downloaded a copy of the tRPC source code from Tencent,\n// please note that tRPC source code is licensed under the  Apache 2.0 License,\n// A copy of the Apache 2.0 License is included in this file.\n//\n//\n\n// package counter implements a counter.\npackage counter\n\nfunc GetCounter(n int) int {\n\tcounter := 0\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tcounter++\n\t\t}()\n\t}\n\treturn counter\n}\n","message":"Successfully read counter/counter.go, start line: 1, end line: 25, total lines: 25"}
The `counter.go` file contains a `GetCounter` function that increments a counter `n` times using goroutines. However, there's a critical issue here:

### Identified Bug:
1. **Race Condition**: The `counter` variable is accessed concurrently by multiple goroutines without any synchronization mechanism (e.g., mutex or atomic operations). This leads to a **data race**, and the final value of `counter` will be unpredictable.

### Explanation:
- The `GetCounter` function spawns `n` goroutines, each incrementing the `counter` variable.
- Since goroutines run concurrently, multiple goroutines might read and write `counter` simultaneously, causing incorrect results.
- The function returns `counter` immediately after spawning the goroutines, without waiting for them to complete. This means the returned value might not reflect all increments.

### Fix:
To fix this, we can:
1. Use a mutex to synchronize access to `counter`.
2. Use `sync.WaitGroup` to ensure all goroutines complete before returning.
3. Alternatively, use atomic operations for simple increments.

Here's the corrected version of `counter/counter.go`:
📁 File operation initiated:
   • replace_content (ID: call_0_b9b3782c-2b07-4d9e-b70d-1eeb59417587)
     Arguments: {"file_name":"counter/counter.go","old_string":"func GetCounter(n int) int {\n\tcounter := 0\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tcounter++\n\t\t}()\n\t}\n\treturn counter\n}","new_string":"import (\n\t\"sync\"\n)\n\nfunc GetCounter(n int) int {\n\tvar (\n\t\tcounter int\n\t\tmu      sync.Mutex\n\t\twg      sync.WaitGroup\n\t)\n\twg.Add(n)\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tdefer wg.Done()\n\t\t\tmu.Lock()\n\t\t\tcounter++\n\t\t\tmu.Unlock()\n\t\t}()\n\t}\n\twg.Wait()\n\treturn counter\n}"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_b9b3782c-2b07-4d9e-b70d-1eeb59417587): {"base_directory":"project","file_name":"counter/counter.go","message":"Successfully replaced 1 of 1 occurrence(s) in 'counter/counter.go'"}
The `counter/counter.go` file has been updated to fix the race condition. Here's what changed:

### Changes Made:
1. **Added Imports**: Added `sync` for `Mutex` and `WaitGroup`.
2. **Synchronization**:
   - Used a `sync.Mutex` to protect the `counter` variable.
   - Used a `sync.WaitGroup` to ensure all goroutines complete before returning the result.
3. **Corrected Logic**:
   - Each goroutine increments the counter safely under the mutex.
   - The `WaitGroup` ensures all increments are done before returning.

### Next Steps:
1. **Test the Fix**: You can test the program by running it with a sample input in `input.txt` and verifying the output in `output.txt`.
2. **Error Handling**: If you'd like, I can also improve the error handling in `main.go` (e.g., handling `os.WriteFile` errors).

Would you like me to proceed with any additional fixes or tests?

👤 You: exit
👋 Goodbye!
``````

## How It Works

1. **Setup**: The example creates an LLM agent with access to file operation tools
2. **User Input**: Users can ask any question that might benefit from file operations
3. **Tool Detection**: The AI automatically decides when to use file operation tools based on the query
4. **File Operation Execution**: The file tools perform the requested operations and return structured results
5. **Response Generation**: The AI uses the file operation results to provide informed, helpful responses

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
