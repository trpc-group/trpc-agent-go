## benchmark/toolsearch (trpc-agent-go-impl)

### 功能
- 用 `trpc-agent-go/evaluation` 执行 toolsearch 评测
- 输出：整体耗时（evaluation executionTime + wall time）、tokens（chat / toolsearch / total）、每轮的期望工具与实际工具、每轮 tokens 与耗时
- 额外落盘一份结构化 summary：`<output-dir>/<evalSetResultId>_<mode>.summary.json`

### 运行
在本目录执行：
- `go run . -model deepseek-chat -mode llm -evalset toolsearch-mathtools-multiturn -max-tools 5`

### 重要参数
- `-mode`: `none | llm | knowledge`
- `-data-dir`: 默认 `../data`（读取 `<data-dir>/<app>/<evalset>.{evalset,metrics}.json`）
- `-output-dir`: 默认 `../output`（evaluation result 落盘目录）

### 环境变量
- `MODEL_NAME`: 未显式传 `-model` 时作为默认
- `OPENAI_API_KEY` / `OPENAI_BASE_URL`: 取决于所用 model provider
