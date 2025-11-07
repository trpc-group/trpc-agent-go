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
ID: 2ba1e657-d69e-40b2-9ed1-9087e6d58770
Author: jupyter_data_agent
InvocationID: 21d4d052-072b-44c2-a107-4f9daeabe4e0
Object: chat.completion
Message Content: Okay, I can help you with that! I will generate two random matrices using `numpy` and then calculate their product.

First, let's generate the two matrices. I'll create a 3x2 matrix and a 2x4 matrix so that their product is well-defined (the number of columns in the first matrix must equal the number of rows in the second matrix).

**Code Segment 1: Generate two random matrices**

This code imports the `numpy` library and then creates two matrices, `matrix_a` and `matrix_b`, filled with random integers.

/```python
import numpy as np

# Generate a 3x2 matrix with random integers between 0 and 9
matrix_a = np.random.randint(0, 10, size=(3, 2))
print("Matrix A (3x2):\n", matrix_a)

# Generate a 2x4 matrix with random integers between 0 and 9
matrix_b = np.random.randint(0, 10, size=(2, 4))
print("\nMatrix B (2x4):\n", matrix_b)
/```
```output
Matrix A (3x2):
 [[9 9]
 [5 7]
 [4 2]]

Matrix B (2x4):
 [[3 3 7 9]
 [9 8 8 9]]
/```

Now that we have `matrix_a` and `matrix_b` defined in the kernel's state, we can proceed to calculate their product.

**Code Segment 2: Calculate the product of the two matrices**

The product of `matrix_a` (3x2) and `matrix_b` (2x4) will result in a 3x4 matrix. We can use the `@` operator or `np.dot()` for matrix multiplication in NumPy.

```python
# Calculate the product of matrix_a and matrix_b
matrix_product = matrix_a @ matrix_b

print("Product of Matrix A and Matrix B (3x4):\n", matrix_product)
/```
```output
Product of Matrix A and Matrix B (3x4):
 [[108 105 135 162]
 [ 78  71  91 108]
 [ 30  28  44  54]]
/```

As you can see, we successfully generated two random matrices and then calculated their product, leveraging the Jupyter kernel's ability to maintain state across different code segments.
Token Usage - Prompt: 2664, Completion: 3747, Total: 7899
Done: true

=== Execution Complete ===
Total events processed: 18
=== Demo Complete ===
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