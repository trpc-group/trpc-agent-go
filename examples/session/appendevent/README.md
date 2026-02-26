# AppendEvent Demo

This example demonstrates how to directly append events to session without invoking the model. This is useful for scenarios like:

- Pre-loading conversation history
- Inserting system messages or context
- Recording user actions or metadata
- Building conversation context from external sources

## What is an Event?

An **Event** represents any message or action in a conversation. Events can contain:

- **User messages**: User requests, queries, or input
- **Assistant messages**: Model responses and replies
- **System messages**: System instructions or context
- **Tool calls**: Function calls made by the agent
- **Tool responses**: Results from tool executions
- **Error events**: Error information when something goes wrong

When you use `Runner.Run()`, the framework automatically creates events for both user messages and assistant responses. All events are persisted to the session and form the complete conversation history.

## What is AppendEvent?

`AppendEvent` allows you to directly persist messages to a session without going through the model. This is particularly useful when you want to:

1. **Pre-load context**: Add system messages or initial context before the first user query
2. **Record metadata**: Store user actions, preferences, or other metadata as events
3. **Import history**: Load conversation history from external sources
4. **Build context**: Construct conversation context programmatically

When you later use `Runner.Run()`, all previously appended events are automatically loaded and included in the conversation context.

## Key Features

- **Direct Event Appending**: Append user, system, or assistant messages directly to session
- **Interactive Commands**: Use commands to append messages and view events
- **Session Management**: Create, switch, and list multiple sessions
- **Event Inspection**: View all events in the current session
- **Automatic Context Loading**: Runner automatically includes appended events in conversation context

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

**Required:**

| Variable          | Description                                | Required | Default Value               |
| ----------------- | ------------------------------------------ | -------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the openai model               | **Yes**  | -                           |
| `OPENAI_BASE_URL` | Base URL for the openai model API endpoint | **Yes**  | `https://api.openai.com/v1` |

## Usage

### Basic Usage

```bash
cd examples/session/appendevent
go run main.go helper.go
```

### Command Line Options

```bash
go run main.go helper.go -model deepseek-chat -streaming=true
```

| Option       | Description              | Default         |
| ------------ | ------------------------ | --------------- |
| `-model`     | Name of the model to use | `deepseek-chat` |
| `-streaming` | Enable streaming mode    | `true`          |

## Commands

Once the program starts, you can use the following commands:

| Command                    | Description                               |
| -------------------------- | ----------------------------------------- |
| `/append <message>`        | Append a user message directly to session |
| `/append-system <message>` | Append a system message to session        |
| `/append-assistant <msg>`  | Append an assistant message to session    |
| `/events`                  | List all events in current session        |
| `/new`                     | Start a brand-new session ID              |
| `/sessions`                | List known session IDs                    |
| `/use <id>`                | Switch to an existing (or new) session    |
| `/exit`                    | End the conversation                      |

## Event Required Fields

An Event can represent **both user requests and model responses**. When creating an event using `event.NewResponseEvent()`, the following fields are **required**:

### 1. Function Parameters (Required)

- **`invocationID`** (string): Unique identifier for this invocation
  - Recommended: Use `uuid.New().String()`
- **`author`** (string): Event author
  - For user messages: `"user"`
  - For system messages: `"system"`
  - For assistant messages: agent name or `"assistant"`
- **`response`** (\*model.Response): Response object with Choices

### 2. Response Fields (Required)

- **`Choices`** ([]model.Choice): At least one Choice must be provided
  - **`Index`** (int): Choice index, typically `0`
  - **`Message`** (model.Message): Message object
    - **`Role`** (string): Message role (e.g., `model.RoleUser`, `model.RoleSystem`, `model.RoleAssistant`)
    - **`Content`** (string) or **`ContentParts`** ([]model.ContentPart): At least one must be non-empty

### 3. Auto-Generated Fields

These fields are automatically set by `event.NewResponseEvent()`:

- **`ID`**: Auto-generated UUID
- **`Timestamp`**: Auto-set to current time
- **`Version`**: Auto-set to `CurrentVersion`
- **`Response`**: Auto-initialized if not provided

### 4. Persistence Requirements

For an event to be persisted to session, it must satisfy:

- `Response != nil`
- `!IsPartial` (or has `StateDelta`)
- `IsValidContent()` returns `true` (Choices with `Message.Content`, `Message.ContentParts`, or tool calls)

### 5. Optional but Recommended Fields

- **`RequestID`**: For request tracking (set manually)
- **`FilterKey`**: For event filtering (auto-set by framework)
- **`Done`**: Indicates if the event is final (typically `false` for non-final events)

## Example: Creating an Event

```go
// Create a user message
message := model.NewUserMessage("Hello, world!")

// Create event with required fields
invocationID := uuid.New().String()
evt := event.NewResponseEvent(
    invocationID, // Required: unique invocation identifier
    "user",       // Required: event author
    &model.Response{
        Done: false, // Recommended: false for non-final events
        Choices: []model.Choice{
            {
                Index:   0,       // Required: choice index
                Message: message, // Required: message with Content
            },
        },
    },
)

// Optional: Set RequestID for tracking
evt.RequestID = uuid.New().String()

// Append to session
sessionService.AppendEvent(ctx, sess, evt)
```

## Example Workflow

1. **Start the program**:

   ```bash
   go run main.go helper.go
   ```

2. **Append a system message**:

   ```
   👤 You: /append-system You are a helpful assistant specialized in Go programming.
   ✅ Message appended to session (author: system)
   ```

3. **Append a user message**:

   ```
   👤 You: /append Hello, I'm learning Go.
   ✅ Message appended to session (author: user)
   ```

4. **View events**:

   ```
   👤 You: /events
   📋 Session: session-1234567890
      Total events: 2
      ...
   ```

5. **Send a normal message** (Runner will automatically include appended events):
   ```
   👤 You: What is a goroutine?
   🤖 Assistant: A goroutine is a lightweight thread managed by the Go runtime...
   ```

## How It Works

**Important**: An Event can represent **both user requests and model responses**. The session stores all events, creating a complete conversation history.

1. **Direct Appending**: When you use `/append` commands, messages are directly persisted to the session using `sessionService.AppendEvent()` without invoking the model. These events can be user messages, system messages, or assistant messages.

2. **Automatic Event Creation**: When you send a normal message (not a command), `Runner.Run()` automatically:

   - Creates an event for the user message and appends it to session
   - Processes the message through the model
   - Creates events for model responses and appends them to session
   - All events (user + assistant) are persisted to the session

3. **Automatic Loading**: When processing a new message, `Runner.Run()` automatically:

   - Loads the session (which contains all previously appended events)
   - Converts session events to messages
   - Includes all messages (appended + current) in the conversation context
   - Sends everything to the model together

4. **Context Preservation**: All events (both user requests and model responses) become part of the conversation history and are available to the model in subsequent interactions.

## Code Structure

- **`main.go`**: Core append event functionality and command handling
- **`helper.go`**: Response processing, session management, and utility functions

## Key Functions

### Append Event Functions

- `appendMessageToSession()`: Core function that creates and persists events
- `appendUserMessage()`: Append a user message
- `appendSystemMessage()`: Append a system message
- `appendAssistantMessage()`: Append an assistant message

### Command Handlers

- `handleCommand()`: Main command dispatcher
- `handleAppendSystem()`: Handle `/append-system` command
- `handleAppendAssistant()`: Handle `/append-assistant` command
- `handleAppendUser()`: Handle `/append` command

### Session Management

- `listEvents()`: Display all events in current session
- `startNewSession()`: Create a new session
- `switchSession()`: Switch to a different session
- `listSessions()`: List all known sessions

## Notes

- Events appended directly to session are **not** processed by the model immediately
- They are stored and will be included when you use `Runner.Run()` for normal messages
- Summary generation is typically deferred until assistant responses (see `runner.go:531`)
- Use the same `(appName, userID, sessionID)` to ensure events are in the same session
