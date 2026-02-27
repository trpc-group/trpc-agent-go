# A2A Protocol Interaction Specification

!!! note "Note"
    This document defines the extended implementation specification for the A2A protocol within the trpc-agent-go framework. Regular users do not need to read this document when using A2A Client/Server — the framework automatically handles all protocol conversion details. You only need to refer to this specification when developing a non-trpc-agent-go A2A Client/Server that interoperates with this framework.

## Background

The [A2A (Agent-to-Agent) protocol](https://a2a-protocol.org/latest/specification/) defines the basic data models (Message, Task, Part, etc.) and operation interfaces (SendMessage, StreamMessage, etc.) for inter-Agent communication, but **does not specify** the following application-layer behaviors:

- How tool calls (Function Call / Response) are transmitted through A2A Parts
- How code execution events are encoded
- How model reasoning content (Reasoning) is marked
- How the Client should handle control signals during streaming interactions
- What tracing and aggregation fields should be carried in Metadata

This document defines the **interaction specification** of trpc-agent-go on top of the A2A protocol, serving as the standard reference for Client and Server implementations. When the A2A protocol is upgraded (e.g., v1.0), this document will be updated accordingly.

> For the complete A2A protocol specification, see: https://a2a-protocol.org/latest/specification/
>
> For the framework usage guide, see: [A2A Integration Guide](a2a.md)

---

## Overall Conversion Flow

```mermaid
sequenceDiagram
    participant C as A2AAgent (Client)
    participant S as A2A Server
    participant R as Agent Runner
    participant A as Agent

    Note over C,A: Non-streaming (message/send)
    C->>C: Invocation → protocol.Message (TextPart/FilePart)
    C->>S: HTTP POST JSON-RPC: message/send
    S->>S: protocol.Message → model.Message
    S->>R: runner.Run(userID, ctxID, message)
    R->>A: agent.Run(ctx, invocation)
    A-->>R: event.Event (ToolCall)
    A-->>R: event.Event (ToolResponse)
    A-->>R: event.Event (Content)
    R-->>S: <-chan event.Event
    S->>S: Convert each event → protocol.Message
    S->>S: Single message returns Message / multiple messages wrapped in Task
    S-->>C: JSON-RPC Response (Message or Task)
    C->>C: Message/Task → event.Event sequence

    Note over C,A: Streaming (message/stream)
    C->>C: Invocation → protocol.Message (TextPart/FilePart)
    C->>S: HTTP POST JSON-RPC: message/stream
    S->>S: protocol.Message → model.Message
    S-->>C: SSE: TaskStatusUpdateEvent (submitted)
    S->>R: runner.Run(userID, ctxID, message)
    R->>A: agent.Run(ctx, invocation)
    loop Agent produces events
        A-->>R: event.Event (Delta/ToolCall/...)
        R-->>S: event.Event
        S->>S: event → TaskArtifactUpdateEvent
        S-->>C: SSE: TaskArtifactUpdateEvent
        C->>C: ArtifactUpdate → event.Event
    end
    S-->>C: SSE: TaskArtifactUpdateEvent (lastChunk=true)
    S-->>C: SSE: TaskStatusUpdateEvent (completed)
```

`SendMessage` waits for the complete response before returning it all at once, while `StreamMessage` pushes incremental events in real-time via SSE. The framework automatically handles format conversion on both ends.

---

## Event Types and A2A Mapping


| Agent Event          | A2A Part Type                 | Part Metadata                 | Message Metadata               |
| -------------------- | ----------------------------- | ----------------------------- | ------------------------------ |
| Text Reply           | `TextPart`                    | —                             | `object_type: chat.completion` |
| Reasoning Content    | `TextPart`                    | `thought: true`               | Same as above                  |
| Tool Call            | `DataPart` (id/name/args)     | `type: function_call`         | `object_type: chat.completion` |
| Tool Response        | `DataPart` (id/name/response) | `type: function_response`     | `object_type: tool.response`   |
| Executable Code      | `DataPart` (code/language)    | `type: executable_code`       | `tag: code_execution_code`     |
| Code Execution Result| `DataPart` (output/outcome)   | `type: code_execution_result` | `tag: code_execution_result`   |

---

## Tool Call Transmission Flow

### Non-streaming (message/send)

The Server collects all Agent events and returns them at once. A single message is returned directly as `protocol.Message`; multiple messages are wrapped in `protocol.Task` (intermediate processes in `history`, final reply in `artifacts`).

```mermaid
sequenceDiagram
    participant C as A2AAgent (Client)
    participant S as A2A Server
    participant R as Agent Runner
    participant A as Agent + LLM

    C->>C: Invocation → protocol.Message
    C->>S: HTTP POST message/send (TextPart: "What's the weather in Beijing?")
    S->>S: protocol.Message → model.Message
    S->>R: runner.Run(userID, ctxID, message)
    R->>A: agent.Run(ctx, invocation)
    A->>A: Call LLM

    Note over A,R: LLM decides to call a tool
    A-->>R: event(ToolCall: get_weather, args: {city:"Beijing"})
    R->>R: Execute tool get_weather
    R-->>A: Tool result: {temp:"20°C"}
    A-->>R: event(ToolResponse: {temp:"20°C"})

    Note over A,R: LLM generates final reply
    A->>A: Call LLM again (with tool result)
    A-->>R: event(Content: "The current temperature in Beijing is 20°C")
    A-->>R: event(RunnerCompletion)
    R-->>S: channel closed

    Note over S,C: Server converts and wraps
    S->>S: ToolCall event → DataPart (function_call)
    S->>S: ToolResponse event → DataPart (function_response)
    S->>S: Content event → TextPart
    S->>S: 3 messages → wrapped as Task

    S-->>C: JSON-RPC Response: Task
    Note left of C: history: [function_call msg, function_response msg]<br/>artifacts: [TextPart "The current temperature in Beijing is 20°C"]

    C->>C: Parse history → ToolCall/ToolResponse event
    C->>C: Parse artifacts → Content event
    C->>C: Output event.Event sequence downstream
```

### Streaming (message/stream)

The Server converts each Agent event in real-time to an SSE `TaskArtifactUpdateEvent` and pushes it to the Client. Tool calls and tool responses are sent as complete events, while text content is pushed incrementally.

```mermaid
sequenceDiagram
    participant C as A2AAgent (Client)
    participant S as A2A Server
    participant R as Agent Runner
    participant A as Agent + LLM

    C->>C: Invocation → protocol.Message
    C->>S: HTTP POST message/stream (TextPart: "What's the weather in Beijing?")
    S->>S: protocol.Message → model.Message
    S->>S: BuildTask → taskID
    S-->>C: SSE: TaskStatusUpdateEvent (submitted) [Client filters]
    S->>R: runner.Run(userID, ctxID, message)
    R->>A: agent.Run(ctx, invocation)

    Note over A,S: Tool call (complete event)
    A-->>R: event(ToolCall: get_weather)
    R-->>S: event
    S->>S: ToolCall → DataPart (function_call)
    S-->>C: SSE: ArtifactUpdate {DataPart, llm_response_id: "chatcmpl-1"}
    C->>C: DataPart → ToolCall event

    R->>R: Execute tool get_weather
    A-->>R: event(ToolResponse: {temp:"20°C"})
    R-->>S: event
    S->>S: ToolResponse → DataPart (function_response)
    S-->>C: SSE: ArtifactUpdate {DataPart, llm_response_id: "chatcmpl-1"}
    C->>C: DataPart → ToolResponse event

    Note over A,S: Final reply (incremental push)
    A-->>R: event(Delta: "The current")
    R-->>S: event
    S-->>C: SSE: ArtifactUpdate {TextPart: "The current", llm_response_id: "chatcmpl-2"}
    C->>C: TextPart → Delta event

    A-->>R: event(Delta: " temperature is 20°C")
    R-->>S: event
    S-->>C: SSE: ArtifactUpdate {TextPart: " temperature is 20°C", llm_response_id: "chatcmpl-2"}
    C->>C: Same llm_response_id → aggregate into same message

    Note over S,C: Stream ends
    A-->>R: event(RunnerCompletion)
    R-->>S: channel closed
    S-->>C: SSE: ArtifactUpdate (lastChunk=true) [Client filters]
    S-->>C: SSE: TaskStatusUpdateEvent (completed) [Client filters]
    S->>S: CleanTask(taskID)
```

### Client-Side Filtering Rules

- `TaskStatusUpdateEvent` (submitted/completed): Task lifecycle signals, no user content
- `TaskArtifactUpdateEvent` with `lastChunk=true`: Stream end signal or aggregated result

### Role of `llm_response_id`

The Server includes `llm_response_id` in the Metadata of each response (from the ID returned by the LLM API, such as OpenAI's `chatcmpl-xxx`). All events produced by the same LLM call share the same `llm_response_id`. When the Agent makes a second LLM call (e.g., the final reply after a tool call), the `llm_response_id` changes. The Client uses this to determine that multiple incremental events belong to the same message.

This mechanism is primarily used for message aggregation in AG-UI scenarios — AG-UI's translator uses `rsp.ID` to decide when to emit `TextMessageStart`/`TextMessageEnd` events, and a change in `llm_response_id` indicates a new message has started.

---

## Reasoning Content Transmission

Model reasoning processes (e.g., DeepSeek R1) are marked via `TextPart.metadata.thought`:


| Direction      | ReasoningContent                            | Content                          |
| -------------- | ------------------------------------------- | -------------------------------- |
| Agent → A2A   | `TextPart` + `metadata: {thought: true}`    | `TextPart` (no thought marker)   |
| A2A → Agent   | `thought=true` → restored as `ReasoningContent` | No marker → restored as `Content` |

A single Message can contain both reasoning content and formal reply as two TextParts.

---

## Metadata Specification

### Request Direction (Client → Server)


| Carrier          | Field           | Description                                        |
| ---------------- | --------------- | -------------------------------------------------- |
| HTTP Header      | `X-User-ID`    | User identifier (primary source)                   |
| HTTP Header      | `traceparent`  | W3C Trace Context (auto-injected by OpenTelemetry) |
| Message.Metadata | `invocation_id`| Client-side invocation ID for trace correlation    |
| Message.Metadata | `user_id`      | User identifier (supplementary)                    |

### Response Direction (Server → Client)


| Carrier                   | Field             | Description                                                                                          |
| ------------------------- | ----------------- | ---------------------------------------------------------------------------------------------------- |
| Message/Artifact Metadata | `object_type`    | Event business type (`chat.completion`, `tool.response`, etc.)                                       |
| Message/Artifact Metadata | `tag`            | Event tag (distinguishes code execution vs code execution result)                                    |
| Message/Artifact Metadata | `llm_response_id`| LLM response ID (used for Client-side message aggregation, e.g., OpenAI's `chatcmpl-xxx`)           |
| Part Metadata             | `type`           | Data semantic type (`function_call`, `function_response`, `executable_code`, `code_execution_result`) |
| Part Metadata             | `thought`        | Whether this is reasoning/thinking content                                                           |

---

## Network Packet Examples

### Non-streaming: Request and Response with Tool Call

**Request:**

```http
POST / HTTP/1.1
Host: agent.example.com
Content-Type: application/json
X-User-ID: user_12345
traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01

{
  "jsonrpc": "2.0",
  "id": "req-001",
  "method": "message/send",
  "params": {
    "message": {
      "kind": "message",
      "messageId": "msg-001",
      "role": "user",
      "contextId": "ctx-001",
      "parts": [
        { "kind": "text", "text": "What's the weather in Beijing?" }
      ],
      "metadata": {
        "invocation_id": "inv-001",
        "user_id": "user_12345"
      }
    }
  }
}
```

**Response (Task, with tool call intermediate process):**

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": "req-001",
  "result": {
    "id": "task-001",
    "contextId": "ctx-001",
    "status": {
      "state": "completed",
      "timestamp": "2025-01-23T10:30:00Z"
    },
    "history": [
      {
        "kind": "message",
        "messageId": "msg-tool-call",
        "role": "agent",
        "parts": [
          {
            "kind": "data",
            "data": {
              "id": "call_001",
              "type": "function",
              "name": "get_weather",
              "args": "{\"city\":\"Beijing\"}"
            },
            "metadata": { "type": "function_call" }
          }
        ],
        "metadata": {
          "object_type": "chat.completion",
          "tag": "",
          "llm_response_id": "chatcmpl-abc123"
        }
      },
      {
        "kind": "message",
        "messageId": "msg-tool-resp",
        "role": "agent",
        "parts": [
          {
            "kind": "data",
            "data": {
              "id": "call_001",
              "name": "get_weather",
              "response": "{\"temp\":\"20°C\",\"condition\":\"sunny\"}"
            },
            "metadata": { "type": "function_response" }
          }
        ],
        "metadata": {
          "object_type": "tool.response",
          "tag": "",
          "llm_response_id": "chatcmpl-abc123"
        }
      }
    ],
    "artifacts": [
      {
        "artifactId": "msg-final",
        "parts": [
          { "kind": "text", "text": "The current temperature in Beijing is 20°C, sunny." }
        ]
      }
    ]
  }
}
```

### Streaming: SSE Event Sequence with Tool Call

**Request:**

```http
POST / HTTP/1.1
Host: agent.example.com
Content-Type: application/json
X-User-ID: user_12345
Accept: text/event-stream

{
  "jsonrpc": "2.0",
  "id": "req-002",
  "method": "message/stream",
  "params": {
    "message": {
      "kind": "message",
      "messageId": "msg-002",
      "role": "user",
      "contextId": "ctx-001",
      "parts": [
        { "kind": "text", "text": "What's the weather in Beijing?" }
      ],
      "metadata": {
        "invocation_id": "inv-002",
        "user_id": "user_12345"
      }
    }
  }
}
```

**SSE Response:**

```
event: message
data: {"kind":"status-update","taskId":"task-002","contextId":"ctx-001","status":{"state":"submitted","timestamp":"2025-01-23T10:30:00Z"},"final":false}

event: message
data: {"kind":"artifact-update","taskId":"task-002","contextId":"ctx-001","artifact":{"artifactId":"chatcmpl-abc123","parts":[{"kind":"data","data":{"id":"call_001","type":"function","name":"get_weather","args":"{\"city\":\"Beijing\"}"},"metadata":{"type":"function_call"}}]},"lastChunk":false,"metadata":{"object_type":"chat.completion","tag":"","llm_response_id":"chatcmpl-abc123"}}

event: message
data: {"kind":"artifact-update","taskId":"task-002","contextId":"ctx-001","artifact":{"artifactId":"chatcmpl-abc123","parts":[{"kind":"data","data":{"id":"call_001","name":"get_weather","response":"{\"temp\":\"20°C\"}"},"metadata":{"type":"function_response"}}]},"lastChunk":false,"metadata":{"object_type":"tool.response","tag":"","llm_response_id":"chatcmpl-abc123"}}

event: message
data: {"kind":"artifact-update","taskId":"task-002","contextId":"ctx-001","artifact":{"artifactId":"chatcmpl-def456","parts":[{"kind":"text","text":"The current"}]},"lastChunk":false,"metadata":{"object_type":"chat.completion.chunk","tag":"","llm_response_id":"chatcmpl-def456"}}

event: message
data: {"kind":"artifact-update","taskId":"task-002","contextId":"ctx-001","artifact":{"artifactId":"chatcmpl-def456","parts":[{"kind":"text","text":" temperature in Beijing is 20°C, sunny."}]},"lastChunk":false,"metadata":{"object_type":"chat.completion.chunk","tag":"","llm_response_id":"chatcmpl-def456"}}

event: message
data: {"kind":"artifact-update","taskId":"task-002","contextId":"ctx-001","artifact":{"parts":[]},"lastChunk":true}

event: message
data: {"kind":"status-update","taskId":"task-002","contextId":"ctx-001","status":{"state":"completed","timestamp":"2025-01-23T10:30:05Z"},"final":true}

```

### Non-streaming: Response with Reasoning Content

```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "id": "req-003",
  "result": {
    "kind": "message",
    "messageId": "msg-thinking-001",
    "role": "agent",
    "contextId": "ctx-001",
    "parts": [
      {
        "kind": "text",
        "text": "Let me analyze this step by step...",
        "metadata": { "thought": true }
      },
      {
        "kind": "text",
        "text": "The current temperature in Beijing is 20°C."
      }
    ],
    "metadata": {
      "object_type": "chat.completion",
      "tag": "",
      "llm_response_id": "chatcmpl-ghi789"
    }
  }
}
```

### Streaming: Parallel Tool Calls

Multiple tool calls are placed in the `parts` array of the same `artifact-update`:

```
event: message
data: {"kind":"artifact-update","taskId":"task-003","contextId":"ctx-001","artifact":{"artifactId":"chatcmpl-jkl012","parts":[{"kind":"data","data":{"id":"call_001","type":"function","name":"get_weather","args":"{\"city\":\"Beijing\"}"},"metadata":{"type":"function_call"}},{"kind":"data","data":{"id":"call_002","type":"function","name":"get_weather","args":"{\"city\":\"Shanghai\"}"},"metadata":{"type":"function_call"}}]},"lastChunk":false,"metadata":{"object_type":"chat.completion","tag":"","llm_response_id":"chatcmpl-jkl012"}}

event: message
data: {"kind":"artifact-update","taskId":"task-003","contextId":"ctx-001","artifact":{"artifactId":"chatcmpl-jkl012","parts":[{"kind":"data","data":{"id":"call_001","name":"get_weather","response":"{\"temp\":\"20°C\"}"},"metadata":{"type":"function_response"}},{"kind":"data","data":{"id":"call_002","name":"get_weather","response":"{\"temp\":\"22°C\"}"},"metadata":{"type":"function_response"}}]},"lastChunk":false,"metadata":{"object_type":"tool.response","tag":"","llm_response_id":"chatcmpl-jkl012"}}

```

---

## Distributed Tracing

### Trace Context Propagation

Distributed tracing is propagated through HTTP Headers, following the [W3C Trace Context](https://www.w3.org/TR/trace-context/) standard:

- **Client side**: Automatically injects the `traceparent` header into HTTP requests via OpenTelemetry's `TextMapPropagator`
- **Server side**: Extracts trace context from the `traceparent` HTTP request header and injects it into `context.Context`, making it available throughout the entire call chain

### Application-Layer Tracing Fields

In addition to HTTP-layer trace context, supplementary tracing information is passed through `Message.Metadata`:

- **`invocation_id`** (Request Metadata): Identifies a single Agent invocation on the Client side; the Server can use it for log correlation
- **`llm_response_id`** (Response Metadata): Identifies the original LLM response ID; the Client uses it for message aggregation
