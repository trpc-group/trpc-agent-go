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

#### 3. `list_files` - List Files

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

#### 4. `search_files` - Search Files

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
Available tools: save_file, read_file, list_files, search_files
==================================================
✅ File operations chat ready! Session: file-session-1754481761

💡 Try asking questions like:
   - Save a file called 'hello.txt' with content 'Hello, World!'
   - Read the file 'hello.txt'
   - List all files in the directory
   - Search for files with pattern '*.txt'
   - Create a file called 'data.json' with JSON content
   - The current directory is a code project. Can you help me fix the bug?

ℹ️  Note: All file operations will be performed in the base directory

👤 You: The current directory is a code project. Can you help me fix the bug?
🤖 Assistant: To help you fix the bug, I'll first need to understand the project structure and the relevant files. Let me start by listing the files in the current directory to get an overview.
📁 File operation initiated:
   • list_file (ID: call_0_98e206aa-1ed9-4399-8feb-69cc358847f6)
     Arguments: {"path":""}

🔄 Processing file operation...
✅ File operation result (ID: call_0_98e206aa-1ed9-4399-8feb-69cc358847f6): {"base_directory":"project","path":"","files":["README.md","expect.txt","go.mod","input.txt","main.go","output.txt"],"folders":["counter"],"message":"Found 6 files and 1 folders in base directory"}
Here’s the structure of your project:

### Files:
1. `README.md`
2. `expect.txt`
3. `go.mod`
4. `input.txt`
5. `main.go`
6. `output.txt`

### Folders:
1. `counter`

Since `main.go` is likely the main entry point of your Go project, I'll start by reading its contents to understand the code and identify any potential bugs.
📁 File operation initiated:
   • read_file (ID: call_0_26e639df-13a1-4420-bcc9-02e609431af0)
     Arguments: {"file_name":"main.go"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_26e639df-13a1-4420-bcc9-02e609431af0): {"base_directory":"project","file_name":"main.go","contents":"package main\n\nimport (\n\t\"log\"\n\t\"os\"\n\t\"strconv\"\n\t\"strings\"\n\n\t\"trpc.group/trpc-go/trpc-agent-go/examples/filetoolset/project/counter\"\n)\n\nfunc main() {\n\tcontent, err := os.ReadFile(\"input.txt\")\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tn, err := strconv.Atoi(strings.TrimSpace(string(content)))\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tcounter := counter.GetCounter(n)\n\tos.WriteFile(\"output.txt\", []byte(strconv.Itoa(counter)), 0644)\n}","message":"Successfully read main.go"}
The `main.go` file reads an integer from `input.txt`, processes it using a `counter` package (likely in the `counter` folder), and writes the result to `output.txt`. Here's the breakdown of the code:

1. **Reading Input**: It reads the content of `input.txt` and converts it to an integer.
2. **Processing**: It uses the `counter.GetCounter` function to process the integer.
3. **Writing Output**: It writes the result to `output.txt`.

### Potential Issues:
1. **Error Handling**: The `os.WriteFile` call does not handle errors. If writing fails, the program will not log the error.
2. **Input Validation**: The code assumes `input.txt` contains a valid integer. If it doesn't, the program will crash.
3. **Dependencies**: The `counter` package is imported from a specific path (`trpc.group/trpc-go/trpc-agent-go/examples/filetoolset/project/counter`). If this is not the correct path, the program will fail to compile.

### Next Steps:
1. **Check `input.txt`**: Ensure it contains a valid integer.
2. **Review `counter` Package**: I'll inspect the `counter` folder to understand the `GetCounter` function.
3. **Fix Error Handling**: Add error handling for `os.WriteFile`.

Let me first check the contents of `input.txt` and `output.txt` to understand the expected behavior.
📁 File operation initiated:
   • read_file (ID: call_0_e021b1b6-f099-41af-a022-166637607ba8)
     Arguments: {"file_name":"input.txt"}
   • read_file (ID: call_1_0b1c57d6-c7e7-49e0-b14a-eea37843841a)
     Arguments: {"file_name":"output.txt"}
   • read_file (ID: call_2_22066760-7b03-4750-a801-bcb97c8742c6)
     Arguments: {"file_name":"expect.txt"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_e021b1b6-f099-41af-a022-166637607ba8): {"base_directory":"project","file_name":"input.txt","contents":"100000","message":"Successfully read input.txt"}
✅ File operation result (ID: call_1_0b1c57d6-c7e7-49e0-b14a-eea37843841a): {"base_directory":"project","file_name":"output.txt","contents":"97876","message":"Successfully read output.txt"}
✅ File operation result (ID: call_2_22066760-7b03-4750-a801-bcb97c8742c6): {"base_directory":"project","file_name":"expect.txt","contents":"100000","message":"Successfully read expect.txt"}
Here’s what I found:

1. **`input.txt`**: Contains the integer `100000`.
2. **`output.txt`**: Contains the integer `97876`.
3. **`expect.txt`**: Contains the integer `100000`.

### Observations:
- The `output.txt` (`97876`) does not match `expect.txt` (`100000`), which suggests a bug in the `counter` package's `GetCounter` function.
- The `input.txt` is valid, so the issue is not with input parsing.

### Next Steps:
I’ll inspect the `counter` package to understand the `GetCounter` function and identify the bug. Let me list the files in the `counter` folder.
📁 File operation initiated:
   • list_file (ID: call_0_dc9f37b8-a159-40c6-9c81-4037b401cdd0)
     Arguments: {"path":"counter"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_dc9f37b8-a159-40c6-9c81-4037b401cdd0): {"base_directory":"project","path":"counter","files":["counter.go"],"folders":null,"message":"Found 1 files and 0 folders in counter"}
The `counter` folder contains a single file: `counter.go`. I'll read its contents to understand the `GetCounter` function.
📁 File operation initiated:
   • read_file (ID: call_0_606e2b89-3bfd-4d22-9496-666ba5be76b9)
     Arguments: {"file_name":"counter/counter.go"}

🔄 Processing file operation...
✅ File operation result (ID: call_0_606e2b89-3bfd-4d22-9496-666ba5be76b9): {"base_directory":"project","file_name":"counter/counter.go","contents":"package counter\n\nfunc GetCounter(n int) int {\n\tcounter := 0\n\tfor i := 0; i \u003c n; i++ {\n\t\tgo func() {\n\t\t\tcounter++\n\t\t}()\n\t}\n\treturn counter\n}\n","message":"Successfully read counter/counter.go"}
### Bug Analysis:
The `GetCounter` function in `counter/counter.go` has a **concurrency bug**. Here's the issue:

1. **Problem**:
   - The function spawns `n` goroutines, each incrementing the `counter` variable.
   - However, goroutines run concurrently, and the `counter++` operation is not thread-safe. This leads to a **race condition**, where multiple goroutines may read and write `counter` simultaneously, causing incorrect results.
   - Additionally, the function returns `counter` immediately after spawning the goroutines, without waiting for them to finish. This means the returned value is likely `0` or some intermediate value, not the final count.

2. **Expected Behavior**:
   - The function should return `n` (the input value), as it increments `counter` `n` times.

### Fix:
To fix this, we need to:
1. Ensure thread-safe access to `counter` using a mutex or synchronization.
2. Wait for all goroutines to complete before returning the result.

Here’s the corrected version of `counter/counter.go`:

```go
package counter

import "sync"

func GetCounter(n int) int {
    var (
        counter int
        wg      sync.WaitGroup
        mu      sync.Mutex
    )

    wg.Add(n)
    for i := 0; i < n; i++ {
        go func() {
            defer wg.Done()
            mu.Lock()
            counter++
            mu.Unlock()
        }()
    }
    wg.Wait()
    return counter
}
```

### Changes Made:
1. Added a `sync.Mutex` (`mu`) to protect the `counter` variable from concurrent access.
2. Used a `sync.WaitGroup` (`wg`) to wait for all goroutines to complete before returning the result.
3. Each goroutine now locks the mutex before incrementing `counter` and unlocks it afterward.

### Verification:
After applying this fix, the function should return `n` (e.g., `100000` for the given input), matching the expected output in `expect.txt`.

Would you like me to save the corrected `counter/counter.go` file? Let me know, and I can proceed with the update.

👤 You: Yes 
🤖 Assistant: 📁 File operation initiated:
   • save_file (ID: call_0_bba8c8b6-74f6-4bdf-92e5-874c6ac540cd)
     Arguments: {"contents":"package counter\n\nimport \"sync\"\n\nfunc GetCounter(n int) int {\n    var (\n        counter int\n        wg      sync.WaitGroup\n        mu      sync.Mutex\n    )\n\n    wg.Add(n)\n    for i := 0; i < n; i++ {\n        go func() {\n            defer wg.Done()\n            mu.Lock()\n            counter++\n            mu.Unlock()\n        }()\n    }\n    wg.Wait()\n    return counter\n}","file_name":"counter/counter.go","overwrite":true}

🔄 Processing file operation...
✅ File operation result (ID: call_0_bba8c8b6-74f6-4bdf-92e5-874c6ac540cd): {"base_directory":"project","file_name":"counter/counter.go","message":"Successfully saved: counter/counter.go"}

🤖 Assistant: The corrected `counter/counter.go` file has been saved successfully. Here's a summary of the changes:

1. **Fixed Concurrency Bug**:
   - Added `sync.Mutex` to ensure thread-safe access to the `counter` variable.
   - Used `sync.WaitGroup` to wait for all goroutines to complete before returning the result.

2. **Expected Behavior**:
   - The function now correctly increments the counter `n` times and returns the expected value (`100000` for the given input).

### Next Steps:
1. **Run the Program**:
   - Execute `main.go` to verify that the output (`output.txt`) matches `expect.txt` (`100000`).

2. **Check Output**:
   - After running the program, inspect `output.txt` to confirm the fix.

If you encounter any further issues or need additional help, feel free to ask!
```

### Changes Made:
1. Added a `sync.Mutex` (`mu`) to protect the `counter` variable from concurrent access.
2. Used a `sync.WaitGroup` (`wg`) to wait for all goroutines to complete before returning the result.
3. Each goroutine now locks the mutex before incrementing `counter` and unlocks it afterward.

### Verification:
After applying this fix, the function should return `n` (e.g., `100000` for the given input), matching the expected output in `expect.txt`.

Would you like me to save the corrected `counter/counter.go` file? Let me know, and I can proceed with the update.

👤 You: Yes 
🤖 Assistant: 📁 File operation initiated:
   • save_file (ID: call_0_bba8c8b6-74f6-4bdf-92e5-874c6ac540cd)
     Arguments: {"contents":"package counter\n\nimport \"sync\"\n\nfunc GetCounter(n int) int {\n    var (\n        counter int\n        wg      sync.WaitGroup\n        mu      sync.Mutex\n    )\n\n    wg.Add(n)\n    for i := 0; i < n; i++ {\n        go func() {\n            defer wg.Done()\n            mu.Lock()\n            counter++\n            mu.Unlock()\n        }()\n    }\n    wg.Wait()\n    return counter\n}","file_name":"counter/counter.go","overwrite":true}

🔄 Processing file operation...
✅ File operation result (ID: call_0_bba8c8b6-74f6-4bdf-92e5-874c6ac540cd): {"base_directory":"project","file_name":"counter/counter.go","message":"Successfully saved: counter/counter.go"}

🤖 Assistant: The corrected `counter/counter.go` file has been saved successfully. Here's a summary of the changes:

1. **Fixed Concurrency Bug**:
   - Added `sync.Mutex` to ensure thread-safe access to the `counter` variable.
   - Used `sync.WaitGroup` to wait for all goroutines to complete before returning the result.

2. **Expected Behavior**:
   - The function now correctly increments the counter `n` times and returns the expected value (`100000` for the given input).

### Next Steps:
1. **Run the Program**:
   - Execute `main.go` to verify that the output (`output.txt`) matches `expect.txt` (`100000`).

2. **Check Output**:
   - After running the program, inspect `output.txt` to confirm the fix.

If you encounter any further issues or need additional help, feel free to ask!


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
