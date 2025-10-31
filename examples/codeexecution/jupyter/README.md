# Jupyter Code Execution Example

This example demonstrates how to use the Jupyter code execution capabilities with both standalone Jupyter gateway server and existing Jupyter server connections.

## What is Jupyter Code Execution?

The Jupyter code execution system allows you to execute Python code snippets using Jupyter kernels, providing an interactive execution environment similar to Jupyter notebooks.

### Key Features

- **Jupyter Gateway Integration**: Automatically starts and manages Jupyter kernel gateway server
- **Existing Server Support**: Connect to running Jupyter servers for code execution
- **Interactive Execution**: Supports interactive Python code execution with persistent kernel state
- **Code Block Extraction**: Automatically extract code blocks from markdown-formatted text
- **Safe Execution**: Silences pip install commands and provides controlled execution environment
- **Logging Configuration**: Configurable logging levels and file output

## Prerequisites

- Go 1.23.0 or later
- Python 3.x with Jupyter kernel gateway installed
- Jupyter kernel gateway package: `pip install jupyter_kernel_gateway`

## Usage Modes

### Standalone Jupyter Gateway Mode

Automatically starts a Jupyter kernel gateway server and executes code through it.

### Existing Jupyter Server Mode

Connects to an already running Jupyter server for code execution.

## Configuration Options

| Option | Description | Default Value |
| ------ | ----------- | ------------- |
| `WithIP()` | Jupyter server IP address | `127.0.0.1` |
| `WithPort()` | Jupyter server port | `8888` |
| `WithToken()` | Authentication token | Auto-generated |
| `WithKernelName()` | Kernel name | `python3` |
| `WithLogFile()` | Log file path | Empty (console only) |
| `WithLogLevel()` | Log level | `ERROR` |

## Usage

### Basic Usage with Standalone Jupyter Gateway

if you don't have a jupyter server running, you can use the standalone mode.

```bash
cd examples/codeexecution/jupyter
go run main.go
```

### Connecting to Existing Jupyter Server

for example, you start a jupyter server with kernel gateway:
```shell
jupyter kernelgateway --KernelGatewayApp.auth_token 009384d6e2452d520f45d87e72db349a0ebe7d3f04965978 --JupyterApp.answer_yes true
```

```go
    jupyterCli, err := jupyter.NewClient(jupyter.ConnectionInfo{
		Host:       "127.0.0.1",
		Port:       8888,
		Token:      "<TOKEN>",
		KernelName: "python3",
	})
	if err != nil {
		log.Fatalf("Failed to create Jupyter client: %v", err)
	}
	
    llmagent.WithCodeExecutor(jupyterCli)
```

## Example Output

When you run the example, you might see output like:

```
ID: 1aa99cd2-691c-4b4b-8a4b-ec4c8e4736e1
Author: jupyter_data_agent
InvocationID: 38303f0e-3266-49d5-86d3-d491f4539b1f
Object: chat.completion
Message Content: Okay, I can help you with that! I will use Python and the pandas library to calculate descriptive statistics for your dataset.

Here's the analysis:

```python
import pandas as pd
import numpy as np

# The given dataset
data = [5, 12, 8, 15, 7, 9, 11]

# Create a pandas Series for easy calculation of statistics
s = pd.Series(data)

# Calculate descriptive statistics
descriptive_stats = s.describe()

# Calculate mode separately as describe() doesn't always show it clearly for single modes
mode_value = s.mode()

print("Dataset:", data)
print("\n--- Descriptive Statistics ---")
print(descriptive_stats)
print(f"Mode: {mode_value.tolist()}") # .tolist() to display it nicely if there are multiple modes or none



Dataset: [5, 12, 8, 15, 7, 9, 11]

--- Descriptive Statistics ---
count     7.000000
mean      9.571429
std       3.409890
min       5.000000
25%       7.500000
50%       9.000000
75%      11.500000
max      15.000000
dtype: float64
Mode: [5, 7, 8, 9, 11, 12, 15]

**Explanation of the Descriptive Statistics:**

*   **count:** The number of observations in the dataset (7).
*   **mean:** The average value (9.57).
*   **std:** The standard deviation, which measures the amount of variation or dispersion of the dataset (3.41).
*   **min:** The smallest value in the dataset (5).
*   **25% (Q1):** The first quartile, meaning 25% of the data falls below this value (7.5).
*   **50% (Q2/Median):** The median, which is the middle value when the data is ordered (9.0).
*   **75% (Q3):** The third quartile, meaning 75% of the data falls below this value (11.5).
*   **max:** The largest value in the dataset (15).
*   **Mode:** The value(s) that appear most frequently in the dataset. In this case, since all numbers appear only once, all numbers are considered modes (5, 7, 8, 9, 11, 12, 15).
    Token Usage - Prompt: 2366, Completion: 4398, Total: 8337
    Done: true
```

## Security Considerations

When using Jupyter code execution:

1. **Authentication**: Always use authentication tokens for Jupyter server connections
2. **Network Security**: Ensure Jupyter servers are properly secured and not exposed to untrusted networks
3. **Code Validation**: Validate code input before execution
4. **Resource Management**: Monitor Jupyter server resource usage
5. **Log Monitoring**: Regularly check Jupyter server logs for suspicious activity

## Troubleshooting

### Jupyter Gateway Not Installed

If you see the error "Jupyter gateway server is not installed", install it with:

```bash
pip install jupyter_kernel_gateway
```

### Port Conflicts

If the default port 8888 is already in use, specify a different port:

```go
jupyterExecutor, err := jupyter.New(jupyter.WithPort(8889))
```

### Connection Issues

Ensure the Jupyter server is running and accessible at the specified IP and port.