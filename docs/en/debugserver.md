# Debug Server Usage Guide

## Overview

Debug Server is a debugging tool provided by the trpc-agent-go framework. 
It helps developers quickly test and debug Agent functionality.
It can be combined with [ADK Web UI](https://github.com/google/adk-web) to allow you to verify Agent behavior and tool calls through a visual interactive interface.

## Main Features

- **Visual Debug Interface**: Provides a user-friendly graphical interface through ADK Web UI
- **Real-time Interactive Testing**: Supports real-time conversation and tool calls with Agents
- **Streaming Response**: Supports Server-Sent Events (SSE) streaming response
- **Session Management**: Supports creating and managing multiple conversation sessions
- **Tool Validation**: Can intuitively test and verify various tool functions of Agents

## Architecture Diagram

```
User Interface
+---------------------------+
|      ADK Web UI           |  ← Access via browser: http://localhost:4200
|        (React)            |
+-----------+---------------+
            | HTTP/SSE Request
            v
+-----------------------------+
|     Debug Server            |  ← Listening on http://localhost:8000
|                             |
|       API Routing           | 
|       Session Management    | 
|       CORS Handling         |
+-----------+-----------------+
            | Call Agent
            v
+---------------------------------+
|    tRPC-Agent-Go                |
|                                 |
| +-------------+ +--------------+| 
| | LLM Agent   | | Tool System  ||
| | • Model Call| | • Calculator ||
| | • Streaming | | • Time Query ||
| | • Prompting | | • Custom Tool||
| +-------------+ +--------------+|
+-----------+---------------------+
            | External Call
            v
+----------------------------------+
|     External Services            |
|                                  |
| • LLM API   (OpenAI/DeepSeek)    | 
| • Database   (Redis/MySQL)       | 
| • Other API  (Search/File System)|
+----------------------------------+
```

Data Flow:

```
User Input → Web UI → Debug Server → Agent → LLM/Tools → Streaming Response → Web UI
```

## Usage Steps

1. Create an Agent.
2. Create a Debug Server with the Agent as a constructor parameter. The Debug Server itself can provide http Handler functions.
3. Create a tRPC HTTP service and register the Debug Server's http Handler as the handler function for the tRPC HTTP service.
4. Start the tRPC HTTP service as a backend service.
5. Install ADK Web UI for convenient frontend visual debugging.
6. Start ADK Web UI and specify the tRPC HTTP service as the backend service.
7. You can directly input user requests through ADK Web UI in the browser frontend for debugging. The frontend page will display observable data.

For specific runnable examples, see [examples/debugserver](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/debugserver)

## Debug Results Display

Through ADK Web UI, you can directly test call scenarios. The Web interface will display event and trace information.
For example, the following shows debugging an agent with calculator functionality.

![event](../assets/img/debugserver/event.png)

![trace](../assets/img/debugserver/trace.png)
