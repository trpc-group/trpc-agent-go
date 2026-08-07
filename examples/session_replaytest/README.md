# Session / Memory 多后端回放一致性测试框架 (Issue #2001)

本框架提供跨存储后端（如 InMemory 与 SQLite/Redis）的 Session、Memory、Summary（含 filter-key）和 Track 事件回放一致性测试。

## 设计说明 (Design Overview)

### 1. 归一化策略 (Normalization Strategy)
不同存储后端在记录事件与状态时，可能存在时间戳浮动（±1s）、随机生成 ID、map 遍历顺序非确定性以及特定私有元数据字段差异。框架在比对前使用 `NormalizeKeys()` 与 `FormatSummary()` 对 map 字典键值与 Summary filter-key 进行确定性排序与格式归一化。

### 2. Summary 与 Track 比较策略
针对 Go 语言框架特有的 Summary filter-key 分域架构与 Track 观测轨迹，比较器不仅校验全局 Session Summary 内容，更精确比对特定 `filter_key` 维度下的文本一致性，并对追踪日志中的延迟与操作序列进行对齐校验。

### 3. 后端接入说明 (Backend Integration)
- **轻量模式 (Lightweight Mode)**：默认开启 InMemory 与 SQLite 驱动，无需任何配置，毫秒级运行完成。
- **扩展模式 (Integration Mode)**：支持通过环境变量（如 `REPLAYTEST_REDIS_URL` / `REPLAYTEST_POSTGRES_DSN`）开启真实分布式后端连接比对。

## 运行与测试

```bash
# 运行单元测试
go test -v -count=1 ./...

# 静态格式与诊断检查
gofmt -l .
go vet ./...

# 运行全量 10 Case 回放比对并生成 JSON 报告
go run . -output output
```
