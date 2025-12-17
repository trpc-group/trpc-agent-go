# tRPC-Agent-Go Ecosystem Development Guide

This document analyzes the modules in the tRPC-Agent-Go framework that need ecosystem development, explains the interfaces that need to be implemented, and provides contribution guidance.

Note: All community-built components are contributed directly to the corresponding directory under the GitHub open source repository, such as `model/somemodel`, `tool/sometool`, `agent/someagent`, etc.

When contributing, create a reasonably named subfolder under the corresponding contribution module directory, then implement the corresponding module interface, and provide rich test cases and example samples.

## Ecosystem Development Module Analysis

### 1. Agent Ecosystem

**Goal:** Encapsulate and adapt third-party Agent frameworks

**Interface Definition:** [agent.Agent](https://github.com/trpc-group/trpc-agent-go/blob/main/agent/agent.go)

**Existing Implementation Reference:** [LLMAgent](https://github.com/trpc-group/trpc-agent-go/tree/main/agent/llmagent)

**Implementation Notes:**

- The `Run` method must return an event channel, supporting streaming responses
- The `Tools` method returns the list of tools available to the Agent
- The `Info` method provides basic information about the Agent
- `SubAgents` and `FindSubAgent` support Agent composition patterns
- Reference LLMAgent implementation to understand event handling and error handling mechanisms

**Implementation Example:**

```go
package langchain

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

type LangChainAdapter struct {
    config *Config
    client *langchain.Client
}

func New(config *Config) (agent.Agent, error) {
    client := langchain.NewClient(config.Endpoint, config.APIKey)
    
    return &LangChainAdapter{
        config: config,
        client: client,
    }, nil
}

func (a *LangChainAdapter) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
    events := make(chan *event.Event)
    
    go func() {
        defer close(events)
        
        response, err := a.client.Call(ctx, invocation.Messages)
        if err != nil {
            events <- &event.Event{
                Type: event.TypeError,
                Error: err,
            }
            return
        }
        
        events <- &event.Event{
            Type: event.TypeResponse,
            Response: &model.Response{
                Content: response.Content,
            },
        }
    }()
    
    return events, nil
}

func (a *LangChainAdapter) Tools() []tool.Tool {
    return a.config.Tools
}

func (a *LangChainAdapter) Info() agent.Info {
    return agent.Info{
        Name:        "langchain-adapter",
        Description: "LangChain framework adapter",
    }
}

func (a *LangChainAdapter) SubAgents() []agent.Agent {
    return nil
}

func (a *LangChainAdapter) FindSubAgent(name string) agent.Agent {
    return nil
}
```

**Open Source Components That Can Be Integrated:**

- LangChain adapter
- LangGraph adapter

**Contribution Method:**

- Create components under the corresponding directory (create a subdirectory with the corresponding component name)
- Contribute directly to `https://github.com/trpc-group/trpc-agent-go/agent/`

### 2. Model Ecosystem

**Goal:** Support more model providers

**Interface Definition:** [model.Model](https://github.com/trpc-group/trpc-agent-go/blob/main/model/model.go)

**Existing Implementation Reference:** [OpenAI Model](https://github.com/trpc-group/trpc-agent-go/tree/main/model/openai)

**Implementation Notes:**

- The `GenerateContent` method must support streaming responses, returning an event channel
- Distinguish between system-level errors (return error) and API-level errors (Response.Error)
- Implement the `Info` method to provide basic model information
- Reference OpenAI implementation to understand request building and response handling
- Support context cancellation and timeout control

**Implementation Example:**

```go
package gemini

import (
    "context"
    "fmt"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

type GeminiModel struct {
    config *Config
    client *gemini.Client
}

func New(config *Config) (model.Model, error) {
    client := gemini.NewClient(config.APIKey)
    
    return &GeminiModel{
        config: config,
        client: client,
    }, nil
}

func (g *GeminiModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
    if request == nil {
        return nil, fmt.Errorf("request cannot be nil")
    }
    
    responses := make(chan *model.Response)
    
    go func() {
        defer close(responses)
        
        // Call Gemini API.
        stream, err := g.client.GenerateContent(ctx, request.Messages)
        if err != nil {
            responses <- &model.Response{
                Error: &model.Error{
                    Message: err.Error(),
                },
            }
            return
        }
        
        for chunk := range stream {
            responses <- &model.Response{
                Content: chunk.Content,
            }
        }
    }()
    
    return responses, nil
}

func (g *GeminiModel) Info() model.Info {
    return model.Info{
        Name: "gemini-pro",
    }
}
```

**Open Source Components That Can Be Integrated:**

- Google Gemini model support
- Anthropic Claude model support
- Ollama local model support

**Contribution Method:**

- Create components under the corresponding directory (create a subdirectory with the corresponding component name)
- Contribute directly to `https://github.com/trpc-group/trpc-agent-go/model/`

### 3. Tool Ecosystem

**Goal:** Integrate more third-party tools

**Interface Definition:** 

- [tool.Tool](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/tool.go) - Single tool interface
- [tool.ToolSet](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/toolset.go) - Tool collection interface

**Existing Implementation Reference:** [DuckDuckGo Tool](https://github.com/trpc-group/trpc-agent-go/tree/main/tool/duckduckgo)

**Implementation Notes:**

**Single Tool Implementation:**

- The `Declaration` method must return complete tool metadata
- The `Call` method receives JSON format parameters and returns results of any type
- Use JSON Schema to define input and output formats
- Reference DuckDuckGo implementation to understand tool calling and error handling
- Support context cancellation and timeout control

**Tool Collection Implementation:**

- The `Tools` method returns available tool lists based on context
- The `Close` method releases resources held by the tool collection
- Support dynamic tool loading and configuration
- Implement tool lifecycle management

**Implementation Example:**

**Single Tool Implementation:**

```go
package weather

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/tool"
)

type WeatherTool struct {
    apiKey string
    client *http.Client
}

func New(apiKey string) tool.CallableTool {
    return &WeatherTool{
        apiKey: apiKey,
        client: &http.Client{},
    }
}

func (w *WeatherTool) Declaration() *tool.Declaration {
    return &tool.Declaration{
        Name:        "get_weather",
        Description: "Get current weather information for a location",
        InputSchema: &tool.Schema{
            Type: "object",
            Properties: map[string]*tool.Schema{
                "location": {
                    Type:        "string",
                    Description: "City name or coordinates",
                },
            },
            Required: []string{"location"},
        },
        OutputSchema: &tool.Schema{
            Type: "object",
            Properties: map[string]*tool.Schema{
                "temperature": {Type: "number"},
                "condition":   {Type: "string"},
                "humidity":    {Type: "number"},
            },
        },
    }
}

func (w *WeatherTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
    var args struct {
        Location string `json:"location"`
    }
    
    if err := json.Unmarshal(jsonArgs, &args); err != nil {
        return nil, fmt.Errorf("invalid arguments: %w", err)
    }
    
    url := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s", w.apiKey, args.Location)
    
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, err
    }
    
    resp, err := w.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var weatherData map[string]interface{}
    if err := json.NewDecoder(resp.Body).Decode(&weatherData); err != nil {
        return nil, err
    }
    
    return weatherData, nil
}
```

**Tool Collection Implementation:**

```go
package apitools

import (
    "context"
    "sync"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

type APIToolSet struct {
    tools map[string]tool.Tool
    mu    sync.RWMutex
}

func New() *APIToolSet {
    return &APIToolSet{
        tools: make(map[string]tool.Tool),
    }
}

func (a *APIToolSet) AddTool(name string, t tool.Tool) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.tools[name] = t
}

func (a *APIToolSet) RemoveTool(name string) {
    a.mu.Lock()
    defer a.mu.Unlock()
    delete(a.tools, name)
}

func (a *APIToolSet) Tools(ctx context.Context) []tool.Tool {
    a.mu.RLock()
    defer a.mu.RUnlock()
    
    var result []tool.Tool
    for _, t := range a.tools {
        result = append(result, t)
    }
    return result
}

func (a *APIToolSet) Close() error {
    a.mu.Lock()
    defer a.mu.Unlock()
    
    // Clean up resources.
    a.tools = make(map[string]tool.Tool)
    return nil
}

// Name identifies this ToolSet.
func (a *APIToolSet) Name() string { return "api-tools" }
```

**Open Source Components That Can Be Integrated:**

- Search engine tools (Google, Bing)
- Weather query tools
- Calculator tools
- File operation tools
- API tool collections (REST API toolkit)
- Database operation tool collections
- File processing tool collections

**Contribution Method:**

- Create components under the corresponding directory (create a subdirectory with the corresponding component name)
- Contribute directly to `https://github.com/trpc-group/trpc-agent-go/tool/`

### 4. Knowledge Base Ecosystem

**Goal:** Integrate mature RAG components

**Interface Definition:** [knowledge.Knowledge](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/knowledge.go)

**Existing Implementation Reference:** 

* [InMemory Knowledge](https://github.com/trpc-group/trpc-agent-go/tree/main/knowledge/inmemory)

**Implementation Notes:**

- The `Search` method supports context and history records
- Returns relevant documents and relevance scores
- Supports search parameters and result limits
- Reference InMemory implementation to understand search logic and result processing
- Supports vectorized search and semantic matching

**Implementation Example:**

```go
package weaviate

import (
    "context"
    "fmt"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

type WeaviateKnowledge struct {
    config *Config
    client *weaviate.Client
}

func New(config *Config) (knowledge.Knowledge, error) {
    client := weaviate.NewClient(config.Endpoint, config.APIKey)
    
    return &WeaviateKnowledge{
        config: config,
        client: client,
    }, nil
}

func (w *WeaviateKnowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
    if req.Query == "" {
        return nil, fmt.Errorf("query cannot be empty")
    }
    
    // Build search query.
    query := w.buildQuery(req)
    
    // Execute vector search.
    results, err := w.client.Search(ctx, query)
    if err != nil {
        return nil, err
    }
    
    if len(results) == 0 {
        return nil, fmt.Errorf("no results found")
    }
    
    // Return best matching result.
    bestResult := results[0]
    
    return &knowledge.SearchResult{
        Document: &document.Document{
            ID:      bestResult.ID,
            Content: bestResult.Content,
            Metadata: bestResult.Metadata,
        },
        Score: bestResult.Score,
        Text:  bestResult.Content,
    }, nil
}

func (w *WeaviateKnowledge) buildQuery(req *knowledge.SearchRequest) *weaviate.Query {
    // Build Weaviate query logic.
    return &weaviate.Query{
        Query: req.Query,
        Limit: req.MaxResults,
        Filter: w.buildFilter(req),
    }
}
```

**Open Source Components That Can Be Integrated:**

- Weaviate vector database
- Pinecone vector database
- Qdrant vector database

**Contribution Method:**

- Create components under the corresponding directory (create a subdirectory with the corresponding component name)
- Contribute directly to `https://github.com/trpc-group/trpc-agent-go/knowledge/`

### 5. Session Ecosystem

**Goal:** Support multiple session storage backends, manage user session state and events

**Interface Definition:** [session.Service](https://github.com/trpc-group/trpc-agent-go/blob/main/session/session.go)

**Existing Implementation Reference:**

- [InMemory Session](https://github.com/trpc-group/trpc-agent-go/tree/main/session/inmemory)
- [Redis Session](https://github.com/trpc-group/trpc-agent-go/tree/main/session/redis)

**Implementation Notes:**

- Implement complete Session lifecycle management (create, get, delete, list)
- Support state storage and event recording
- Implement connection pooling and error handling
- Support transactions and consistency
- Can reuse storage module clients
- Reference InMemory and Redis implementations to understand Session management logic

**Implementation Example:**

```go
package postgresql

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

type PostgreSQLService struct {
    db *sql.DB
}

func New(dsn string) (session.Service, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, err
    }
    
    if err := db.Ping(); err != nil {
        return nil, err
    }
    
    return &PostgreSQLService{db: db}, nil
}

func (p *PostgreSQLService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, 
    options ...session.Option) (*session.Session, error) {
    if err := key.CheckSessionKey(); err != nil {
        return nil, err
    }
    
    now := time.Now()
    session := &session.Session{
        ID:        key.SessionID,
        AppName:   key.AppName,
        UserID:    key.UserID,
        State:     state,
        Events:    []event.Event{},
        UpdatedAt: now,
        CreatedAt: now,
    }
    
    // Insert into database.
    _, err := p.db.ExecContext(ctx, `
        INSERT INTO sessions (id, app_name, user_id, state, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6)
    `, session.ID, session.AppName, session.UserID, p.marshalState(state), session.CreatedAt, session.UpdatedAt)
    
    if err != nil {
        return nil, err
    }
    
    return session, nil
}

func (p *PostgreSQLService) GetSession(ctx context.Context, key session.Key, 
    options ...session.Option) (*session.Session, error) {
    if err := key.CheckSessionKey(); err != nil {
        return nil, err
    }
    
    var session session.Session
    var stateData []byte
    
    err := p.db.QueryRowContext(ctx, `
        SELECT id, app_name, user_id, state, created_at, updated_at
        FROM sessions WHERE id = $1
    `, key.SessionID).Scan(&session.ID, &session.AppName, &session.UserID, &stateData, &session.CreatedAt, 
        &session.UpdatedAt)
    
    if err != nil {
        return nil, err
    }
    
    session.State = p.unmarshalState(stateData)
    
    return &session, nil
}

func (p *PostgreSQLService) Close() error {
    return p.db.Close()
}

func (p *PostgreSQLService) marshalState(state session.StateMap) []byte {
    data, _ := json.Marshal(state)
    return data
}

func (p *PostgreSQLService) unmarshalState(data []byte) session.StateMap {
    var state session.StateMap
    json.Unmarshal(data, &state)
    return state
}
```

**Open Source Components That Can Be Integrated:**

- PostgreSQL session storage
- MongoDB session storage
- MySQL session storage
- Cassandra session storage

**Contribution Method:**

- Create components under the corresponding directory (create a subdirectory with the corresponding component name)
- Contribute directly to `https://github.com/trpc-group/trpc-agent-go/session/`

### 6. Memory Ecosystem

**Goal:** Support multiple memory storage backends, manage user long-term memory and personalized information

**Interface Definition:** [memory.Service](https://github.com/trpc-group/trpc-agent-go/blob/main/memory/memory.go)

**Existing Implementation Reference:** [InMemory Memory](https://github.com/trpc-group/trpc-agent-go/tree/main/memory/inmemory)

**Implementation Notes:**

- Implement complete Memory lifecycle management (add, update, delete, search, read)
- Support memory topic classification and search
- Provide memory tool integration (memory_add, memory_search, etc.)
- Implement connection pooling and error handling
- Can reuse storage module clients
- Support memory limits and cleanup mechanisms
- Reference InMemory implementation to understand memory management logic

**Implementation Example:**

```go
package postgresql

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/memory"
    memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

type PostgreSQLMemoryService struct {
    db *sql.DB
    cachedTools map[string]tool.Tool
}

func New(dsn string) (memory.Service, error) {
    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return nil, err
    }
    
    if err := db.Ping(); err != nil {
        return nil, err
    }
    
    service := &PostgreSQLMemoryService{
        db: db,
        cachedTools: make(map[string]tool.Tool),
    }
    
    // Initialize tools.
    service.initTools()
    
    return service, nil
}

func (p *PostgreSQLMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, 
    topics []string) error {
    if err := userKey.CheckUserKey(); err != nil {
        return err
    }
    
    now := time.Now()
    memoryID := p.generateMemoryID(memoryStr)
    
    // Insert memory into database.
    _, err := p.db.ExecContext(ctx, `
        INSERT INTO memories (id, app_name, user_id, memory, topics, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `, memoryID, userKey.AppName, userKey.UserID, memoryStr, p.marshalTopics(topics), now, now)
    
    return err
}

func (p *PostgreSQLMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, 
    query string) ([]*memory.Entry, error) {
    if err := userKey.CheckUserKey(); err != nil {
        return nil, err
    }
    
    // Execute full-text search.
    rows, err := p.db.QueryContext(ctx, `
        SELECT id, app_name, user_id, memory, topics, created_at, updated_at
        FROM memories 
        WHERE app_name = $1 AND user_id = $2 
        AND (memory ILIKE $3 OR topics::text ILIKE $3)
        ORDER BY updated_at DESC
        LIMIT 10
    `, userKey.AppName, userKey.UserID, "%"+query+"%")
    
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    var entries []*memory.Entry
    for rows.Next() {
        var entry memory.Entry
        var topicsData []byte
        
        err := rows.Scan(&entry.ID, &entry.AppName, &entry.UserID, &entry.Memory.Memory, &topicsData, &entry.CreatedAt, 
        &entry.UpdatedAt)
    if err != nil {
        return nil, err
    }
    
        entry.Memory.Topics = p.unmarshalTopics(topicsData)
        entries = append(entries, &entry)
    }
    
    return entries, nil
}

func (p *PostgreSQLMemoryService) Tools() []tool.Tool {
    var tools []tool.Tool
    for _, t := range p.cachedTools {
        tools = append(tools, t)
    }
    return tools
}

func (p *PostgreSQLMemoryService) initTools() {
    p.cachedTools[memory.AddToolName] = memorytool.NewAddTool(p)
    p.cachedTools[memory.SearchToolName] = memorytool.NewSearchTool(p)
    p.cachedTools[memory.LoadToolName] = memorytool.NewLoadTool(p)
}

func (p *PostgreSQLMemoryService) generateMemoryID(memoryStr string) string {
    // Generate unique memory ID.
    return fmt.Sprintf("mem_%d", time.Now().UnixNano())
}

func (p *PostgreSQLMemoryService) marshalTopics(topics []string) []byte {
    data, _ := json.Marshal(topics)
    return data
}

func (p *PostgreSQLMemoryService) unmarshalTopics(data []byte) []string {
    var topics []string
    json.Unmarshal(data, &topics)
    return topics
}
```

For quick implementation, you can directly integrate with existing Memory platforms/services (such as mem0). Recommendations:

- Provide implementation in `memory/mem0/`, following the `memory.Service` interface.
- Reuse existing `memory/tool` tools (`memory_add`, `memory_search`, `memory_load`, etc.), expose through `Tools()`.
- Map topics and search according to target service capabilities, maintain lightweight local indexes when necessary to enhance query experience.
- Optional: Reuse `storage` module client management for unified authentication, connection and reuse.

Example skeleton (simplified):

```go
package mem0

import (
    "context"
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/memory"
    memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

type Service struct {
    client  *http.Client
    baseURL string
    apiKey  string
    tools   map[string]tool.Tool
}

func New(baseURL, apiKey string) *Service {
    s := &Service{
        client:  &http.Client{},
        baseURL: baseURL,
        apiKey:  apiKey,
        tools:   make(map[string]tool.Tool),
    }
    s.tools[memory.AddToolName] = memorytool.NewAddTool(s)
    s.tools[memory.SearchToolName] = memorytool.NewSearchTool(s)
    s.tools[memory.LoadToolName] = memorytool.NewLoadTool(s)
    return s
}

func (s *Service) Tools() []tool.Tool {
    var ts []tool.Tool
    for _, t := range s.tools {
        ts = append(ts, t)
    }
    return ts
}

func (s *Service) AddMemory(ctx context.Context, key memory.UserKey, m string, topics []string) error {
    if err := key.CheckUserKey(); err != nil {
        return err
    }
    // Call mem0 API to write memory.
    return nil
}

func (s *Service) SearchMemories(ctx context.Context, key memory.UserKey, q string) ([]*memory.Entry, error) {
    if err := key.CheckUserKey(); err != nil {
        return nil, err
    }
    // Call mem0 API to search, and convert to []*memory.Entry.
    return nil, nil
}

// Other interfaces Update/Delete/Clear/Read map according to mem0 capabilities
```

Implementation points:

- Configure authentication and rate limiting according to target service guidelines.
- Return values strictly align with `memory.Entry` and `memory.Memory`, use UTC for time fields.
- Tool declarations should accurately describe input and output for frontend and model understanding.
- Add README, examples and tests to ensure compatibility with runner-based integrations.

**Open Source Components That Can Be Integrated:**

- PostgreSQL memory storage
- MongoDB memory storage
- Elasticsearch memory storage
- Redis memory storage

**Contribution Method:**

- Create components under the corresponding directory (create a subdirectory with the corresponding component name)
- Contribute directly to `https://github.com/trpc-group/trpc-agent-go/memory/`

### 7. Observability Ecosystem

**Goal:** Provide unified observability capabilities based on OpenTelemetry standards, covering Logging, Metrics, Tracing, facilitating ecosystem expansion and replacement.

**Core Packages and Interfaces:**

- Logging: `trpc-agent-go/log` (`log.Logger` interface and `log.Default` global logger).
- Metrics: `trpc-agent-go/telemetry/metric` (global `metric.Meter` and `metric.Start` initialization).
- Tracing: `trpc-agent-go/telemetry/trace` (global `trace.Tracer` and `trace.Start` initialization).
- tRPC Integration: `trpc/log.go`, `trpc/telemetry/galileo/`.

#### Logging

- Interface Definition: `log.Logger` defines `Debug/Info/Warn/Error/Fatal` and their `*f` variant methods, facilitating replacement with any implementation.
- Default Implementation: `log.Default` uses `zap`'s `SugaredLogger` by default.
- Dynamic Level: `log.SetLevel(level)` supports `debug/info/warn/error/fatal`.
- tRPC Integration: `trpc/log.go` injects `tlog.DefaultLogger` as `log.Default` and refreshes with tRPC plugin lifecycle.

Example (using global logger):

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/log"
)

func main() {
    log.SetLevel(log.LevelInfo)
    log.Info("app start")
    log.Debugf("ctx: %v", context.Background())
}
```

Example (in tRPC through configuration to disk/remote):

```yaml
plugins:
  log:
    default:
      - writer: console
        level: info
      - writer: file
        level: warn
        writer_config:
          log_path: ./app.log
```

Contribution directions:

- Adapt any Logger (such as zerolog, logrus): implement `log.Logger` interface, and set `log.Default = yourLogger` during initialization.
- tRPC pluginization: reference `trpc/log.go`'s `plugin.RegisterSetupHook` usage.

#### Metrics

- Package: `telemetry/metric`.
- Global Object: `metric.Meter`, default `noop`, points to OTel Meter after calling `metric.Start`.
- Initialization: `metric.Start(ctx, metric.WithEndpoint("host:4317"))`.
- OTLP Export: uses `otlpmetricgrpc`, supports environment variable override:
  - `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`.
  - `OTEL_EXPORTER_OTLP_ENDPOINT` (fallback).
- Resource Identification: automatically fills `service.namespace/name/version`.

Example (start metrics and report Counter):

```go
package main

import (
    "context"

    "go.opentelemetry.io/otel/metric"

    "trpc.group/trpc-go/trpc-agent-go/telemetry/metric" // alias ametric
    ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
)

func main() {
    mp, err := ametric.NewMeterProvider(
		context.Background(),
		ametric.WithEndpoint("localhost:4318"),
		ametric.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to create meter provider: %v", err)
	}
	defer mp.Shutdown(context.Background())
	ametric.InitMeterProvider(mp)
	meter := mp.Meter("trpc_agent_go.app")
        counter, _ := meter.Int64Counter(
        "requests_total",
        metric.WithDescription("total requests"),
    )
    counter.Add(context.Background(), 1)
}
```

Contribution directions:

- Exporter ecosystem: encapsulate more OTel Exporter convenient startup methods (such as Prometheus pull/OTLP http).
- Metrics library: agree on common metric naming and label conventions, provide helper methods.

#### Tracing

- Package: `telemetry/trace`.
- Global Object: `trace.Tracer`, default `noop`, points to OTel Tracer after calling `trace.Start`.
- Initialization: `trace.Start(ctx, trace.WithEndpoint("host:4317"))`.
- OTLP Export: uses `otlptracegrpc`, supports environment variable override:
  - `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`.
  - `OTEL_EXPORTER_OTLP_ENDPOINT` (fallback).
- Propagator: enables `TraceContext` by default.

Example (start tracing and create Span):

```go
package main

import (
    "context"

    "go.opentelemetry.io/otel/trace"
    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func main() {
    clean, _ := atrace.Start(context.Background(),
        atrace.WithEndpoint("localhost:4317"),
    )
    defer clean()

    ctx, span := atrace.Tracer.Start(context.Background(), "example",
        trace.WithAttributes(),
    )
    _ = ctx
    span.End()
}
```

Contribution directions:

- Exporter ecosystem: encapsulate Zipkin, Jaeger (direct push) and other startup methods.
- Span conventions: agree on common Span names/attribute keys, provide helpers (can be placed in `telemetry/`).

### 8. API Service Ecosystem

**Goal:** Provide unified, extensible API service encapsulation for frontend Chat interfaces (such as ADK Web, AG-UI, Agent UI), covering session management, conversation sending, streaming transmission, tool calls, observability and authentication capabilities, and align with various UI protocols for plug-and-play.

**Existing Implementation Reference:**

- **A2A Server**: `server/a2a`.
  - Service encapsulation for A2A protocol, built-in `AuthProvider` and task orchestration, suitable for platform-to-Agent integration scenarios.

**Alignment with Frontend Protocols:**

- **AG-UI**: Reference `https://github.com/ag-ui-protocol/ag-ui`.
  - Required capabilities:
    - Session list/create/query.
    - Text conversation and SSE streaming increment; support tool calls and function response fragment display.
    - State/usage metadata, error expression alignment.
    - Rich media support (InlineData) for files/images and server-side storage integration.
    - Authentication (API Key, JWT, Cookie session) and CORS.
  - Recommend providing implementation in `server/agui/`, reuse common model and event mapping tools, complete protocol layer adaptation in Handler.
- **Agent UI (agno)**: Reference `https://docs.agno.com/agent-ui/introduction`.
  - Focus: SSE/WebSocket streaming, Tool call streaming UI feedback, session/artifact persistence.

**Key Design Points:**

- **Schema Mapping**:
  - Input: Map UI's `Content`/`Part` to internal `model.Message`.
  - Output events: Map internal `event.Event` to UI-expected envelope/parts, structure tool calls and tool responses to avoid duplicate text display.
- **Streaming Transmission**:
  - Prefer SSE for streaming; WebSocket can be an ecosystem extension.
  - Non-streaming endpoints need to aggregate final messages and tool responses according to UI expectations.
- **Session Storage**:
  - Inject specific implementation through `runner.WithSessionService`, reuse `session` module.
- **Observability**:
  - Reuse `telemetry/trace` and `telemetry/metric`, export key spans and event attributes for UI-side debugging and positioning.
- **Authentication and Security**:
  - Support API Key/JWT/custom Header; add rate limiting and cross-domain control for sensitive endpoints.
- **Open Specifications**:
  - Recommend attaching `openapi.json`/`README.md` to each `server/*` submodule for frontend/integration party integration.

**AG-UI Adaptation Suggestion (skeleton):**

```go
package agui

import (
    "net/http"

    "github.com/gorilla/mux"
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

type Server struct {
    router *mux.Router
    ag     agent.Agent
    run    runner.Runner
}

func New(ag agent.Agent, opts ...runner.Option) *Server {
    r := runner.NewRunner(ag.Info().Name, ag, opts...)
    s := &Server{router: mux.NewRouter(), ag: ag, run: r}
    s.routes()
    return s
}

func (s *Server) Handler() http.Handler { return s.router }

func (s *Server) routes() {
    // GET /sessions, POST /sessions, GET /sessions/{id}
    // POST /chat (non-stream), POST /chat/stream (SSE)
}
```

**Ecosystem Directions and Contribution Standards:**

- **Target UI/Protocols**:
  - **AG-UI**: Provide HTTP + SSE adaptation in `server/agui/`, with examples and `openapi.json`.
- **Agent UI (agno)**: Provide HTTP + SSE / WebSocket adaptation in `server/agentui/`.
  - **WebSocket/Bidi Streaming**: Align with ADK `run_live`, provide real-time audio/video channels (depends on model-side support).
- **Implementation Requirements**:
  - Clear event Schema, complete mapping, ensure tool calls/responses have good experience in UI.
  - Support pluggable session storage, default In-Memory, recommend supporting Redis/MySQL etc.
  - Built-in CORS, authentication middleware (API Key/JWT), expose health check endpoints.
  - Observability: integrate `telemetry`, provide minimal Trace and Metric examples.
  - Documentation: README, OpenAPI, end-to-end examples (include simple frontend or curl scripts).

Link references:

- `server/a2a` (A2A protocol encapsulation). 

### 9. Planner Ecosystem

**Goal:** Provide diverse planners to adapt to different models and workflows, including built-in thinking capability adaptation and explicit planning (ReAct/Reflection, etc.).

**Interface Definition:** `planner.Planner`.

- `BuildPlanningInstruction(ctx, invocation, llmRequest) string`: Build or inject system prompts and request configurations for planning.
- `ProcessPlanningResponse(ctx, invocation, response) *model.Response`: Post-process model responses for planning (optional).

**Existing Implementation Reference:**

- `planner/builtin`: Adapt O series, Claude, Gemini and other models with reasoning parameters, through configuring `ReasoningEffort`, `ThinkingEnabled`, `ThinkingTokens`.
- `planner/react`: Provide explicit planning instructions and response post-processing, agree on `/*PLANNING*/`, `/*ACTION*/`, `/*REASONING*/`, `/*FINAL_ANSWER*/` and other tags.

**Ecosystem Directions:**

- Reflection Planner: Self-reflective correction and multi-round re-planning.
- LangGraph style Planner: Align with Pregel parallel and checkpoint mechanisms.
- Tool-first Planner: Selection and constraints for Tool-First processes.

**Integration Example (skeleton):**

```go
package myplanner

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/planner"
)

type Planner struct{}

var _ planner.Planner = (*Planner)(nil)

func New() *Planner { return &Planner{} }

func (p *Planner) BuildPlanningInstruction(ctx context.Context, inv *agent.Invocation, req *model.Request) string {
    // Can inject custom parameters into req, and return system prompt string.
    return "You must plan before action."
}

func (p *Planner) ProcessPlanningResponse(ctx context.Context, inv *agent.Invocation, 
    rsp *model.Response) *model.Response {
    if rsp == nil {
        return nil
    }
    // Can do structured segmentation or tool call correction on rsp.
    return nil
}
```

**Combination and Usage:** Inject Planner when creating `agent/llmagent`, or select different Planner strategies at `runner` layer as needed, combine with `Tool` and `Session` management for end-to-end implementation.

**Contribution Suggestions:**

- Provide implementation and README, test cases, examples in `planner/<name>/`.
- Combine with `docs/overall-introduction.md`'s Observability to provide end-to-end examples for frontend UI demonstration.
- Follow goimports and error message style, add periods at end of comments, code line breaks around 80 columns.

## Component Relationship Description

### Relationship between Storage, Session, and Memory

These three components have different responsibilities and relationships in the architecture:

**1. Storage (Storage Layer)**

- **Responsibility:** Provide unified storage client management, provide infrastructure support for Session and Memory
- **Function:** Register, manage and obtain clients for various storage backends (Redis, PostgreSQL, MongoDB, etc.)
- **Characteristics:** As an infrastructure component, can be shared and used by Session and Memory components

**2. Session (Session Layer)**

- **Responsibility:** Manage user session state and events
- **Function:** Create, get, delete sessions, manage session state, record session events
- **Dependency:** Can reuse Storage module clients
- **Data Characteristics:** Temporary data, can be cleaned up after session ends

**3. Memory (Memory Layer)**

- **Responsibility:** Manage user long-term memory and personalized information
- **Function:** Add, search, update, delete user memories, provide memory tools
- **Dependency:** Can reuse Storage module clients
- **Data Characteristics:** Persistent data, maintained across sessions

**Relationship Diagram:**

```
┌─────────────────┐
│   Application   │
└─────────┬───────┘
          │
┌─────────▼───────┐    ┌─────────────────┐
│   Session       │    │   Memory        │
│   Service       │    │   Service       │
└─────────┬───────┘    └─────────┬───────┘
          │                      │
          └──────────┬───────────┘
                     │
          ┌─────────▼───────┐
          │   Storage       │
          │   Client        │
          │   Management    │
          └─────────┬───────┘
                     │
          ┌─────────▼───────┐
          │   Storage       │
          │   Backends      │
          │   (Redis, DB)   │
          └─────────────────┘
```

**Usage Example:**

```go
// 1. Register storage client.
storage.RegisterRedisInstance("default", storage.WithClientBuilderURL("redis://localhost:6379"))

// 2. Session service uses storage client.
sessionService, err := session.NewRedisService(
    session.WithRedisInstance("default"),
)

// 3. Memory service uses storage client.
memoryService, err := memory.NewRedisService(
    memory.WithRedisInstance("default"),
)

// 4. Use in application.
session, err := sessionService.CreateSession(ctx, sessionKey, state)
memory, err := memoryService.AddMemory(ctx, userKey, "User likes coffee", []string{"preferences"})
```

## Contribution Guidance

## Contribution Guidance

**Suitable Components:**

- Various third-party service and tool integrations
- Open source component adapters
- Standard protocol support
- Framework functionality extensions

**Contribution Process:**

1. Fork `https://github.com/trpc-group/trpc-agent-go`
2. Create components under the corresponding module root directory (such as `model/somemodel`, `tool/sometool`, `agent/someagent`)
3. Implement corresponding interfaces
4. Write tests and documentation
5. Submit Pull Request

**Directory Structure Example:**

```
model/gemini/
├── model.go
├── config.go
├── examples/
├── README.md
└── gemini_test.go
```

## Summary

Ecosystem development is an important direction for tRPC-Agent-Go development. By implementing standard interfaces, various third-party services and tools can be easily integrated to expand the framework's capabilities.

**Key Contribution Points:**

- Reference existing implementations to understand interface usage
- Choose appropriate contribution paths based on component types
- Follow unified interface specifications and code standards
- Provide complete test cases and documentation

**Determining Contribution Location:**

- All components are contributed directly to the corresponding module directory on GitHub

**Storage, Session, Memory Component Characteristics:**

- **Storage:** Provides unified client management, can be shared by Session and Memory
- **Session:** Manages temporary session data, can reuse Storage clients
- **Memory:** Manages persistent memory data, can reuse Storage clients
- The three components are decoupled through interfaces, supporting independent implementation and combined usage
