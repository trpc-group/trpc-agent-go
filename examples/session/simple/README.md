# Session Management Demo

This example demonstrates advanced session management capabilities using the `Runner` component. It showcases how to manage multiple conversation sessions with different storage backends.

## What is Session Management?

This implementation highlights the power of session management in conversational AI:

- **Multiple Sessions**: Create and switch between multiple independent conversation contexts
- **Persistent Storage**: Support for Redis, PostgreSQL, and MySQL backends
- **Session Discovery**: List and switch between existing sessions


### Key Features

- **Session Creation**: Start new conversation sessions with `/new`
- **Session Switching**: Switch between sessions with `/use <id>`
- **Session Listing**: View all active sessions with `/sessions`
- **History Recap**: Ask the agent to summarize conversation with `/history`
- **Backend Flexibility**: Choose from in-memory, Redis, PostgreSQL, or MySQL storage
- **Context Preservation**: Each session maintains independent conversation history

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)
- Optional: Redis/PostgreSQL/MySQL server (depending on backend choice)

## Environment Variables

**Required:**

| Variable                | Description                                    | Required | Default Value               |
| ----------------------- | ---------------------------------------------- | -------- | --------------------------- |
| `OPENAI_API_KEY`        | API key for the openai model                   | **Yes**  | -                           |
| `OPENAI_BASE_URL`       | Base URL for the openai model API endpoint     | **Yes**  | `https://api.openai.com/v1` |

### Backend-Specific Variables

**Redis:**
| Variable      | Description           | Default Value     |
| ------------- | --------------------- | ----------------- |
| `REDIS_ADDR`  | Redis server address  | `localhost:6379`  |

**PostgreSQL:**
| Variable      | Description           | Default Value     |
| ------------- | --------------------- | ----------------- |
| `PG_HOST`     | PostgreSQL host       | `localhost`       |
| `PG_PORT`     | PostgreSQL port       | `5432`            |
| `PG_USER`     | PostgreSQL user       | `root`            |
| `PG_PASSWORD` | PostgreSQL password   | ``                |
| `PG_DATABASE` | PostgreSQL database   | `trpc-agent-go`   |

**MySQL:**
| Variable         | Description        | Default Value    |
| ---------------- | ------------------ | ---------------- |
| `MYSQL_HOST`     | MySQL host         | `localhost`      |
| `MYSQL_PORT`     | MySQL port         | `3306`           |
| `MYSQL_USER`     | MySQL user         | `root`           |
| `MYSQL_PASSWORD` | MySQL password     | ``               |
| `MYSQL_DATABASE` | MySQL database     | `trpc_agent_go`  |

## Command Line Arguments

| Argument           | Description                                         | Default Value    |
| ------------------ | --------------------------------------------------- | ---------------- |
| `-model`           | Name of the model to use                            | `deepseek-chat`  |
| `-session`         | Session backend: inmemory/redis/pgsql/mysql         | `inmemory`       |
| `-streaming`       | Enable streaming mode for responses                 | `true`           |
| `-event-limit`     | Maximum number of events to store per session       | `100`            |
| `-session-ttl`     | Session time-to-live duration                       | `24h`            |

## Usage

### Basic Usage with In-Memory Backend

```bash
cd examples/session
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"
go run .
```

### Custom Event Limit and Session TTL

Control how many events are stored and how long sessions live:

```bash
# Store up to 200 events per session, with 48 hour TTL
go run . -event-limit 200 -session-ttl 48h

# Store 50 events, with 6 hour TTL (useful for testing)
go run . -event-limit 50 -session-ttl 6h
```

**Event Limit**: Controls memory usage and query performance. Lower values = less memory, faster queries.

**Session TTL**: How long inactive sessions persist before cleanup. Longer TTL = better user experience for returning users.

### With Redis Backend

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
go run . -session redis
```

Custom Redis address:
```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export REDIS_ADDR="localhost:6380"
go run . -session redis
```

### With PostgreSQL Backend

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export PG_PASSWORD="your-password"
go run . -session pgsql
```

Custom PostgreSQL configuration:
```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export PG_HOST="localhost"
export PG_USER="postgres"
export PG_PASSWORD="your-password"
export PG_DATABASE="sessions_db"
go run . -session pgsql
```

### With MySQL Backend

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export MYSQL_PASSWORD="your-password"
go run . -session mysql
```

Custom MySQL configuration:
```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export MYSQL_HOST="localhost"
export MYSQL_USER="root"
export MYSQL_PASSWORD="your-password"
export MYSQL_DATABASE="sessions_db"
go run . -session mysql
```

## Session Commands

The example supports the following session management commands:

| Command            | Description                                        |
| ------------------ | -------------------------------------------------- |
| `/new`             | Create a new session with a fresh conversation     |
| `/sessions`        | List all known session IDs                         |
| `/use <id>`        | Switch to an existing session or create a new one  |
| `/history`         | Ask the assistant to recap the conversation        |
| `/exit`            | End the conversation                               |

## Session Management Workflow

### Creating Multiple Sessions

```
👤 You: Hello, I'm working on project A
🤖 Assistant: Hello! I'd be happy to help you with project A...

👤 You: /new
🆕 Started new session!
   Previous: chat-session-1703123456
   Current:  chat-session-1703123499
   (Conversation history has been reset)

👤 You: Hi, this is about project B
🤖 Assistant: Hello! Tell me about project B...
```

### Listing Sessions

```
👤 You: /sessions
🗂 Session roster:
     chat-session-1703123456
   * chat-session-1703123499
```

The `*` indicates the currently active session.

### Switching Between Sessions

```
👤 You: /use chat-session-1703123456
🔁 Switched to session chat-session-1703123456

👤 You: What were we discussing?
🤖 Assistant: We were talking about project A...
```

### Viewing Session History

```
👤 You: /history
🤖 Assistant: In our conversation so far, we discussed:
1. You mentioned working on project A
2. I offered to help with the project
...
```

## Session Storage Backends

### In-Memory (Default)

- **Best for**: Development, testing, quick demos
- **Pros**: 
  - Fast
  - No external dependencies
  - Zero configuration
- **Cons**: 
  - Data lost on restart
  - Not suitable for distributed systems
  - Limited to single process

### Redis

- **Best for**: Production, distributed applications
- **Pros**: 
  - Persistent storage
  - Supports multiple instances
  - Automatic TTL/expiration
  - High performance
  - Pub/sub capabilities
- **Cons**: 
  - Requires Redis server
  - Additional infrastructure

### PostgreSQL

- **Best for**: Enterprise applications, complex queries
- **Pros**: 
  - ACID guarantees
  - Relational data model
  - JSONB storage for efficient JSON operations
  - Soft delete support
  - Built-in TTL cleanup
  - Rich query capabilities
- **Cons**: 
  - Requires PostgreSQL server
  - Heavier footprint

**PostgreSQL Features:**
- JSONB columns for session state storage
- Soft delete (data marked as deleted, not removed)
- Automatic TTL cleanup of expired sessions
- Partial unique indexes for session recreation
- Automatic schema management

### MySQL

- **Best for**: MySQL-based infrastructure, legacy systems
- **Pros**: 
  - Wide adoption
  - JSON storage support
  - ACID guarantees
  - Automatic TTL cleanup
  - Compatible with MySQL 5.x+
- **Cons**: 
  - Requires MySQL server
  - JSON operations less efficient than PostgreSQL JSONB

**MySQL Features:**
- JSON columns for session data
- Soft delete support
- TTL-based expiration
- Automatic schema creation
- Compatible with MySQL 5.7+

## Example Session

```
🚀 Session Management Demo
Model: deepseek-chat
Streaming: true
Session Backend: PostgreSQL (localhost:5432/trpc-agent-go)
==================================================
✅ Chat ready! Session: chat-session-1703123456

💡 Session commands:
   /history   - Ask the assistant to recap our conversation
   /new       - Start a brand-new session ID
   /sessions  - List known session IDs
   /use <id>  - Switch to an existing (or new) session
   /exit      - End the conversation

👤 You: Hello! I'm planning a trip to Japan
🤖 Assistant: That's exciting! Japan is a wonderful destination...

👤 You: /new
🆕 Started new session!
   Previous: chat-session-1703123456
   Current:  chat-session-1703123500

👤 You: Hi, I need help with Python coding
🤖 Assistant: I'd be happy to help with Python!...

👤 You: /sessions
🗂 Session roster:
     chat-session-1703123456
   * chat-session-1703123500

👤 You: /use chat-session-1703123456
🔁 Switched to session chat-session-1703123456

👤 You: What were we talking about?
🤖 Assistant: We were discussing your trip to Japan...

👤 You: /exit
👋 Goodbye!
```

## Key Differences from Runner Example

This session example differs from the basic `examples/runner` in several ways:

| Feature                  | examples/runner         | examples/session           |
| ------------------------ | ----------------------- | -------------------------- |
| **Focus**                | Basic Runner usage      | Session management         |
| **Session Backend**      | In-memory only          | Multiple backends          |
| **Session Commands**     | None                    | /new, /sessions, /use      |
| **Tools**                | Calculator, Time        | None (focus on sessions)   |
| **Complexity**           | Minimal                 | Advanced                   |
| **Use Case**             | Quick start, learning   | Production patterns        |

## Help

To see all available command line options:

```bash
go run . --help
```

## Next Steps

After exploring session management:

1. **Integrate with your application**: Use the session service in your own agent
2. **Customize storage**: Configure TTL, cleanup intervals, table prefixes
3. **Add authentication**: Implement user-based session isolation
4. **Monitor sessions**: Track active sessions and usage patterns
5. **Scale horizontally**: Deploy multiple instances with Redis/PostgreSQL backend
