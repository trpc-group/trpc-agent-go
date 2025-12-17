# tRPC-Agent-Go 生态建设指南

本文档分析 tRPC-Agent-Go 框架中需要生态建设的模块，说明需要实现的接口，并提供贡献指导。

注意：所有共建组件都直接贡献到 GitHub 开源仓库的对应目录下，比如 `model/somemodel`、`tool/sometool`、`agent/someagent` 等。

贡献时在对应的贡献模块目录下新建合理命名的子文件夹，然后实现对应模块接口，并提供丰富的测试用例以及 example 示例。

## 生态建设模块分析

### 1. Agent 生态化

**目标：** 封装和适配第三方 Agent 框架

**接口定义：** [agent.Agent](https://github.com/trpc-group/trpc-agent-go/blob/main/agent/agent.go)

**现有实现参考：** [LLMAgent](https://github.com/trpc-group/trpc-agent-go/tree/main/agent/llmagent)

**实现注意事项：**

- `Run` 方法必须返回事件通道，支持流式响应
- `Tools` 方法返回 Agent 可用的工具列表
- `Info` 方法提供 Agent 的基本信息
- `SubAgents` 和 `FindSubAgent` 支持 Agent 组合模式
- 参考 LLMAgent 实现，了解事件处理和错误处理机制

**实现示例：**

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

**可以集成开源组件示例：**

- LangChain 适配器
- LangGraph 适配器

**贡献方式：**

- 在对应目录下创建组件（新建一个对应组件名称的子目录）
- 直接贡献到 `https://github.com/trpc-group/trpc-agent-go/agent/`

### 2. 模型（Model）生态化

**目标：** 支持更多模型提供商

**接口定义：** [model.Model](https://github.com/trpc-group/trpc-agent-go/blob/main/model/model.go)

**现有实现参考：** [OpenAI Model](https://github.com/trpc-group/trpc-agent-go/tree/main/model/openai)

**实现注意事项：**

- `GenerateContent` 方法必须支持流式响应，返回事件通道
- 区分系统级错误（返回 error）和 API 级错误（Response.Error）
- 实现 `Info` 方法提供模型基本信息
- 参考 OpenAI 实现，了解请求构建和响应处理
- 支持上下文取消和超时控制

**实现示例：**

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
        
        // 调用 Gemini API
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

**可以集成的开源组件示例：**

- Google Gemini 模型支持
- Anthropic Claude 模型支持
- Ollama 本地模型支持

**贡献方式：**

- 在对应目录下创建组件（新建一个对应组件名称的子目录）
- 直接贡献到 `https://github.com/trpc-group/trpc-agent-go/model/`

### 3. 工具（Tool）生态化

**目标：** 集成更多第三方工具

**接口定义：** 

- [tool.Tool](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/tool.go) - 单个工具接口
- [tool.ToolSet](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/toolset.go) - 工具集合接口

**现有实现参考：** [DuckDuckGo Tool](https://github.com/trpc-group/trpc-agent-go/tree/main/tool/duckduckgo)

**实现注意事项：**

**单个工具实现：**

- `Declaration` 方法必须返回完整的工具元数据
- `Call` 方法接收 JSON 格式的参数，返回任意类型结果
- 使用 JSON Schema 定义输入输出格式
- 参考 DuckDuckGo 实现，了解工具调用和错误处理
- 支持上下文取消和超时控制

**工具集合实现：**

- `Tools` 方法根据上下文返回可用的工具列表
- `Close` 方法释放工具集合持有的资源
- 支持动态工具加载和配置
- 实现工具的生命周期管理

**实现示例：**

**单个工具实现：**

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

**工具集合实现：**

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
    
    // 清理资源
    a.tools = make(map[string]tool.Tool)
    return nil
}

// Name 返回该工具集的名称
func (a *APIToolSet) Name() string { return "api-tools" }
```

**可以集成的开源组件示例：**

- 搜索引擎工具（Google、Bing）
- 天气查询工具
- 计算器工具
- 文件操作工具
- API 工具集合（REST API 工具包）
- 数据库操作工具集合
- 文件处理工具集合

**贡献方式：**

- 在对应目录下创建组件（新建一个对应组件名称的子目录）
- 直接贡献到 `https://github.com/trpc-group/trpc-agent-go/tool/`

### 4. 知识库（Knowledge）生态化

**目标：** 集成成熟的 RAG 组件

**接口定义：** [knowledge.Knowledge](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/knowledge.go)

**现有实现参考：** 

* [InMemory Knowledge](https://github.com/trpc-group/trpc-agent-go/tree/main/knowledge/inmemory)

**实现注意事项：**

- `Search` 方法支持上下文和历史记录
- 返回相关文档和相关性评分
- 支持搜索参数和结果限制
- 参考 InMemory 实现，了解搜索逻辑和结果处理
- 支持向量化搜索和语义匹配

**实现示例：**

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
    
    // 构建搜索查询
    query := w.buildQuery(req)
    
    // 执行向量搜索
    results, err := w.client.Search(ctx, query)
    if err != nil {
        return nil, err
    }
    
    if len(results) == 0 {
        return nil, fmt.Errorf("no results found")
    }
    
    // 返回最佳匹配结果
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
    // 构建 Weaviate 查询逻辑
    return &weaviate.Query{
        Query: req.Query,
        Limit: req.MaxResults,
        Filter: w.buildFilter(req),
    }
}
```

**可以集成的开源组件示例：**

- Weaviate 向量数据库
- Pinecone 向量数据库
- Qdrant 向量数据库

**贡献方式：**

- 在对应目录下创建组件（新建一个对应组件名称的子目录）
- 直接贡献到 `https://github.com/trpc-group/trpc-agent-go/knowledge/`

### 5. Session 生态化

**目标：** 支持多种会话存储后端，管理用户会话状态和事件

**接口定义：** [session.Service](https://github.com/trpc-group/trpc-agent-go/blob/main/session/session.go)

**现有实现参考：**

- [InMemory Session](https://github.com/trpc-group/trpc-agent-go/tree/main/session/inmemory)
- [Redis Session](https://github.com/trpc-group/trpc-agent-go/tree/main/session/redis)

**实现注意事项：**

- 实现完整的 Session 生命周期管理（创建、获取、删除、列表）
- 支持状态存储和事件记录
- 实现连接池和错误处理
- 支持事务和一致性
- 可以复用 storage 模块的客户端
- 参考 InMemory 和 Redis 实现，了解 Session 管理逻辑

**实现示例：**

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

func (p *PostgreSQLService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
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
    
    // 插入到数据库
    _, err := p.db.ExecContext(ctx, `
        INSERT INTO sessions (id, app_name, user_id, state, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6)
    `, session.ID, session.AppName, session.UserID, p.marshalState(state), session.CreatedAt, session.UpdatedAt)
    
    if err != nil {
        return nil, err
    }
    
    return session, nil
}

func (p *PostgreSQLService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
    if err := key.CheckSessionKey(); err != nil {
        return nil, err
    }
    
    var session session.Session
    var stateData []byte
    
    err := p.db.QueryRowContext(ctx, `
        SELECT id, app_name, user_id, state, created_at, updated_at
        FROM sessions WHERE id = $1
    `, key.SessionID).Scan(&session.ID, &session.AppName, &session.UserID, &stateData, &session.CreatedAt, &session.UpdatedAt)
    
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

**可以集成的开源组件示例：**

- PostgreSQL 会话存储
- MongoDB 会话存储
- MySQL 会话存储
- Cassandra 会话存储

**贡献方式：**

- 在对应目录下创建组件（新建一个对应组件名称的子目录）
- 直接贡献到 `https://github.com/trpc-group/trpc-agent-go/session/`

### 6. Memory 生态化

**目标：** 支持多种记忆存储后端，管理用户长期记忆和个性化信息

**接口定义：** [memory.Service](https://github.com/trpc-group/trpc-agent-go/blob/main/memory/memory.go)

**现有实现参考：** [InMemory Memory](https://github.com/trpc-group/trpc-agent-go/tree/main/memory/inmemory)

**实现注意事项：**

- 实现完整的 Memory 生命周期管理（添加、更新、删除、搜索、读取）
- 支持记忆主题分类和搜索
- 提供记忆工具集成（memory_add, memory_search 等）
- 实现连接池和错误处理
- 可以复用 storage 模块的客户端
- 支持记忆限制和清理机制
- 参考 InMemory 实现，了解记忆管理逻辑

**实现示例：**

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
    
    // 初始化工具
    service.initTools()
    
    return service, nil
}

func (p *PostgreSQLMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
    if err := userKey.CheckUserKey(); err != nil {
        return err
    }
    
    now := time.Now()
    memoryID := p.generateMemoryID(memoryStr)
    
    // 插入记忆到数据库
    _, err := p.db.ExecContext(ctx, `
        INSERT INTO memories (id, app_name, user_id, memory, topics, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
    `, memoryID, userKey.AppName, userKey.UserID, memoryStr, p.marshalTopics(topics), now, now)
    
    return err
}

func (p *PostgreSQLMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
    if err := userKey.CheckUserKey(); err != nil {
        return nil, err
    }
    
    // 执行全文搜索
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
        
        err := rows.Scan(&entry.ID, &entry.AppName, &entry.UserID, &entry.Memory.Memory, &topicsData, &entry.CreatedAt, &entry.UpdatedAt)
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
    // 生成唯一记忆 ID
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

为便于快速落地，可直接对接现有 Memory 平台/服务（如 mem0）。建议：

- 在 `memory/mem0/` 提供实现，遵循 `memory.Service` 接口。
- 复用现有 `memory/tool` 工具（`memory_add`、`memory_search`、
  `memory_load` 等），通过 `Tools()` 暴露。
- 主题（topics）与检索（search）按目标服务能力做映射，必要时在本地
  维护轻量索引以增强查询体验。
- 可选：复用 `storage` 模块的客户端管理统一鉴权、连接与复用。

示例骨架（简化）：

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
    // 调用 mem0 API 写入记忆
    return nil
}

func (s *Service) SearchMemories(ctx context.Context, key memory.UserKey, q string) ([]*memory.Entry, error) {
    if err := key.CheckUserKey(); err != nil {
        return nil, err
    }
    // 调用 mem0 API 检索, 并转换为 []*memory.Entry
    return nil, nil
}

// 其余接口 Update/Delete/Clear/Read 按 mem0 能力做映射实现
```

实现要点：

- 鉴权与限流按目标服务指南配置。
- 返回值严格对齐 `memory.Entry` 与 `memory.Memory`，时间字段使用 UTC。
- 工具声明（Declaration）应准确描述输入输出，便于前端与模型理解。
- 补充 README、示例与测试，确保与 runner 相关集成可用。

**可以集成的开源组件示例：**

- PostgreSQL 记忆存储
- MongoDB 记忆存储
- Elasticsearch 记忆存储
- Redis 记忆存储

**贡献方式：**

- 在对应目录下创建组件（新建一个对应组件名称的子目录）
- 直接贡献到 `https://github.com/trpc-group/trpc-agent-go/memory/`

### 7. 可观测（Observability）生态化

**目标：** 基于 OpenTelemetry 标准提供统一的可观测能力，覆盖 Logging、Metrics、Tracing，便于生态扩展与替换。

**核心包与接口：**

- Logging: `trpc-agent-go/log`（`log.Logger` 接口与 `log.Default` 全局
  日志器）。
- Metrics: `trpc-agent-go/telemetry/metric`（`metric.Meter` 全局 Meter 与
  `metric.Start` 初始化）。
- Tracing: `trpc-agent-go/telemetry/trace`（`trace.Tracer` 全局 Tracer 与
  `trace.Start` 初始化）。
- tRPC 集成：`trpc/log.go`、`trpc/telemetry/galileo/`。

#### Logging（日志）

- 接口定义：`log.Logger` 定义了 `Debug/Info/Warn/Error/Fatal` 及其 `*f`
  变体方法，便于替换为任意实现。
- 默认实现：`log.Default` 默认使用 `zap` 的 `SugaredLogger`。
- 动态级别：`log.SetLevel(level)` 支持 `debug/info/warn/error/fatal`。
- tRPC 集成：`trpc/log.go` 将 `tlog.DefaultLogger` 注入为
  `log.Default`，并随 tRPC 插件生命周期刷新。

示例（使用全局日志器）：

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

示例（在 tRPC 中通过配置落盘/远端）：

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

贡献方向：

- 适配任意 Logger（如 zerolog、logrus）：实现 `log.Logger` 接口，并在
  初始化时设置 `log.Default = yourLogger`。
- tRPC 插件化：参考 `trpc/log.go` 的 `plugin.RegisterSetupHook` 用法。

#### Metrics（指标）

- 包：`telemetry/metric`。
- 全局对象：`metric.Meter`，默认 `noop`，调用 `metric.Start` 后指向
  OTel Meter。
- 初始化：`metric.Start(ctx, metric.WithEndpoint("host:4317"))`。
- OTLP 导出：使用 `otlpmetricgrpc`，支持环境变量覆盖：
  - `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`。
  - `OTEL_EXPORTER_OTLP_ENDPOINT`（兜底）。
- 资源标识：自动填充 `service.namespace/name/version`。

示例（启动指标与上报 Counter）：

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

贡献方向：

- 导出器生态：封装更多 OTel Exporter 的便捷启动方法（如 Prometheus pull/OTLP http）。
- 指标库：约定常用指标命名与标签规范，提供 helper 方法。

#### Tracing（链路追踪）

- 包：`telemetry/trace`。
- 全局对象：`trace.Tracer`，默认 `noop`，调用 `trace.Start` 后指向
  OTel Tracer。
- 初始化：`trace.Start(ctx, trace.WithEndpoint("host:4317"))`。
- OTLP 导出：使用 `otlptracegrpc`，支持环境变量覆盖：
  - `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`。
  - `OTEL_EXPORTER_OTLP_ENDPOINT`（兜底）。
- Propagator：默认启用 `TraceContext`。

示例（启动追踪与创建 Span）：

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

贡献方向：

- 导出器生态：封装 Zipkin、Jaeger（直接推送）等启动方法。
- Span 规范：约定常见 Span 名称/属性键，提供 helper（可放在
  `telemetry/`）。

### 8. API 服务生态化

**目标：** 面向前端 Chat 界面（如 ADK Web、AG-UI、Agent UI）提供统一、
可扩展的 API 服务封装，覆盖会话管理、对话发送、流式传输、工具调用、
可观测与鉴权等能力，并对齐各 UI 协议以便即插即用。

**现有实现参考：**

- **A2A Server**：`server/a2a`。
  - 面向 A2A 协议的服务封装，内建 `AuthProvider` 与任务编排，适合
    平台到 Agent 的集成场景。

**与前端协议对齐：**

- **AG-UI**：参考 `https://github.com/ag-ui-protocol/ag-ui`。
  - 需要的能力：
    - 会话列表/创建/查询。
    - 文本对话与 SSE 流式增量；支持工具调用与函数响应片段化展示。
    - 状态/用量元数据、错误表达对齐。
    - 文件/图片等富媒体承载（InlineData）与服务端存储对接。
    - 鉴权（API Key、JWT、Cookie 会话）与 CORS。
  - 建议在 `server/agui/` 提供实现，复用通用的模型与事件
    映射工具，在 Handler 中完成协议层适配。
- **Agent UI（agno）**：参考 `https://docs.agno.com/agent-ui/introduction`。
  - 重点：SSE/WebSocket 流、Tool 调用流式 UI 反馈、会话/工件持久化。

**关键设计要点：**

- **Schema 映射**：
  - 输入：将 UI 的 `Content`/`Part` 映射为内部 `model.Message`。
  - 输出事件：将内部 `event.Event` 映射为 UI 期望的 envelope/parts，
    对工具调用与工具响应进行结构化，避免重复文本展示。
- **流式传输**：
  - 优先使用 SSE；WebSocket 可作为生态扩展。
  - 非流式端点需按 UI 期望聚合最终消息与工具响应。
- **会话存储**：
  - 通过 `runner.WithSessionService` 注入具体实现，复用 `session` 模块。
- **可观测**：
  - 复用 `telemetry/trace` 与 `telemetry/metric`，导出关键 Span 与事件属性，便于 UI 侧调试与定位。
- **鉴权与安全**：
  - 支持 API Key/JWT/自定义 Header；对敏感端点加速率限制与跨域控制。
- **开放规范**：
  - 建议在各 `server/*` 子模块附带 `openapi.json`/`README.md`，
    便于前端/集成方对接。

**AG-UI 适配建议（骨架）：**

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

**生态化方向与贡献规范：**

- **目标 UI/协议**：
  - **AG-UI**：在 `server/agui/` 提供 HTTP + SSE 适配，附带示例
与 `openapi.json`。
- **Agent UI（agno）**：在 `server/agentui/` 提供 HTTP + SSE /
WebSocket 适配。
  - **WebSocket/Bidi Streaming**：对标 ADK `run_live`，提供实时音视频
    通道（依赖模型侧支持）。
- **落地要求：**
  - 事件 Schema 明确、映射完备，确保工具调用/响应在 UI 端有良好体验。
  - 支持会话存储可插拔，默认 In-Memory，推荐支持 Redis/MySQL 等。
  - 内置 CORS、鉴权中间件（API Key/JWT），暴露健康检查端点。
  - 可观测：打通 `telemetry`，提供最小 Trace 与 Metric 样例。
  - 文档：README、OpenAPI、端到端示例（包含简单前端或 curl 脚本）。

链接参考：

- `server/a2a`（A2A 协议封装）。 

### 9. Planner 生态化

**目标：** 提供多样化的规划器以适配不同模型与工作流，包括内置思维
能力适配与显式规划（ReAct/Reflection 等）。

**接口定义：** `planner.Planner`。

- `BuildPlanningInstruction(ctx, invocation, llmRequest) string`：构建或
  注入用于规划的系统提示与请求配置。
- `ProcessPlanningResponse(ctx, invocation, response) *model.Response`：
  对模型响应做规划后处理（可选）。

**现有实现参考：**

- `planner/builtin`：适配 O 系列、Claude、Gemini 等具备推理参数的模型,
  通过配置 `ReasoningEffort`、`ThinkingEnabled`、`ThinkingTokens`。
- `planner/react`：提供显式规划指令与响应后处理, 约定 `/*PLANNING*/`、
  `/*ACTION*/`、`/*REASONING*/`、`/*FINAL_ANSWER*/` 等标签。

**生态化方向：**

- Reflection Planner：自反式修正与多轮再规划。
- LangGraph 风格 Planner：对齐 Pregel 并行与检查点机制。
- 工具优先 Planner：面向 Tool-First 流程的选择与约束。

**接入示例（骨架）：**

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
    // 可向 req 注入自定义参数, 并返回系统提示串
    return "You must plan before action."
}

func (p *Planner) ProcessPlanningResponse(ctx context.Context, inv *agent.Invocation, rsp *model.Response) *model.Response {
    if rsp == nil {
        return nil
    }
    // 可对 rsp 做结构化切分或工具调用修正
    return nil
}
```

**组合与使用：** 在 `agent/llmagent` 创建时注入 Planner，或在 `runner`
层按需选择不同 Planner 策略，结合 `Tool` 与 `Session` 管理实现端到端。

**贡献建议：**

- 在 `planner/<name>/` 提供实现与 README、测试用例、示例。
- 结合 `docs/overall-introduction.md` 的 Observability 提供端到端示例，
  便于前端 UI 演示。
- 遵循 goimports 与错误消息风格，注释句末加句号，代码换行约 80 列。

## 组件关系说明

### Storage、Session、Memory 三者的关系

这三个组件在架构中具有不同的职责和关系：

**1. Storage（存储层）**

- **职责：** 提供统一的存储客户端管理，为 Session 和 Memory 提供基础设施支持
- **功能：** 注册、管理和获取各种存储后端的客户端（Redis、PostgreSQL、MongoDB 等）
- **特点：** 作为基础设施组件，可以被 Session 和 Memory 组件共享使用

**2. Session（会话层）**

- **职责：** 管理用户会话状态和事件
- **功能：** 创建、获取、删除会话，管理会话状态，记录会话事件
- **依赖：** 可以复用 Storage 模块的客户端
- **数据特点：** 临时性数据，会话结束后可以清理

**3. Memory（记忆层）**

- **职责：** 管理用户长期记忆和个性化信息
- **功能：** 添加、搜索、更新、删除用户记忆，提供记忆工具
- **依赖：** 可以复用 Storage 模块的客户端
- **数据特点：** 持久性数据，跨会话保持

**关系图：**

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

**使用示例：**

```go
// 1. 注册存储客户端
storage.RegisterRedisInstance("default", storage.WithClientBuilderURL("redis://localhost:6379"))

// 2. Session 服务使用存储客户端
sessionService, err := session.NewRedisService(
    session.WithRedisInstance("default"),
)

// 3. Memory 服务使用存储客户端
memoryService, err := memory.NewRedisService(
    memory.WithRedisInstance("default"),
)

// 4. 应用中使用
session, err := sessionService.CreateSession(ctx, sessionKey, state)
memory, err := memoryService.AddMemory(ctx, userKey, "用户喜欢咖啡", []string{"preferences"})
```

## 贡献指导

## 贡献指导

**适合的组件：**

- 各种第三方服务和工具的集成
- 开源组件适配器
- 标准协议支持
- 框架功能扩展

**贡献流程：**

1. Fork `https://github.com/trpc-group/trpc-agent-go`
2. 在对应模块的根目录下创建组件（如 `model/somemodel`、`tool/sometool`、`agent/someagent`）
3. 实现相应的接口
4. 编写测试和文档
5. 提交 Pull Request

**目录结构示例：**

```
model/gemini/
├── model.go
├── config.go
├── examples/
├── README.md
└── gemini_test.go
```

## 总结

生态建设是 tRPC-Agent-Go 发展的重要方向。通过实现标准接口，可以轻松集成各种第三方服务和工具，扩展框架的能力。

**贡献要点：**

- 参考现有实现，了解接口使用方式
- 根据组件类型选择合适的贡献路径
- 遵循统一的接口规范和代码标准
- 提供完整的测试用例和文档

**判断贡献位置：**

- **核心通用组件**：如果是比较核心都能用得到的组件，直接贡献到 GitHub 对应模块目录
- **生态组件（开源依赖）**：如果依赖公开开源组件，贡献到 GitHub 的 ecosystem 目录

**Storage、Session、Memory 组件特点：**

- **Storage：** 提供统一的客户端管理，可以被 Session 和 Memory 共享
- **Session：** 管理临时会话数据，可以复用 Storage 客户端
- **Memory：** 管理持久记忆数据，可以复用 Storage 客户端
- 三个组件通过接口解耦，支持独立实现和组合使用
