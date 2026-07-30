# Testing Rules

> **Limitations**: Token rules match against added (`+`) lines in unified diffs. The TEST-001 heuristic requires ≥4 added code lines to flag a file. Multi-line patterns may cause false negatives. See `internal/ruleengine/ruleengine.go`.

## TEST-001: New Public Function Without Corresponding Test

- **type**: token
- **severity**: medium
- **pattern**: `func\s+[A-Z]\w+\(`
- **message**: "本次变更新增公开函数，但 diff 中未出现对应的 _test.go 文件或现有测试文件中未新增测试函数"
- **fix**: "为新增的公开函数编写对应的测试用例。Go 测试文件命名规范: <filename>_test.go"

Detection heuristic: only triggers when the same file has ≥4 added code lines
(excludes trivial one-liner wrappers). Single-return functions like
`func IsNotEmpty(s string) bool { return len(s) > 0 }` are excluded.
A minimal Go function (signature + one-liner body + closing brace) = 3 code lines.

## TEST-002: New Exported Type Without Test Coverage

- **type**: token
- **severity**: low
- **pattern**: `type\s+[A-Z]\w+\s+struct`
- **message**: "本次变更新增公开结构体，但未出现对应的测试文件"
- **fix**: "为新增结构体及其公开方法编写测试"

## DETECTION LOGIC

检测逻辑:
1. 提取 diff 中所有 `+` 行（新增行）
2. 匹配 `func [A-Z]\w+(` → 公开函数声明
3. 匹配 `type [A-Z]\w+ struct` → 公开结构体声明
4. 检查 diff 中是否同时出现对应的 `_test.go` 文件
5. 如果新增公开函数/类型，但无对应测试文件 → 触发 TEST-001/TEST-002

注意: 第一版基于 diff-file 输入，只能检测"本次变更是否漏加测试"，
而非"整个仓库是否缺少测试覆盖"。--repo-path 模式预留了全仓库扫描能力。
