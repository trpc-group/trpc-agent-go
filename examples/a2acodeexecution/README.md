# A2A Code Execution Example

This example demonstrates how to build an A2A server with code execution capabilities and interact with it using an A2A client.

## Structure

```
a2acodeexecution/
├── server/         # A2A server with code execution agent
│   └── main.go
├── client/         # A2A client to interact with the server
│   └── main.go
└── README.md
```

## Code Execution Event Model

Code execution events use the same `ObjectType` (`postprocessing.code_execution`) but are distinguished by the `Tag` field:

| Event Type | ObjectType | Tag |
|------------|-----------|-----|
| Code Execution | `postprocessing.code_execution` | `code` |
| Code Execution Result | `postprocessing.code_execution` | `code_execution_result` |

### Checking Event Type

```go
// Check for code execution event
if evt.Response.Object == model.ObjectTypePostprocessingCodeExecution &&
    evt.ContainTag(event.TagCodeExecution) {
    // Handle code execution
}

// Check for code execution result event
if evt.Response.Object == model.ObjectTypePostprocessingCodeExecution &&
    evt.ContainTag(event.TagCodeExecutionResult) {
    // Handle code execution result
}
```

## Prerequisites

1. Set up your OpenAI-compatible API credentials:
```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # or your API endpoint
```

2. Python environment for code execution (the local executor uses Python)

## Running the Example

### Step 1: Start the Server

```bash
# From the examples/a2acodeexecution/server directory
cd server
go run main.go

# With custom options
go run main.go -model deepseek-chat -host 0.0.0.0:8888 -streaming=true
```

#### Server Command Line Options

| Option | Default | Description |
|--------|---------|-------------|
| `-model` | `deepseek-chat` | Model name to use |
| `-host` | `0.0.0.0:8888` | A2A server host address |
| `-streaming` | `true` | Enable streaming mode |

### Step 2: Run the Client

In a new terminal:

```bash
# From the examples/a2acodeexecution/client directory
cd client
go run main.go

# With custom server URL
go run main.go -url http://localhost:8888
```

#### Client Command Line Options

| Option | Default | Description |
|--------|---------|-------------|
| `-url` | `http://localhost:8888` | A2A server URL |

## Expected Output

### Server Output

```
========================================
A2A Code Execution Server
========================================
Model: deepseek-chat
Host: 0.0.0.0:8888
Streaming: true
========================================

Starting A2A server on 0.0.0.0:8888...
Press Ctrl+C to stop the server
```

### Client Output

```
========================================
A2A Code Execution Client
========================================
Server URL: http://localhost:8888
========================================

Connected to A2A Server:
  Name: code_execution_agent
  Description: An agent that can execute Python code to solve problems
  URL: http://localhost:8888

Test 1: Simple Python Code Execution
=====================================
Query: Calculate the sum of numbers from 1 to 10 using Python code

[Code Execution]
---------------------------------------------
```python
result = sum(range(1, 11))
print(f"The sum is: {result}")
```
---------------------------------------------

[Code Execution Result]
The sum is: 55
---------------------------------------------
```

## Key Components

### Server (`server/main.go`)
- Creates an LLM agent with `llmagent.WithCodeExecutor(local.New())` to enable code execution
- Exposes the agent via A2A protocol using `a2a.New()` and `server.Start()`

### Client (`client/main.go`)
- Connects to the A2A server using `a2aagent.New(a2aagent.WithAgentCardURL(url))`
- Processes events and distinguishes between code execution and result events using `ObjectType` and `Tag`
