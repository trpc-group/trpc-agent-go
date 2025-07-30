# Code Execution Example

This example demonstrates how to use the code execution capabilities with both `LocalCodeExecutor` and `ContainerCodeExecutor` implementations.

## What is Code Execution?

The code execution system allows you to execute code snippets in various programming languages (Python, Bash) either locally or in isolated Docker containers.

### Key Features

- **Multiple Executors**: Support for both local execution and containerized execution
- **Multi-language Support**: Execute Python, and Bash code
- **Configurable**: Custom working directories, timeouts, and cleanup options
- **Code Block Extraction**: Automatically extract code blocks from markdown-formatted text
- **Safe Execution**: Isolated environments with resource limits (containers) or controlled local execution

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint) for LLM functionality
- Docker installed and running (for ContainerCodeExecutor)
- Python 3.x, Go, and Bash interpreters (for LocalCodeExecutor)

## Code Executors

### LocalCodeExecutor

Executes code directly on the local machine. Suitable for trusted environments.

### ContainerCodeExecutor  

Executes code in isolated Docker containers. Provides better security and isolation.

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required, automatically read by OpenAI SDK) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK) | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |

## Usage

### Basic Usage with Local Execution

```bash
cd examples/codeexecution
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### With Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
go run main.go -model deepseek-chat
```


## Example Output

When you run the example, you might see output like:

```
Creating LLMAgent with configuration:
- Model Name: deepseek-chat
- OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment

=== LLMAgent Execution ===
Processing events from LLMAgent:

--- Event xxx ---
.......

--- Event 328 ---
ID: 30641681-7f0f-46cc-b992-003458af0c3d
Author: data_science_agent
InvocationID: invocation-7d8497e5-d9d0-462c-bee0-4be82e8924a2
Object: 
Message Content: I'll analyze the given sample data (5, 12, 8, 15, 7, 9, 11) using Python's standard library functions. Here's the complete analysis in a single code block:

```python
import statistics

# Data processing and analysis
data = [5, 12, 8, 15, 7, 9, 11]
sorted_data = sorted(data)
length = len(data)
minimum = min(data)
maximum = max(data)
mean = statistics.mean(data)
median = statistics.median(data)
stdev = statistics.stdev(data) if len(data) > 1 else 0
variance = statistics.variance(data) if len(data) > 1 else 0

# Output results
print(f"Original data: {data}")
print(f"Sorted data: {sorted_data}")
print(f"Count: {length}")
print(f"Minimum: {minimum}")
print(f"Maximum: {maximum}")
print(f"Mean: {mean:.2f}")
print(f"Median: {median}")
print(f"Standard deviation: {stdev:.2f}")
print(f"Variance: {variance:.2f}")
```

This code will:
1. Import the necessary statistics module
2. Process the given data
3. Calculate all basic statistical measures
4. Print the results in a readable format

The analysis includes both measures of central tendency (mean, median) and measures of dispersion (standard deviation, variance), along with basic data characteristics like count, min, and max values.
Token Usage - Prompt: 860, Completion: 320, Total: 1180
Done: true

--- Event 329 ---
ID: 815e45db-7ae0-48c2-a330-da924d2a8122
Author: data_science_agent
InvocationID: invocation-7d8497e5-d9d0-462c-bee0-4be82e8924a2
Object: 
Message Content: Code execution result:
Original data: [5, 12, 8, 15, 7, 9, 11]
Sorted data: [5, 7, 8, 9, 11, 12, 15]
Count: 7
Minimum: 5
Maximum: 15
Mean: 9.57
Median: 9
Standard deviation: 3.36
Variance: 11.29


Done: false

--- Event 330 ---
ID: 9c14d605-3e9b-40f8-9c61-42d901ee9b4a
Author: data_science_agent
InvocationID: invocation-7d8497e5-d9d0-462c-bee0-4be82e8924a2
Object: runner.completion
Done: true

=== Execution Complete ===
```

### Security Considerations

When using code execution, especially with user-provided code:

1. **Container Isolation**: Use `ContainerCodeExecutor` for better security isolation
2. **Timeouts**: Always set reasonable timeouts to prevent infinite loops
3. **Resource Limits**: Consider Docker resource limits for container execution
4. **Input Validation**: Validate code input before execution
5. **Network Isolation**: Containers run with limited network access
