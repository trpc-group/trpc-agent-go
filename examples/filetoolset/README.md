# File Tool Set Example

This example demonstrates how to use file operation tools with an AI agent for interactive conversations. The tool provides comprehensive file system capabilities including saving files, reading files, listing files, and searching files.

## Background

The filetoolset project contains a Go program with a bug, and builds an AI agent with file operation tools to help fix this bug.

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
✅ File operations chat ready! Session: file-session-1755574124

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
   • list_file (ID: call_0_587947ac-66d3-4a55-942a-033b655bd989)
     Arguments: {}

🔄 Processing file operation...
✅ File operation result (ID: call_0_587947ac-66d3-4a55-942a-033b655bd989): {"base_directory":"project","path":"","files":["README.md","expect.txt","go.mod","input.txt","main.go","output.txt"],"folders":["counter"],"message":"Found 6 files and 1 folders in base directory"}
I see the project contains the following files and folders:

**Files:**
1. `README.md`
2. `expect.txt`
3. `go.mod`
4. `input.txt`
5. `main.go`
6. `output.txt`

**Folders:**
1. `counter`

To identify the bug, I'll first inspect the `main.go` file, as it's likely the main entry point of the project. Let me read its contents.
📁 File operation initiated:
   • read_file (ID: call_0_b005bfb1-77d5-4e59-9962-5a5853f53363)
     Arguments: {"file_name":"main.go"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_b005bfb1-77d5-4e59-9962-5a5853f53363): {"base_directory":"project","file_name":"main.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package main is a example project with bug.\npackage main\n\nimport (\n\t\"log\"\n\t\"os\"\n\t\"strconv\"\n\t\"strings\"\n\n\t\"trpc.group/trpc-go/trpc-agent-go/examples/filetoolset/project/counter\"\n)\n\nfunc main() {\n\tcontent, err := os.ReadFile(\"input.txt\")\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tn, err := strconv.Atoi(strings.TrimSpace(string(content)))\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tcounter := counter.GetCounter(n)\n\tos.WriteFile(\"output.txt\", []byte(strconv.Itoa(counter)), 0644)\n}\n","message":"Successfully read main.go, start line: 1, end line: 34, total lines: 34"}
The `main.go` file reads an integer from `input.txt`, processes it using the `counter` package, and writes the result to `output.txt`. Let me check the `counter` package to understand its functionality. I'll read the contents of the `counter` folder.
📁 File operation initiated:
   • list_file (ID: call_0_545b0891-c372-49a2-b3d6-a982caf09f4a)
     Arguments: {"path":"counter"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_545b0891-c372-49a2-b3d6-a982caf09f4a): {"base_directory":"project","path":"counter","files":["counter.go"],"folders":null,"message":"Found 1 files and 0 folders in counter"}
Now, I'll read the `counter.go` file to understand how the counter logic is implemented.
📁 File operation initiated:
   • read_file (ID: call_0_14925dfc-578d-46c2-a93c-34f40b7dede5)
     Arguments: {"file_name":"counter/counter.go"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_14925dfc-578d-46c2-a93c-34f40b7dede5): {"base_directory":"project","file_name":"counter/counter.go","contents":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package counter implements a counter.\npackage counter\n\nimport \"sync/atomic\"\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tatomic.AddInt64(\u0026counter, 1)\n\t\t}()\n\t}\n\treturn int(counter)\n}\n","message":"Successfully read counter/counter.go, start line: 1, end line: 24, total lines: 24"}
### Analysis of the Bug

1. **`main.go`**:
   - Reads an integer `n` from `input.txt`.
   - Calls `counter.GetCounter(n)` to process the integer.
   - Writes the result to `output.txt`.

2. **`counter/counter.go`**:
   - The `GetCounter` function spawns `n` goroutines, each incrementing a shared `counter` variable using `atomic.AddInt64`.
   - However, the function returns the `counter` immediately after spawning the goroutines, without waiting for them to complete. This means the returned value is likely `0` or some intermediate value, not the final count.

### The Bug
The issue is a **race condition**:
- The goroutines spawned in `GetCounter` may not have finished executing by the time the function returns.
- As a result, the `counter` value returned is not the final count after all goroutines have completed.

### Fix
To fix this, we need to ensure all goroutines complete before returning the `counter` value. We can use a `sync.WaitGroup` to synchronize the goroutines.

Here’s the corrected version of `counter/counter.go`:

```go
package counter

import (
        "sync"
        "sync/atomic"
)

func GetCounter(n int) int {
        var (
                counter int64
                wg      sync.WaitGroup
        )
        wg.Add(n)
        for i := 0; i < n; i++ {
                go func() {
                        defer wg.Done()
                        atomic.AddInt64(&counter, 1)
                }()
        }
        wg.Wait()
        return int(counter)
}
```

### Changes Made:
1. Added a `sync.WaitGroup` to track the completion of all goroutines.
2. Called `wg.Add(n)` to set the number of goroutines to wait for.
3. Added `defer wg.Done()` in each goroutine to signal completion.
4. Added `wg.Wait()` to block until all goroutines finish.

### Testing the Fix
To test the fix:
1. Place a number in `input.txt` (e.g., `5`).
2. Run the program.
3. Check `output.txt`; it should now correctly display the count (e.g., `5`).

Would you like me to save the corrected `counter.go` file? Let me know!

👤 You: do it
🤖 Assistant: 📁 File operation initiated:
   • replace_content (ID: call_0_1f20ca9b-7e1d-4f8f-8eed-14d139d06048)
     Arguments: {"file_name":"counter/counter.go","old_string":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package counter implements a counter.\npackage counter\n\nimport \"sync/atomic\"\n\nfunc GetCounter(n int) int {\n\tvar counter int64\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tatomic.AddInt64(\u0026counter, 1)\n\t\t}()\n\t}\n\treturn int(counter)\n}","new_string":"//\n// Tencent is pleased to support the open source community by making trpc-agent-go available.\n//\n// Copyright (C) 2025 Tencent.  All rights reserved.\n\n// trpc-agent-go is licensed under the Apache License Version 2.0.\n//\n//\n\n// package counter implements a counter.\npackage counter\n\nimport (\n\t\"sync\"\n\t\"sync/atomic\"\n)\n\nfunc GetCounter(n int) int {\n\tvar (\n\t\tcounter int64\n\t\twg      sync.WaitGroup\n\t)\n\twg.Add(n)\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tdefer wg.Done()\n\t\t\tatomic.AddInt64(\u0026counter, 1)\n\t\t}()\n\t}\n\twg.Wait()\n\treturn int(counter)\n}"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_1f20ca9b-7e1d-4f8f-8eed-14d139d06048): {"base_directory":"project","file_name":"counter/counter.go","message":"Successfully replaced 1 of 1 occurrence(s) in 'counter/counter.go'"}

🤖 Assistant: The `counter/counter.go` file has been successfully updated with the fix for the race condition. The changes include:

1. Added `sync` to the imports for `sync.WaitGroup`.
2. Introduced a `sync.WaitGroup` to ensure all goroutines complete before returning the `counter` value.
3. Added `wg.Add(n)`, `defer wg.Done()`, and `wg.Wait()` to synchronize the goroutines.

You can now test the program by running it with an input value in `input.txt`. The output in `output.txt` should correctly reflect the count after all goroutines have finished executing.

Let me know if you'd like to test the fix or if there's anything else you'd like me to check!

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
