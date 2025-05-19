# Runner Example

This example demonstrates how to use the `runner` package from trpc-agent-go to efficiently execute agents. The example allows both direct runner usage and integration with the A2A protocol for client streaming interaction.

## Features

- **Direct Runner Usage**: Process messages directly with the runner
- **A2A Server Integration**: Wrap the runner in an A2A server for client interaction
- **Streaming Client Support**: Connect to the runner-backed A2A server with streaming
- **Synchronous/Asynchronous Modes**: Choose between immediate responses or event streaming
- **ReAct Agent**: Use Google's Gemini or OpenAI models for reasoning with tools
- **Multiple Tools**: Calculator, translator, unit converter, and text analysis
- **Configurable Timeout**: Set timeouts for agent executions

## Prerequisites

- Go 1.19 or later
- Google API Key for Gemini models (https://makersuite.google.com/app/apikey)
- OpenAI API Key for OpenAI models (optional)

## Usage

### Building

```bash
cd examples/runner
go build -o a2a_example main.go
chmod +x run_with_runner.sh
```

### Direct Runner Usage

Process a message directly with the runner:

```bash
# Run the example with a direct message (synchronous mode)
./run_with_runner.sh --message="Calculate the square root of 16 and translate it to Spanish"

# Run in asynchronous mode to see event streaming
./run_with_runner.sh --async --message="What's 25 * 4 and how do you say 'hello' in French?"

# Specify a different model
./run_with_runner.sh --provider=gemini --model=gemini-2.0-flash
```

### A2A Server and Client Interaction

Run as an A2A server that clients can connect to:

```bash
# Start the runner as an A2A server
./run_with_runner.sh --server

# In another terminal, connect as a client
./run_with_runner.sh --client --message="Calculate 25 * 4"

# Connect with streaming enabled
./run_with_runner.sh --client --stream --message="What's the square root of 144?"

# Connect to a server on a different address
./run_with_runner.sh --client --address=localhost:9000 --message="Your query here"
```

### Command Line Options

```
--async              Run in asynchronous mode
--model=NAME         Specify model name (default: gemini-2.0-flash)
--provider=PROVIDER  Specify model provider (default: gemini)
--message=MESSAGE    Message to process
--timeout=SECONDS    Runner timeout in seconds (default: 30)
--server             Run as an A2A server for client interaction
--client             Act as an A2A client (requires a running server)
--stream             Use streaming when in client mode
--address=ADDRESS    Server address:port (default: localhost:8081)
--debug              Enable debug logging
--help               Show this help message
```

## Implementation Details

The example demonstrates two primary usage patterns:

1. **Direct Runner Usage**: Creates a runner and processes messages directly
2. **A2A Integration**: Wraps the runner in an A2A server that clients can connect to

The `RunnerTaskProcessor` bridges the A2A protocol with the runner by:
- Converting A2A protocol messages to runner messages
- Running the agent asynchronously for streaming support
- Converting runner events back to A2A protocol messages

This shows how the runner can be integrated into larger systems while maintaining its performance and scalability benefits. 