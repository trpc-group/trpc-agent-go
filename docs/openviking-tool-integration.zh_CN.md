# trpc-agent-go 从 Tool 层接入 OpenViking 方案

> 聚焦「P0：Tool 层接入」。目标是让任意 LLMAgent 通过一组 `viking_*` 工具，
> 直接读写 OpenViking 的上下文文件系统。对标 OpenViking 官方 `create_openviking_tools()`。

## 一、为什么从 Tool 层切入

- **改动面最小**：不触碰 `knowledge`/`memory` 抽象，只新增一个工具包，单个 PR 即可落地。
- **最贴合 OpenViking 原生用法**：官方的 MCP / plugin 本质就是把 `viking_*` 暴露给模型自主调用。
- **天然适配分层检索**：模型可以自己先 `viking_search`（拿 URI + 摘要），再决定对哪些 URI `viking_read`（拉全文）——这正是 OpenViking「省 token」的两段式用法，由模型驱动，无需我们在代码里硬编码策略。
- **可组合**：工具可与 agent 已有的其它工具混用，互不影响。

## 二、整体设计

```
internal/openviking/         # 共享 HTTP 客户端（唯一实体，后续 knowledge/memory 也复用）
  client.go                  #   Client：封装 /api/v1/* 端点 + 认证 + 重试
  types.go                   #   请求/响应 struct（对齐 server pydantic 模型）

tool/openviking/             # 本方案交付物
  toolset.go                 #   ToolSet 实现：按 profile 暴露 viking_* 工具，持有共享 client
  tools.go                   #   各 viking_* 工具的 input struct + Call 实现
  options.go                 #   Options（URL/APIKey/Profile/Timeout/账户身份…）
  toolset_test.go            #   基于 httptest 的 mock server 单测
```

实现两个 trpc-agent-go 接口：

- 每个工具用 `function.NewFunctionTool[I, O]` 构造，自动满足 `tool.CallableTool`。
- 用 `tool.ToolSet`（`Tools(ctx) []Tool` / `Close()` / `Name()`）把整组工具打包，`Close()` 负责释放 HTTP client。

## 三、共享 HTTP 客户端 `internal/openviking`

OpenViking server 的端点（前缀 `/api/v1`，来自 `server/routers/`）：

| Client 方法 | HTTP | 说明 |
|---|---|---|
| `Find` | `POST /search/find` | 无会话语义召回，返回 URI+摘要+score |
| `Search` | `POST /search/search` | 带 session 的层级检索 |
| `Grep` | `POST /search/grep` | 内容正则匹配 |
| `Glob` | `POST /search/glob` | 路径通配 |
| `Ls` | `GET /fs/...`（ls） | 列目录 |
| `Read` | `GET /fs/read` | L2 全文 |
| `Abstract` / `Overview` | `GET /fs/abstract` `/fs/overview` | L0 / L1 |
| `Write` | `POST /fs/write` | 写入 |
| `AddResource` | `POST /resources` | 导入资源 |
| `AddSkill` | `POST /skills` | 注册技能 |
| `CreateSession` / `AddMessage` / `CommitSession` | `POST /sessions` `/sessions/{id}/messages` `/sessions/{id}/commit` | 会话写入与提交 |
| `Status` | `GET /system/status` | 健康检查 |

关键实现点（照搬官方 `client.py` 策略）：

- **认证**：统一注入请求头 `X-API-Key`、`X-OpenViking-Account`、`X-OpenViking-User`（见 `server/auth.py`）。
- **重试**：仅对**只读方法**（find/search/read/ls/grep/glob/abstract/overview/status）在 `DEADLINE_EXCEEDED`/`UNAVAILABLE`/连接类错误时重试一次。
- **响应结构**：统一 `{ "status": "ok", "result": {...} }`；检索类 `result` 为 `{memories[], resources[], skills[]}`，每条 item 含 `uri/score/abstract/level/context_type/category`。

请求/响应 struct（核心部分）：

```go
// internal/openviking/types.go (示意)
type FindRequest struct {
    Query          string   `json:"query"`
    TargetURI      string   `json:"target_uri,omitempty"`
    Limit          int      `json:"limit,omitempty"`
    ScoreThreshold *float64 `json:"score_threshold,omitempty"`
}

type SearchRequest struct {
    FindRequest
    SessionID string `json:"session_id,omitempty"`
}

type MatchedItem struct {
    URI         string  `json:"uri"`
    Score       float64 `json:"score"`
    Abstract    string  `json:"abstract"`
    Level       int     `json:"level"`
    ContextType string  `json:"context_type"`
    Category    string  `json:"category"`
}

type RetrievalResult struct {
    Memories  []MatchedItem `json:"memories"`
    Resources []MatchedItem `json:"resources"`
    Skills    []MatchedItem `json:"skills"`
}
```

## 四、ToolSet 与 Options

```go
// tool/openviking/options.go (示意)
type Options struct {
    URL     string        // 默认 http://localhost:1933
    APIKey  string
    Account string
    User    string
    Profile Profile       // retrieval | agent(默认) | admin
    Timeout time.Duration // 默认 60s
    // ToolNames 显式指定工具子集；非空时覆盖 Profile。
    ToolNames []string
    // AllowForget 是否额外暴露 viking_forget（危险，默认 false）。
    AllowForget bool
}

type Profile string
const (
    ProfileRetrieval Profile = "retrieval" // 只读检索类
    ProfileAgent     Profile = "agent"     // 检索 + 写入(store/add_*) 默认
    ProfileAdmin     Profile = "admin"     // agent + forget
)
```

```go
// tool/openviking/toolset.go (示意)
type ToolSet struct {
    client *openviking.Client
    tools  []tool.Tool
    name   string
}

func New(opts Options) (*ToolSet, error) { /* 创建 client + 按 profile 选工具 */ }

func (s *ToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s *ToolSet) Close() error                        { return s.client.Close() }
func (s *ToolSet) Name() string                        { return s.name } // "openviking"
```

## 五、工具清单与映射（与官方对齐）

| 工具名 | Profile | 输入字段 | 调用的 Client 方法 | 输出 |
|---|---|---|---|---|
| `viking_find` | 全部 | `query, target_uri?, limit?, min_score?` | `Find` | 命中列表（URI+摘要+score，按 type 分组） |
| `viking_search` | 全部 | `query, target_uri?, session_id?, limit?, min_score?` | `Search` | 同上（带会话上下文） |
| `viking_browse` | 全部 | `uri?, recursive?, pattern?` | `Ls`/`Glob` | 目录列表 |
| `viking_read` | 全部 | `uris, content_mode?(abstract/overview/read), max_chars?` | `Abstract`/`Overview`/`Read` | 指定层级内容 |
| `viking_grep` | 全部 | `uri, pattern, case_insensitive?, node_limit?` | `Grep` | 匹配片段 |
| `viking_store` | agent/admin | `messages, session_id?, commit?` | `CreateSession`/`AddMessage`/`CommitSession` | session_id + 提交结果 |
| `viking_add_resource` | agent/admin | `path, to?, parent?, wait?` | `AddResource` | 导入结果 |
| `viking_add_skill` | agent/admin | `data, wait?` | `AddSkill` | 注册结果 |
| `viking_health` | 全部 | （无） | `Status` | 健康状态 |
| `viking_forget` | 仅 admin+AllowForget | `uri, recursive?` | `Rm` | 删除结果 |

> 说明：`viking_read` 的 `content_mode` 直接对齐 OpenViking 的 L0/L1/L2——
> 默认 `read`(L2 全文)，模型也可显式取 `abstract`/`overview` 控制 token。

单个工具实现范式：

```go
// tool/openviking/tools.go (示意)
type vikingSearchArgs struct {
    Query     string  `json:"query" jsonschema:"description=Semantic query for OpenViking contexts"`
    TargetURI string  `json:"target_uri,omitempty" jsonschema:"description=Limit to a viking:// URI prefix"`
    SessionID string  `json:"session_id,omitempty" jsonschema:"description=Session id for context-aware search"`
    Limit     int     `json:"limit,omitempty" jsonschema:"description=Max results (default 8)"`
    MinScore  float64 `json:"min_score,omitempty" jsonschema:"description=Minimum relevance score"`
}

func (s *ToolSet) newSearchTool() tool.Tool {
    fn := func(ctx context.Context, a vikingSearchArgs) (string, error) {
        res, err := s.client.Search(ctx, openviking.SearchRequest{ /* map a */ })
        if err != nil {
            return "", err
        }
        return formatRetrieval(res), nil // [i] type score uri\nabstract
    }
    return function.NewFunctionTool(fn,
        function.WithName("viking_search"),
        function.WithDescription("Session-aware OpenViking retrieval for memories, resources, and skills. Returns URIs with summaries; use viking_read to fetch full content."))
}
```

输出统一为**文本**（与官方一致），方便模型直接阅读；检索结果格式 `[i] {type} score={s} {uri}\n{abstract}`，并在描述里**明确提示模型"先 search 再 read"**。

## 六、使用方式

```go
vikingTools, err := vikingtool.New(vikingtool.Options{
    URL:     "http://localhost:1933",
    APIKey:  os.Getenv("OPENVIKING_API_KEY"),
    Profile: vikingtool.ProfileAgent,
})
if err != nil { /* ... */ }
defer vikingTools.Close()

ag := llmagent.New("assistant",
    llmagent.WithModel(m),
    llmagent.WithToolSets(vikingTools), // 或 WithTools(vikingTools.Tools(ctx)...)
)
```

## 七、测试策略

- **单测**：用 `net/http/httptest` 起一个 mock OpenViking server，断言每个工具发出的 path/body 正确、能正确解析 `{status, result}`、对错误码做了重试。可参考 `agent/dify/dify_mock_server_test.go` 的 mock 风格。
- **示例**：`examples/openviking/main.go` 连接本地 `http://localhost:1933`，跑一遍 search→read→store 闭环（需 README 注明先 `openviking-server`）。

## 八、合规与规范

- 仅通过 HTTP 调用 OpenViking（网络边界），不链接其 AGPL-3.0 代码 → 本侧代码维持 Apache-2.0。
- 新增 Go 文件均带 Tencent Apache 2.0 License 头（CI 强制）。
- 注释一律英文；遵循「如无必要，无增实体」——HTTP client 单一实例，被 ToolSet 复用，未来 knowledge/memory 适配器继续复用同一 client。

## 九、交付拆分

1. `internal/openviking` 客户端（含 find/search/read/ls/grep/glob/store/add_*/status）+ 单测。
2. `tool/openviking` ToolSet + 各工具 + 单测。
3. `examples/openviking` demo + README。

三者可在一个 PR 内完成；若想更细，可把 (1) 单独成 PR 作为后续 knowledge/memory 接入的公共基座。
