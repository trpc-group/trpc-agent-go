# Code Execution DSL Example

This example demonstrates how to use the `builtin.code` component in DSL workflows to execute Python, JavaScript, and Bash code.

## Overview

The `builtin.code` component allows you to:
- Execute code snippets in Python, JavaScript, or Bash
- Store code as strings in the DSL JSON (fully serializable)
- Choose between local execution (fast) or container execution (secure)
- Configure timeouts, working directories, and cleanup options

This enables **front-end users to add custom logic** after the service is deployed, similar to n8n, Dify, and Coze.

## Features

✅ **Multiple Languages**: Python, JavaScript, Bash  
✅ **Dual Execution Modes**: Local (development) or Container (production)  
✅ **Fully Serializable**: Code stored in DSL JSON  
✅ **Configurable**: Timeouts, working directories, cleanup options  
✅ **Front-end Editable**: Code can be edited in Monaco editor  

## Workflow Structure

This example workflow demonstrates:

1. **Python Data Analysis** (`python_analysis` node)
   - Executes Python code to analyze sample data
   - Uses `statistics` module to calculate mean, median, stdev
   - Outputs results to `python_output` state field

2. **Bash System Info** (`bash_system_info` node)
   - Executes Bash commands to collect system information
   - Shows date, user, working directory, Python version
   - Outputs results to `bash_output` state field

3. **Format Results** (`format_results` node)
   - Custom component that formats the final output
   - Combines results from both code executions

## DSL Configuration

### builtin.code Component

```json
{
  "id": "python_analysis",
  "component": {
    "type": "component",
    "ref": "builtin.code"
  },
  "config": {
    "code": "import statistics\ndata = [5, 12, 8, 15, 7, 9, 11]\nprint(statistics.mean(data))",
    "language": "python",
    "executor_type": "local",
    "timeout": 30,
    "work_dir": "",
    "clean_temp_files": true
  },
  "outputs": [
    {
      "source": "output",
      "target": "python_output"
    }
  ]
}
```

### Configuration Options

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `code` | string | ✅ Yes | - | The code to execute |
| `language` | string | ✅ Yes | - | Language: `python`, `javascript`, or `bash` |
| `executor_type` | string | ❌ No | `local` | Execution mode: `local` or `container` |
| `timeout` | int | ❌ No | `30` | Timeout in seconds |
| `work_dir` | string | ❌ No | `""` | Working directory (empty = temp dir) |
| `clean_temp_files` | bool | ❌ No | `true` | Clean temp files after execution |

### Output Fields

| Field | Type | Description |
|-------|------|-------------|
| `output` | string | Standard output from code execution |
| `output_files` | []File | Files generated during execution |

## Running the Example

### Prerequisites

- Go 1.23.0 or later
- Python 3.x installed (for Python code execution)
- Bash shell (for Bash code execution)

### Run

```bash
cd examples/dsl/code_execution
go run main.go components.go
```

### Expected Output

```
🚀 Code Execution DSL Example
==================================================

✅ Loaded workflow: Code Execution Workflow
   Description: Demonstrates using builtin.code component to execute Python, JavaScript, and Bash code
   Nodes: 3

✅ Workflow compiled successfully!

🔄 Executing workflow...

📊 Execution Results:
==================================================

📝 Python Output:
=== Python Data Analysis ===
count: 7
min: 5
max: 15
mean: 9.57
median: 9
stdev: 3.36
{
  "count": 7,
  "min": 5,
  "max": 15,
  "mean": 9.57,
  "median": 9,
  "stdev": 3.36
}

📝 Bash Output:
=== System Information ===
Date: Thu Jan 16 10:30:45 CST 2025
User: username
Working Directory: /tmp/codeexec_xxx
Python Version: Python 3.11.0
Bash Version: 5.2.15

📋 Final Result:

╔════════════════════════════════════════════════════════════════╗
║           Code Execution Workflow Results                      ║
╚════════════════════════════════════════════════════════════════╝

📊 Python Analysis Results:
...

🖥️  System Information:
...

✅ Workflow completed successfully!

✅ Example completed successfully!
```

## Architecture Comparison

### trpc-agent-go-dsl vs Other Platforms

| Platform | Code Storage | Execution | Security | Use Case |
|----------|--------------|-----------|----------|----------|
| **LangGraph** | ❌ Not in DSL | Native Python | N/A | Developer tools |
| **n8n** | ✅ In DSL JSON | VM2/Pyodide | ⭐⭐⭐ | SaaS platform |
| **Dify** | ✅ In DSL JSON | Docker | ⭐⭐⭐⭐⭐ | SaaS platform |
| **trpc-agent-go-dsl** | ✅ In DSL JSON | Local/Container | ⭐⭐⭐⭐ | Enterprise platform |

### Design Philosophy

**trpc-agent-go-dsl** adopts **Paradigm B (Configuration-Driven)** with **Paradigm C (Hybrid)** flexibility:

- ✅ **Code as strings in DSL**: Like n8n and Dify
- ✅ **Dual execution modes**: Local (fast) + Container (secure)
- ✅ **Pre-registered components**: For enterprise security
- ✅ **Front-end editable**: Users can add logic post-deployment

## Security Considerations

### Local Executor (Development)
- ⚠️ **Not sandboxed**: Code runs in the same process
- ✅ **Fast**: No container overhead
- ✅ **Good for**: Development, trusted environments

### Container Executor (Production)
- ✅ **Fully isolated**: Code runs in Docker container
- ✅ **Secure**: Process, network, filesystem isolation
- ⚠️ **Slower**: Container startup overhead
- ✅ **Good for**: Production, untrusted code

### Best Practices

1. **Use Container mode in production** for untrusted code
2. **Set reasonable timeouts** to prevent infinite loops
3. **Validate code input** before execution
4. **Limit resource usage** with Docker resource limits
5. **Monitor execution** for suspicious activity

## Next Steps

- Try modifying the code in `workflow.json`
- Add more code execution nodes
- Experiment with JavaScript code execution
- Implement container executor for production use

## Related Examples

- `examples/codeexecution/` - LLMAgent with code execution
- `examples/codeexecution/jupyter/` - Jupyter kernel execution
- `examples/dsl/graph_fanout/` - Parallel task execution
- `examples/dsl/graph_subagent/` - Multi-agent composition

