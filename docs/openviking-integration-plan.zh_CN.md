# trpc-agent-go 接入 OpenViking 方案

> 本文基于对 OpenViking 官方仓库（`volcengine/OpenViking`，已 clone 至 `.vscode/OpenViking`）
> LangChain/LangGraph 集成源码的通读，结合 trpc-agent-go 现有接口设计而成。

## 一、背景与结论

OpenViking 是火山引擎开源的 **面向 AI Agent 的「上下文数据库 / 上下文文件系统」**，用「文件系统范式」（`viking://` 虚拟文件树 + L0/L1/L2 分层加载 + 目录递归检索 + 会话记忆自迭代）替代传统扁平向量 RAG。

通读其 `openviking/integrations/langchain/` 后的核心结论：

> **所谓「LangChain / LangGraph 接入」不存在任何深度耦合，全部是一个 HTTP 客户端在调用 OpenViking server 的 REST API。**

`client.py` 中真正建立连接的只有一处：

```python
from openviking.client import SyncHTTPClient
client = SyncHTTPClient(url=..., api_key=..., ...)
```

所有适配器（Retriever / Store / Tools / Middleware / History）都只是把宿主框架的某个扩展点，转调到这个 client 的方法（`find` / `search` / `read` / `ls` / `grep` / `create_session` / `add_message` / `commit_session` / `add_resource` 等）。

**接入范式可总结为：** OpenViking = 独立 HTTP 服务；接入方写一个瘦 HTTP 客户端 + 把它转接到宿主框架的「检索 / 状态 / 工具 / 会话」四个扩展点。

## 二、OpenViking HTTP API（实测端点，前缀 `/api/v1`）

来源：`openviking/server/routers/`。

| 能力 | 端点 | 用途 |
|---|---|---|
| 检索 | `POST /search/find` | 无会话快速语义召回 |
| 检索 | `POST /search/search` | 带 session 的层级检索 |
| 检索 | `POST /search/grep`、`POST /search/glob` | 内容 / 路径匹配 |
| 文件系统 | `GET /fs/read`、`/fs/abstract`(L0)、`/fs/overview`(L1)、`POST /fs/write` | 分层读取 / 写入 |
| 浏览 | `ls` / `glob` | 目录遍历 |
| 资源 | `POST /resources`、`POST /skills` | 导入资源 / 注册技能 |
| 会话 | `POST /sessions`、`POST /sessions/{id}/messages`、`POST /sessions/{id}/commit`、`POST /sessions/{id}/extract`、`GET /sessions/{id}/context` | 会话流式写入 + 记忆抽取 |
| 系统 | `GET /api/v1/system/status`、`GET /health` | 健康检查 |

`find` / `search` 请求体（pydantic 模型，见 `search.py`）：

```json
{
  "query": "...",
  "target_uri": "viking://user/memories",
  "session_id": "optional",
  "limit": 10,
  "score_threshold": 0.5,
  "filter": {},
  "level": null
}
```

响应：`{ "status": "ok", "result": { "memories": [], "resources": [], "skills": [] } }`，
每个 item 带 `uri / score / abstract / overview / level / category`。

### LangChain 侧 4 类适配器对照

| 适配器 | LangChain/LangGraph 扩展点 | 底层调用 |
|---|---|---|
| `OpenVikingRetriever` | `BaseRetriever` | `find`/`search` → `Document`，content 按 L0/L1/L2 取，`level==2` 才 `read` 全文 |
| `create_openviking_tools()` | `StructuredTool` 列表 | 12 个 `viking_*` 工具，按 `profile`(retrieval/agent/admin) 选子集 |
| `OpenVikingStore` | LangGraph `BaseStore` | put → `fs/write` JSON + markdown 索引投影；search → `search`；根目录 `viking://user/memories/langgraph_store` |
| `OpenVikingChatMessageHistory` / `with_openviking_context` / `OpenVikingContextMiddleware` | 会话生命周期 | `create_session` / `add_message` / `commit_session`（按 `CommitPolicy`: never/always/pending_tokens） |

## 三、trpc-agent-go 对接点（已核对接口）

trpc-agent-go 恰好有四个与 LangChain 一一对应的扩展接口：

| 能力 | trpc-agent-go 接口 | 文件 |
|---|---|---|
| 工具 | `tool.CallableTool`（`Declaration()` + `Call(ctx, jsonArgs)`） | `tool/tool.go` |
| RAG 检索 | `knowledge.Knowledge`（`Search(ctx, *SearchRequest) (*SearchResult, error)`） | `knowledge/knowledge.go` |
| 长期记忆 | `memory.Service`（`SearchMemories`/`AddMemory`/`ReadMemories`/`Tools()`/`EnqueueAutoMemoryJob`…） | `memory/memory.go` |
| 会话 | `session.Service` + runner 生命周期钩子 | `session/` |

映射关系几乎是平移：

```
LangChain                         trpc-agent-go
─────────────────────────────────────────────────────────
SyncHTTPClient            ──►     openviking.Client (Go, net/http)
create_openviking_tools() ──►     []tool.CallableTool   (viking_* 工具)
OpenVikingRetriever       ──►     knowledge.Knowledge   (Search 适配)
OpenVikingStore           ──►     memory.Service        (SearchMemories/AddMemory…)
ContextMiddleware/History ──►     session 同步 + EnqueueAutoMemoryJob → commit/extract
```

## 四、接入方案

遵循「如无必要，无增实体」原则：**只建一个共享 HTTP 客户端，然后按需薄封装到上述 4 个接口**。
建议放在社区贡献目录下（与 `agent/dify` 同款约定）。

### 分层结构

```
共享层
  internal/openviking/client/    # 唯一实体：Go HTTP 客户端，封装 /api/v1/* 端点
    client.go                    #   Find / Search / Read / Ls / Grep / Glob
    session.go                   #   CreateSession / AddMessage / CommitSession / Extract
    resource.go                  #   AddResource / AddSkill
    types.go                     #   请求 / 响应 struct（对齐 pydantic 模型）

适配层（按优先级落地）
  tool/openviking/               # P0: viking_* 工具集 → []tool.CallableTool
  knowledge/openviking/          # P1: 实现 knowledge.Knowledge (RAG 后端)
  memory/openviking/             # P2: 实现 memory.Service (长期记忆后端)
  runner hook / session 同步      # P3: 会话流式写入 + 自动 commit/extract
```

### 阶段 P0 — Tool 接入（最高性价比，1 个 PR 即可用）

照搬 `create_openviking_tools()` 的 12 个工具，实现 `tool.CallableTool`，`Call` 中调用 HTTP client。

```go
// tool/openviking/tools.go (示意)
type Options struct {
    URL     string        // http://localhost:1933
    APIKey  string
    Profile string        // "retrieval" | "agent" | "admin"
    Timeout time.Duration
}

// NewTools returns viking_* tools selected by profile.
func NewTools(opts Options) ([]tool.CallableTool, error) { /* ... */ }
```

挂到任意 LLMAgent：`llmagent.New("assistant", llmagent.WithTools(vikingTools...))`。
让模型自己决定 `viking_search` / `viking_read` / `viking_store`——这正是 OpenViking 的 MCP/plugin 原生用法，效果最自然。

工具清单（与官方一致）：`viking_find`、`viking_search`、`viking_browse`、`viking_read`、`viking_grep`、`viking_archive_search`、`viking_archive_expand`、`viking_store`、`viking_add_resource`、`viking_add_skill`、`viking_health`、`viking_forget`(仅 admin)。

### 阶段 P1 — Knowledge 接入（RAG 后端）

实现 `knowledge.Knowledge`，把 trpc 的 `SearchRequest` 转成 OV 的 `find/search`：

```go
// knowledge/openviking/knowledge.go (示意)
func (k *Knowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
    // req.History 非空 → 走带 session 的 /search/search，否则 /search/find
    // req.Query / MaxResults / MinScore → query / limit / score_threshold
    // 结果 item.{uri, score, abstract/overview} → SearchResult.Documents
    // 命中 level==2 时再调 /fs/read 取全文（对齐 retrievers.py 的 content_mode=auto）
}
```

任何使用 `knowledge` 的 agent 都能无感把 RAG 后端切到 OpenViking，享受目录递归检索 + 分层加载省 token。

### 阶段 P2 — Memory 接入（长期记忆后端）

实现 `memory.Service`，映射到 OV 的 `viking://user/memories` 与 session 抽取：

| memory.Service 方法 | OpenViking 调用 |
|---|---|
| `SearchMemories` | `search`(target_uri=`viking://user/memories` / `viking://agent/memories`) |
| `ReadMemories` | `ls` + `read` |
| `AddMemory` | `fs/write` 或 `add_message` + `commit` |
| `DeleteMemory` | `rm` |
| `EnqueueAutoMemoryJob` | `sessions/{id}/commit` + `/extract`（异步记忆自迭代，对应 CommitPolicy） |
| `Tools()` | 复用 P0 的 `viking_*` 检索类工具 |

### 阶段 P3 — 会话同步（可选，闭环「越用越聪明」）

在 runner 会话生命周期里把每轮 user/assistant 消息 `add_message` 进 OV session，
按 token 阈值触发 `commit`（对齐 `OpenVikingCommitPolicy.pending_tokens`），
让 OpenViking 自动抽取长期记忆。

## 五、工程注意点

- **复用单一 client**：4 个适配器共享一个 `openviking.Client`，避免重复造轮子。
- **认证**：HTTP 头 `X-API-Key` / `X-OpenViking-Account` / `X-OpenViking-User`（见 `server/auth.py`），client 统一注入。
- **错误恢复**：照搬 `client.py` 策略——`DEADLINE_EXCEEDED` / `UNAVAILABLE` / 连接类错误对只读方法做一次重试。
- **License 合规**：OpenViking 主体为 **AGPL-3.0**，但其 **HTTP API / SDK client 是网络边界**——我们只做 HTTP 客户端调用，不链接其 AGPL 代码，不构成衍生作品；trpc-agent-go 侧保持 Apache-2.0 即可。这也是官方推荐 HTTP 部署模式的合规价值。
- **License 头**：新增 Go 文件需带 Tencent Apache 2.0 头（CI 强制）。

## 六、落地顺序

1. **P0 Tool 接入**（最小可用，1~2 天）→ 立刻能在任意 agent 里用 OpenViking。
2. **P1 Knowledge 接入** → 作为 RAG 后端，复用现有 knowledge 生态。
3. **P2 Memory 接入** + **P3 会话同步** → 完整记忆闭环。

每个阶段独立成 PR，互不阻塞，且都可单独配 `examples/openviking/` demo（连本地 `http://localhost:1933`）。
