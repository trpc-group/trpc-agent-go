# Code Execution Tool Example

This example demonstrates how to use the `codeexec` tool, allowing the LLM to proactively execute code via **Tool Call**.

## Quick Start

### 1. Build

```bash
cd examples
go build -o tool/codeexec/codeexec-demo ./tool/codeexec/
```

### 2. Configure Environment Variables

```bash
# Using DeepSeek
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"

# Or using OpenAI
export OPENAI_API_KEY="your-openai-api-key"
```

### 3. Run

#### Local executor (default)

```bash
cd tool/codeexec
./codeexec-demo -model deepseek-chat
```

#### Jupyter executor (requires `python` + `jupyter_kernel_gateway`)

Install dependency:

```bash
pip install jupyter_kernel_gateway
```

Run with Jupyter backend:

```bash
cd tool/codeexec
./codeexec-demo -model deepseek-chat -executor jupyter
```

> Note: The demo will start a local Jupyter Kernel Gateway subprocess and will call `Close()` on exit to clean it up.

### 4. Example Conversation

```
👤 User: Calculate the factorial of 10
🤖 Assistant: I'll calculate the factorial of 10 for you. The factorial of a number n (denoted as n!) is the product of all positive integers less than or equal to n.

Let me execute Python code to calculate 10!:


🔧 Tool calls:
   💻 execute_code (ID: call_3cf6cde9ac9c4eafac71b847)
     Arguments: {"code_blocks": [{"language": "python", "code": "import math\n\n# Calculate factorial of 10\nresult = math.factorial(10)\nprint(f\"10! = {result}\")\n\n# Let's also show the step-by-step calculation\n...

⚡ Executing code...
✅ Execution result (ID: call_3cf6cde9ac9c4eafac71b847):
{"output":"10! = 3628800\n\nStep-by-step calculation:\n1! = 1\n2! = 2\n3! = 6\n4! = 24\n5! = 120\n6! = 720\n7! = 5040\n8! = 40320\n9! = 362880\n10! = 3628800\n"}
The factorial of 10 is **3,628,800**.

Here's the step-by-step calculation:
- 1! = 1
- 2! = 2 × 1 = 2
- 3! = 3 × 2 = 6
- 4! = 4 × 6 = 24
- 5! = 5 × 24 = 120
- 6! = 6 × 120 = 720
- 7! = 7 × 720 = 5,040
- 8! = 8 × 5,040 = 40,320
- 9! = 9 × 40,320 = 362,880
- 10! = 10 × 362,880 = 3,628,800

So 10! = 10 × 9 × 8 × 7 × 6 × 5 × 4 × 3 × 2 × 1 = 3,628,800
```

## Example Questions

When you run the demo, you'll see a list of example questions. Here are some things you can try:

### Math & Computation
- Calculate the factorial of 10
- What is 123 * 456 + 789?
- Generate first 20 Fibonacci numbers
- Find all prime numbers under 100

### Security & Random
- Generate a random 16-character password with letters, numbers and symbols
- Generate a UUID
- Calculate the MD5 hash of 'hello world'

### Data Analysis
- Calculate mean, median, and std of [1,2,3,4,5,6,7,8,9,10]
- Sort the list [64, 34, 25, 12, 22, 11, 90] using quicksort

### Fun & Creative
- Create an ASCII art of a cat
- Print a multiplication table from 1 to 9
- Draw a simple bar chart for data [5, 3, 8, 2, 7]

### System (Bash)
- Show current date and time
- List files in current directory with sizes
- Show system information (uname -a)
- Display disk usage

## Difference from `WithCodeExecutor`

trpc-agent-go provides two ways to execute code:

### Method 1: `WithCodeExecutor` - Automatic Execution

```go
agent := llmagent.New("agent",
    llmagent.WithCodeExecutor(local.New()),
)
```

**How it works**:
- The framework automatically extracts code blocks from model output (e.g., ```python ... ```)
- Automatically executes all extracted code blocks
- The model **cannot control** whether execution happens

**Use cases**:
- Scenarios requiring forced execution of all code
- Interactive programming like Jupyter Notebook
- Data analysis and scientific computing tasks

### Method 2: `codeexec.NewTool()` - Tool Call Form (This Example)

```go
agent := llmagent.New("agent",
    llmagent.WithTools([]tool.Tool{
        codeexec.NewTool(local.New()),
    }),
)
```

**How it works**:
- Code execution is registered as a Tool
- The model **actively chooses** whether to execute code via Tool Call
- The model decides what code to execute and when

**Use cases**:
- When the model needs to decide whether code execution is necessary
- Working alongside other tools (search, file operations, etc.)
- More flexible Agent scenarios

### Comparison Summary

| Feature | `WithCodeExecutor` | `codeexec.NewTool()` |
|---------|-------------------|------------------|
| Execution Control | Auto-executes all code blocks | Model actively calls via Tool Call |
| Model Awareness | Model doesn't know code will be executed | Model knows execution tool exists |
| Flexibility | Low (forced execution) | High (on-demand execution) |
| Usage | `llmagent.WithCodeExecutor()` | `llmagent.WithTools()` + `codeexec.NewTool()` |
| Typical Scenario | Data analysis, Notebook | General-purpose Agent |

## API Reference

### Creating the Tool

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
)

// Default configuration
tool := codeexec.NewTool(local.New())

// Custom configuration
tool := codeexec.NewTool(
    local.New(local.WithTimeout(30 * time.Second)),
    codeexec.WithName("run_code"),                     // Custom tool name
    codeexec.WithDescription("Execute code..."),      // Custom description
    codeexec.WithLanguages("python", "bash", "go"),   // Custom supported languages
)
```

## Supported Executors

| Executor | Description | Import Path |
|----------|-------------|-------------|
| `local.New()` | Local execution (unsafe) | `codeexecutor/local` |
| `container.New()` | Docker container execution | `codeexecutor/container` |
| `jupyter.New()` | Jupyter Kernel execution (requires `Close()`) | `codeexecutor/jupyter` |

## Notes

1. **Security**: `local.New()` executes code directly on the local machine. Do not use in production environments.
2. **Timeout**: It's recommended to set a reasonable timeout to prevent infinite code execution.
3. **Language Support**: Supports `python` and `bash` by default. Extend via `WithLanguages`.
4. **File Outputs & Multi-node Deployments**: If your executor produces output files (e.g., images, CSVs), make sure your application surfaces them via the `file` tool (or another file-serving mechanism). In multi-node deployments, if the `file` tool (or file server) stores files on local disk, each node will have its own state and requests may not be able to read previously generated files—use shared storage (PVC/NFS/object storage) and/or sticky routing to keep file access consistent.
