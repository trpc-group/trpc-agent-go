# Container Code Execution Example

This example demonstrates how to use the `ContainerCodeExecutor` to run model-generated code in an isolated Docker container.

## What is Container Code Execution?

`codeexecutor/container` creates a disposable Docker container owned by the executor instance, and runs LLM-produced code blocks inside that same container via `docker exec` until `Close()` stops and removes it. It is a good fit for production-like setups where you want stronger isolation than the local executor and do not need persistent kernel state like the Jupyter executor provides.

### Key Features

- **Docker Isolation**: One executor-owned container is reused for successive `ExecuteCode` calls and removed on `Close()`
- **Configurable Image**: Use the default `python:3.9-slim` image, a custom image, or build one from a `Dockerfile`
- **Multi-language Support**: Execute Python (default) and Bash code blocks
- **No Network by Default**: The container is started with `network=none` for safety
- **Bind Mounts**: Mount host directories (e.g. read-only inputs) into the container
- **Clean Shutdown**: `Close()` stops and removes the container and closes the Docker client

## Prerequisites

- Go 1.23.0 or later
- Docker installed and running (the example talks to the daemon via `DOCKER_HOST` or `/var/run/docker.sock`)
- Valid OpenAI API key (or a compatible endpoint) for LLM functionality
- Network access for the initial image pull if the image is not yet available locally

## Environment Variables

| Variable          | Description                                                                | Default Value               |
| ----------------- | -------------------------------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required, automatically read by OpenAI SDK) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK)     | `https://api.openai.com/v1` |
| `DOCKER_HOST`     | Optional Docker daemon endpoint (otherwise the default socket is used)     | ``                          |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code.

## Command Line Arguments

| Argument | Description              | Default Value   |
| -------- | ------------------------ | --------------- |
| `-model` | Name of the model to use | `deepseek-chat` |

## Configuration Options

| Option                   | Description                                                       | Default Value                 |
| ------------------------ | ----------------------------------------------------------------- | ----------------------------- |
| `WithHost()`             | Base URL of the Docker daemon                                     | Docker client defaults        |
| `WithDockerFilePath()`   | Path to a directory containing a `Dockerfile` to build an image   | ``                            |
| `WithContainerConfig()`  | Full `container.Config` override (image, working dir, command...) | `python:3.9-slim`, `/`        |
| `WithHostConfig()`       | Full `container.HostConfig` override                              | `AutoRemove`, `NetworkMode=none` |
| `WithContainerName()`    | Fixed container name                                              | auto-generated                |
| `WithBindMount()`        | Append a host→container bind mount (`src`, `dest`, `mode`)        | none                          |
| `WithAutoInputs()`       | Map the inputs host directory under workspace `inputs/`           | `true`                        |

## Usage

### Basic Usage

From the example directory:

```bash
cd examples/codeexecution/container
go run main.go
```

The first run may take a while because Docker pulls the `python:3.9-slim` image.

### Using a Custom Image

```go
import (
    dockercontainer "github.com/docker/docker/api/types/container"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

executor, err := container.New(
    container.WithContainerConfig(dockercontainer.Config{
        Image:      "python:3.11-slim",
        WorkingDir: "/",
        Cmd:        []string{"tail", "-f", "/dev/null"},
        Tty:        true,
        OpenStdin:  true,
    }),
)
```

### Building an Image from a Dockerfile

`WithDockerFilePath` reuses `containerConfig.Image` as the build tag. When you
build from a local Dockerfile, pair it with `WithContainerConfig` so the build
gets a dedicated tag instead of overwriting the default `python:3.9-slim`:

```go
import (
    dockercontainer "github.com/docker/docker/api/types/container"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
)

executor, err := container.New(
    container.WithDockerFilePath("./docker"),
    container.WithContainerConfig(dockercontainer.Config{
        Image:      "my-agent-sandbox:latest",
        WorkingDir: "/",
        Cmd:        []string{"tail", "-f", "/dev/null"},
        Tty:        true,
        OpenStdin:  true,
    }),
)
```

### Mounting a Host Directory

```go
executor, err := container.New(
    container.WithBindMount("/host/inputs", "/data/inputs", "ro"),
)
```

## Example Output

When you run the example, you might see output like:

    Creating LLMAgent with Container code executor:
    - Model Name: deepseek-chat
    - Code Executor: Docker container
    - OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment

    === LLMAgent with Container Execution ===
    Processing events from LLMAgent:

    --- Event 1 ---
    ID: 9c3c3f1f-...
    Author: container_data_agent
    InvocationID: 21d4d052-...
    Object: chat.completion
    Message Content: I will compute the requested statistics using Python's standard library.

    ```python
    import statistics

    data = [5, 12, 8, 15, 7, 9, 11]
    print("mean     =", statistics.mean(data))
    print("median   =", statistics.median(data))
    print("variance =", statistics.variance(data))
    print("stdev    =", statistics.stdev(data))
    ```
    ```output
    mean     = 9.571428571428571
    median   = 9
    variance = 11.952380952380953
    stdev    = 3.457220105025817
    ```

    Summary: the dataset has a mean of ~9.57, median of 9, variance of ~11.95 and standard deviation of ~3.46.
    Token Usage - Prompt: 312, Completion: 184, Total: 496
    Done: true

    === Execution Complete ===
    Total events processed: 6
    === Demo Complete ===

## Security Considerations

When using container code execution:

1. **Image Provenance**: Only use images you trust. Consider building your own image with `WithDockerFilePath()`.
2. **No Network by Default**: The default `HostConfig` uses `NetworkMode=none`. Override with `WithHostConfig()` only if truly needed.
3. **Resource Limits**: Set `HostConfig.Resources` (CPU/memory limits, PIDs limit) when running untrusted code at scale.
4. **Read-only Mounts**: Prefer `ro` for bind mounts so the model cannot modify host data.
5. **Cleanup**: Always `defer executor.Close()` so containers are stopped and removed promptly.

## Troubleshooting

### Docker Daemon Not Reachable

Error like `Cannot connect to the Docker daemon` means Docker is not running or the current user cannot access the socket. Start Docker Desktop / `dockerd`, or set `DOCKER_HOST` to a reachable endpoint.

### Image Pull Failure

The first run pulls `python:3.9-slim`. If you are offline or behind a proxy, pre-pull the image:

```bash
docker pull python:3.9-slim
```

### Container Hangs or Is Left Behind

`AutoRemove` is enabled by default. If cleanup is skipped (e.g. the process was killed), remove stale containers manually:

```bash
docker ps -a --filter "name=trpc.go.agent-code-exec-" -q | xargs -r docker rm -f
```
