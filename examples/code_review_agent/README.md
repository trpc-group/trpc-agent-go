# Code Review Agent

基于 Skills + 沙箱 + 数据库存储的自动 Go 代码评审 Agent。

## 目录结构

```
examples/code_review_agent/
├── main.go                          # CLI 入口
├── main_test.go                     # 集成测试（9 fixture + 验收标准验证）
├── go.mod / go.sum
├── DESIGN.md                        # 方案设计说明
├── README.md
├── Dockerfile                       # 容器沙箱镜像
├── skills/code-review/
│   ├── SKILL.md                     # 技能定义
│   ├── docs/rules.md                # 规则文档
│   ├── rules/rules.json             # JSON 规则配置
│   └── scripts/checkrunner/         # 沙箱内执行的 Go 检查二进制
├── fixtures/diffs/                  # 9 个测试 diff 样本
├── migrations/                      # SQLite schema DDL
├── sample_output/                   # 示例报告
└── internal/
    ├── analysis/                    # 规则引擎 + 去重 + 置信度分桶
    ├── app/                         # 流程编排
    ├── diffparse/                   # unified diff 解析
    ├── governance/                  # fail-closed 权限策略
    ├── input/                       # diff 文件/仓库加载
    ├── redact/                      # 敏感信息检测与脱敏
    ├── report/                      # JSON + Markdown 双格式报告
    ├── reviewmodel/                 # 领域模型
    ├── sandbox/                     # 沙箱执行 + 离线模块代理
    └── store/                       # 存储接口 + SQLite 实现
```

## 快速开始

```bash
cd examples/code_review_agent

# 下载依赖
go mod tidy

# 构建
go build -o code_review_agent .

# 评审 diff 文件
./code_review_agent --diff-file fixtures/diffs/security.diff

# Dry run 模式（不执行沙箱检查，完整流程 <1s）
./code_review_agent --diff-file fixtures/diffs/security.diff --dry-run

# 评审 Go 仓库
./code_review_agent --repo-path /path/to/go/project

# 使用 SQLite 存储（默认 review.db）
./code_review_agent --diff-file fixtures/diffs/security.diff --db review.db
```

## Docker 沙箱

```bash
# 构建容器镜像（~5分钟，334MB）
docker build -t cr-sandbox:latest .

# 在容器中执行 go vet
docker run --rm -v $(pwd)/repo:/workspace cr-sandbox:latest -mode vet

# 在容器中执行 go test
docker run --rm -v $(pwd)/repo:/workspace cr-sandbox:latest -mode test -timeout 30
```

## 输入格式

| 参数 | 说明 |
|------|------|
| `--diff-file` | unified diff 文件路径 |
| `--repo-path` | Go 项目目录路径 |
| `--dry-run` | 跳过沙箱执行，仅规则分析 |
| `--sandbox` | 沙箱类型: local / container / fake |
| `--db` | SQLite 数据库路径（默认 review.db） |
| `--output-json` | JSON 报告路径（默认 review_report.json） |
| `--output-md` | Markdown 报告路径（默认 review_report.md） |

## 审查规则

| 类别 | 规则数 | 示例 |
|------|--------|------|
| 安全风险 | 1 | `exec.Command` 命令注入 |
| Goroutine/Context | 1 | goroutine 无 context 传播 |
| 资源生命周期 | 2 | `os.Open` 无 `defer Close` |
| 数据库连接 | 1 | `sql.Open` 无连接池管理 |
| 错误处理 | 2 | 忽略 error、裸 panic |
| 测试覆盖 | 1 | 新增函数无对应 test 文件 |
| 敏感信息 | 1 | 硬编码 API Key / Password / Token |

## 运行测试

```bash
# 全部测试（85+ 个）
go test ./...

# 仅集成测试（9 个 fixture 全流程）
go test -run TestReview -v

# 单元测试
go test ./internal/... -v
```

## 验收标准

| # | 标准 | 验证方式 |
|---|------|---------|
| 1 | 9 条 diff 样本全部可运行生成报告 | `TestReview*` 全部 PASS |
| 2 | 高危检出 + 低置信度降噪 | `TestNormalize` / `TestDeduplicate` |
| 3 | DB 可查 task/sandbox/finding/permission | `TestFullTaskQueryability` |
| 4 | 沙箱超时/失败不崩溃 | `TestTimeout` / `TestLocalGoVet` |
| 5 | 脱敏检出 + 无明文泄露 | `TestReviewSecretRedactionDiff` |
| 6 | dry-run < 2 分钟 | 全部 fixture < 1s |
| 7 | deny 不进入沙箱 | `TestGovernanceDenyBlocksSandbox` |
| 8 | 报告含全部必需章节 | `TestReportContainsAllRequiredSections` |

## 方案设计

详见 [DESIGN.md](DESIGN.md)
