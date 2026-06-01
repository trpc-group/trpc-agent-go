# OpenViking ToolSet — 工具 Schema 清单

本文档整理 `tool/openviking` 这个 ToolSet 发给 LLM 的工具结构:每个工具的
**名称、description、入参 schema、出参 schema**。内容由 `tool/openviking/tools.go`
的 Go 类型 + `jsonschema` tag 自动生成。

## Schema 生成规则(实现约定)

- **名称无前缀**:`ToolSet.Name()` 返回 `""`,所以工具保留原生 `viking_*` 名,不会被加
  `openviking_` 前缀。
- **入参 schema** 由入参 struct 反射生成(`internal/tool.GenerateJSONSchema`):
  - 字段名取 `json` tag。
  - description 取 `jsonschema:"description=..."`。
  - **required 判定**:字段为**非指针**且 `json` tag **不含 `omitempty`**(或显式
    `jsonschema:"required"`)→ required;否则可选。
  - 类型映射:`string→string`、`int→integer`、`float64→number`、`bool→boolean`。
- **出参 schema** 由返回类型生成并放入 `Declaration().OutputSchema`。返回 `string`
  的工具,出参 schema 就是 `{"type":"string"}`,字符串内容是 OpenViking 服务端的原始
  JSON(透传)。

## 工具与 Profile 的关系

| Profile | 暴露的工具 |
|---|---|
| `retrieval` | find, search, browse, read, grep, health |
| `agent`(默认) | retrieval + store, add_resource, add_skill |
| `admin` | agent + forget |

`viking_forget` 仅在 `admin` profile 下暴露。

> 下表里所有 `viking://` URI、`limit`、`min_score` 等的 description 均为代码中的原文。
> `viking_find` / `viking_search` 的 description 末尾在 `viking_read` 同时暴露时会追加
> 一句 `" Call viking_read on a URI to fetch full content."`。

---

## 1. `viking_find`

**Description**:`Quick semantic recall from OpenViking without session context. Returns matching viking:// URIs with short summaries (not full content).`（含 read 时追加 ` Call viking_read on a URI to fetch full content.`）

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `query` | string | ✅ | Semantic query to recall OpenViking contexts |
| `target_uri` | string | ✗ | Optional viking:// URI prefix to limit the search scope |
| `limit` | integer | ✗ | Maximum number of hits (default 8) |
| `min_score` | number | ✗ | Minimum relevance score threshold |

**出参**(`retrievalOutput`)

```json
{
  "hits": [
    { "type": "string", "uri": "string", "score": 0, "level": 0, "abstract": "string" }
  ],
  "hint": "string"
}
```

`type` 为 `memory` / `resource` / `skill`;`level` 为 0/1/2(L0/L1/L2);`hint` 引导下一步。

---

## 2. `viking_search`

**Description**:`Session-aware hierarchical retrieval over OpenViking memories, resources, and skills. Returns matching viking:// URIs with short summaries.`（含 read 时追加 ` Call viking_read on a URI to fetch full content.`）

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `query` | string | ✅ | Semantic query to recall OpenViking contexts |
| `target_uri` | string | ✗ | Optional viking:// URI prefix to limit the search scope |
| `session_id` | string | ✗ | Optional session id for context-aware retrieval |
| `limit` | integer | ✗ | Maximum number of hits (default 8) |
| `min_score` | number | ✗ | Minimum relevance score threshold |

**出参**:同 `viking_find`(`retrievalOutput`)。

---

## 3. `viking_browse`

**Description**:`Browse OpenViking namespaces: list a viking:// URI, or glob when a pattern is provided.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `uri` | string | ✗ | viking:// URI to list (default viking://) |
| `recursive` | boolean | ✗ | List recursively |
| `pattern` | string | ✗ | Optional glob pattern; when set a glob search is performed instead of ls and recursive is ignored |

**出参**:`{"type":"string"}` —— OpenViking `ls`/`glob` 的原始 JSON(节点列表,含 `uri`/`isDir`/`abstract` 等)。

---

## 4. `viking_read`

**Description**:`Read an OpenViking URI at a chosen level: abstract (L0), overview (L1), or read (L2 full content). abstract and overview apply to directory nodes only (they return that directory's .abstract.md/.overview.md); for a file/leaf URI use read. The isDir field from viking_browse and the level field from viking_search/viking_find tell you which a hit is. Prefer overview for large directories and use read with offset/limit to page through long files.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `uri` | string | ✅ | viking:// URI to read |
| `content_mode` | string | ✗ | One of abstract (L0 summary) / overview (L1) / read (L2 full content). Default read. abstract 和 overview 只对目录 URI 有效;文件/叶子用 read |
| `offset` | integer | ✗ | Start line for read mode (0-indexed) |
| `limit` | integer | ✗ | Number of lines for read mode (-1 reads to end) |
| `max_chars` | integer | ✗ | Truncate the returned content to this many characters (0 means no limit) |

**出参**(`readOutput`)

```json
{
  "uri": "string",
  "content_mode": "string",
  "content": "string",
  "truncated": false
}
```

`truncated` 带 `omitempty`,为 false 时不出现。

---

## 5. `viking_grep`

**Description**:`Grep-style content search within an OpenViking URI subtree.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `uri` | string | ✅ | viking:// URI to search within |
| `pattern` | string | ✅ | Pattern to match in node content |
| `case_insensitive` | boolean | ✗ | Case-insensitive match |
| `node_limit` | integer | ✗ | Maximum number of nodes to scan |

**出参**:`{"type":"string"}` —— OpenViking `grep` 的原始 JSON(命中节点/行)。

---

## 6. `viking_store`

**Description**:`Store a message into an OpenViking session, optionally committing to trigger memory extraction.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `content` | string | ✅ | Text content of the message to store |
| `role` | string | ✗ | Message role: user or assistant (default user)。仅接受 user/assistant,其它报错 |
| `session_id` | string | ✗ | Existing session id; a new session is created when empty |
| `commit` | boolean | ✗ | Commit the session after storing to trigger memory extraction |

**出参**(`storeOutput`)

```json
{ "session_id": "string", "committed": false }
```

---

## 7. `viking_add_resource`

**Description**:`Import a URL, remote path, or repository into OpenViking resources for later retrieval. For large imports leave wait=false (the default) to avoid the per-request timeout; the server keeps processing in the background after the call returns.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `path` | string | ✅ | A URL or remote path/repository to import into OpenViking resources |
| `to` | string | ✗ | Optional destination viking:// URI |
| `parent` | string | ✗ | Optional parent viking:// URI |
| `wait` | boolean | ✗ | Wait for semantic processing to finish before returning |

**出参**:`{"type":"string"}` —— 导入任务结果原始 JSON(含 `task_id`/`status`/`root_uri` 等)。

---

## 8. `viking_add_skill`

**Description**:`Register a reusable skill in OpenViking.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `data` | string | ✅ | Skill definition (text or a path/URL accepted by OpenViking) |
| `wait` | boolean | ✗ | Wait for processing to finish before returning |

**出参**:`{"type":"string"}` —— 服务端原始 JSON。

---

## 9. `viking_health`

**Description**:`Check OpenViking server status.`

**入参**:无(空 object,无属性)。

**出参**:`{"type":"string"}` —— `/system/status` 的原始 JSON。

---

## 10. `viking_forget`（仅 admin）

**Description**:`Remove a URI from OpenViking. Destructive; only available with the admin profile.`

**入参**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `uri` | string | ✅ | viking:// URI to remove |
| `recursive` | boolean | ✗ | Remove recursively |

**出参**:`{"type":"string"}` —— 删除结果原始 JSON。

---

## 附:LLM 看到的工具声明示例(以 `viking_read` 为例)

```json
{
  "type": "function",
  "function": {
    "name": "viking_read",
    "description": "Read an OpenViking URI at a chosen level: abstract (L0), overview (L1), or read (L2 full content). abstract and overview apply to directory nodes only ...",
    "parameters": {
      "type": "object",
      "properties": {
        "uri":          { "type": "string",  "description": "viking:// URI to read" },
        "content_mode": { "type": "string",  "description": "One of abstract (L0 summary)|overview (L1)|read (L2 full content). Default read. ..." },
        "offset":       { "type": "integer", "description": "Start line for read mode (0-indexed)" },
        "limit":        { "type": "integer", "description": "Number of lines for read mode (-1 reads to end)" },
        "max_chars":    { "type": "integer", "description": "Truncate the returned content to this many characters (0 means no limit)" }
      },
      "required": ["uri"]
    }
  }
}
```
