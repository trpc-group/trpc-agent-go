# DSL Codegen

`trpc.group/trpc-go/trpc-agent-go/dsl/codegen` 用于把 DSL Graph（`*dsl.Graph`）生成一份可运行的 Go 代码（当前版本只生成单文件 `main.go`）。

后端服务层通常会直接依赖并调用这个包（`codegen.GenerateNativeGo`），而不是使用 `dsl/codegen/cmd/dsl_codegen_example`（该目录只是本仓库的本地演示/调试工具）。

## 运行模式

codegen 支持四种运行模式，通过 `WithRunMode` 选项指定：

| 模式 | 值 | 说明 |
|------|-----|------|
| Interactive | `interactive` | 默认模式，生成终端交互式 CLI 应用 |
| AG-UI | `agui` | 生成 AG-UI 协议 HTTP 服务器 |
| A2A | `a2a` | 生成 A2A (Agent-to-Agent) 协议服务器 |
| OpenAI | `openai` | 生成 OpenAI 兼容 API 服务器（`/v1/chat/completions`） |

## 快速使用（服务层）

下面示例演示：从 `workflow.json`（DSL）生成 `main.go`，并可选把源 DSL 一并写入输出目录用于溯源。

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/codegen"
	dslvalidator "trpc.group/trpc-go/trpc-agent-go/dsl/validator"
)

func main() {
	workflowJSON, err := os.ReadFile("workflow.json")
	if err != nil {
		panic(err)
	}

	// 1) Parse / validate
	g, err := dsl.NewParser().Parse(workflowJSON)
	if err != nil {
		panic(fmt.Errorf("parse workflow.json: %w", err))
	}
	if err := dslvalidator.New().Validate(g); err != nil {
		panic(fmt.Errorf("validate workflow.json: %w", err))
	}

	// 2) Codegen - 使用 functional options 模式
	out, err := codegen.GenerateNativeGo(g,
		codegen.WithAppName(g.Name),              // 可选：覆盖显示名称
		codegen.WithRunMode(codegen.RunModeA2A), // 可选：interactive(默认), agui, a2a, openai
	)
	if err != nil {
		panic(fmt.Errorf("codegen: %w", err))
	}

	// 3) Write files
	outDir := "./out"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	for name, src := range out.Files {
		if err := os.WriteFile(filepath.Join(outDir, name), src, 0o644); err != nil {
			panic(err)
		}
	}
	// 可选：把源 DSL 一起写入，方便定位"这份 main.go 是由哪个 DSL 生成的"
	_ = os.WriteFile(filepath.Join(outDir, "workflow.json"), workflowJSON, 0o644)
}
```

### 可用选项

| 选项函数 | 说明 | 默认值 |
|---------|------|-------|
| `WithPackageName(name)` | 生成代码的包名 | `"main"` |
| `WithAppName(name)` | 应用名称（用于日志） | `graph.Name` 或 `"dsl_app"` |
| `WithRunMode(mode)` | 运行模式 | `RunModeInteractive` |

生成的 `main.go` 顶部会包含运行说明（`go mod init/go get/...` + `export OPENAI_API_KEY=...` + `go run .`）。

## 支持的 DSL 子集（当前版本）

为了保持生成代码干净、可读，并避免引入 DSL runtime 依赖，当前 codegen 只覆盖一小部分 DSL 能力：

- `builtin.start`
- `builtin.llmagent`（要求 `config.model_spec`，支持 `mcp_tools`、`output_format` 结构化输出、generation config）
- `builtin.transform`（CEL-lite 表达式求值，结果写入 `state[<id>_parsed]`）
- `builtin.set_state`（CEL-lite 赋值表达式，更新 graph state）
- `builtin.mcp`（调用 MCP server tool，结果写入 `state[<id>_output/<id>_parsed]`）
- `builtin.user_approval`（用 `graph.Interrupt` 表达中断点；生成代码会打印 interrupt 信息，但**不会**自动生成 resume CLI）
- `builtin.while`（循环节点，codegen 时展开为 body 节点 + 合成的 conditional edge）
- `builtin.end`（可选 CEL-lite/JSON 表达式写入 `end_structured_output`）
- `conditional_edges`：支持 `==` 条件，表达式形如：
  - `input.output_parsed.field == "value"`（单层字段，要求该 node 配置了 `output_format.type="json"` 且有 schema）
  - `state.xxx == "value"`
  - 嵌套字段访问（如 `input.output_parsed.a.b`）通过 fallback 路径支持

## 关于密钥/环境变量

出于安全考虑，codegen **永远不会**把 `model_spec.api_key` 的明文写进生成的 Go 代码：

- 如果 DSL 里已经写了 `env:VAR`，生成代码会沿用该 `VAR`
- 如果 DSL 里是明文 key，生成代码会强制改用 `env:OPENAI_API_KEY`

因此运行生成代码时，需要在环境变量中提供对应的 key（缺失会报错）。

## 本仓库示例输出

`dsl/codegen/tmp/` 内有一份用 `examples/dsl/customer_service/workflow.json` 生成的示例输出（`main.go`），并附带同名 `workflow.json` 方便溯源。

如果你的服务层会把源 DSL 落盘用于溯源，请确保对其中的敏感字段（尤其是 `api_key`）做脱敏/占位符替换后再写入。
