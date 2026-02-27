## ToolSearch Benchmark

该 benchmark 位于 `benchmark/toolsearch`，用于：
- 评估集为 **单个 EvalCase 的多轮会话**（每行一轮）；用户输入已写入 `data/<app>/<evalset>.evalset.json`（`data/user-message.txt` 仅用于离线生成/溯源）
- 使用 `trpc-agent-go/evaluation` 执行评估
- 输出 tokens 使用量（区分主对话 vs tool-search 阶段）与运行耗时（整体 + 每轮）

### 快速开始
评估资产（evalset/metrics）通常只需要生成一次，仓库内已放在 `data/` 下；日常跑评估直接执行即可。

在 `benchmark/toolsearch/trpc-agent-go-impl` 目录运行：
- `go run . -model <MODEL_NAME> -mode llm -evalset toolsearch-mathtools-multiturn -max-tools 3`

### 输入与产物
- 评估输入：`data/<app>/<evalset>.evalset.json`（用户输入在 evalset 内的 `conversation` 里）
- 产物：
  - `data/<app>/<evalset>.evalset.json`
  - `data/<app>/<evalset>.metrics.json`
  - `output/<app>_*_*.evalset_result.json`
  - `output/<evalSetResultId>.summary.json`（本次运行的结构化 summary）

更详细参数说明见 `trpc-agent-go-impl/README.md`。
