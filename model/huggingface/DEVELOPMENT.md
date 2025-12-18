# HuggingFace 模型集成 - 开发任务文档

## 📋 项目概述

为 tRPC-Agent-Go 框架集成 HuggingFace Inference API 支持，使开发者能够使用 HuggingFace 上的开源大语言模型。

**开发分支**: `feature/huggingface-model-integration`  
**开始时间**: 2025-12-12  
**当前状态**: ✅ 核心功能完成，Token Tailoring 已实现，多模态支持已完成，测试通过

---

## ✅ 已完成任务

### 阶段 1: 基础架构搭建 (已完成)

- [x] 创建 `model/huggingface` 包结构
- [x] 定义配置选项 (`options.go`)
  - API Key 配置
  - 模型名称配置
  - 超时配置
  - 自定义 HTTP 客户端
  - 自定义 API 端点
- [x] 定义 HuggingFace API 类型 (`types.go`)
  - 请求/响应结构体
  - 流式响应结构体
  - 错误处理类型

### 阶段 2: 核心功能实现 (已完成)

- [x] 实现 `Model` 接口 (`huggingface.go`)
  - `GenerateContent()` - 核心生成方法
  - `Name()` - 模型名称
  - `Close()` - 资源清理
- [x] 实现请求/响应转换 (`converter.go`)
  - `model.Request` → HuggingFace API 格式
  - HuggingFace 响应 → `model.Response`
  - 流式响应处理
  - SSE (Server-Sent Events) 解析
- [x] 支持流式和非流式响应
  - 非流式: 一次性返回完整响应
  - 流式: 通过 channel 逐步返回 chunks
- [x] 实现回调机制
  - `OnRequest` - 请求发送前回调
  - `OnChunk` - 每个 chunk 接收时回调
  - `OnStreamingComplete` - 流式完成回调

### 阶段 3: 测试覆盖 (已完成)

#### Mock 测试 (`huggingface_test.go`)
- [x] 非流式响应测试
  - 成功场景
  - nil 请求处理
  - 空消息列表处理
- [x] 流式响应测试
  - 多 chunk 流式传输
  - 空 chunk 处理
- [x] 回调机制测试
  - 请求回调验证
  - Chunk 回调验证
  - 流式完成回调验证
- [x] **测试覆盖率**: 100%
- [x] **测试通过率**: 100%

#### 集成测试 (`integration_test.go`)
- [x] 真实 API 非流式测试
- [x] 真实 API 流式测试
- [x] 真实 API 回调测试
- [x] 环境变量控制机制
- [x] 测试文档 (`TESTING.md`)
- [x] 交互式运行脚本 (`run_integration_tests.sh`)

### 阶段 4: 文档和示例 (已完成)

- [x] README 文档 (`README.md`)
  - 快速开始指南
  - 配置选项说明
  - 使用示例
  - 支持的模型列表
- [x] 测试文档 (`TESTING.md`)
  - Mock 测试说明
  - 集成测试说明
  - 推荐测试模型
  - 调试技巧
- [x] 代码注释完整性
  - 所有公开方法都有文档注释
  - 关键逻辑有行内注释

### 阶段 5: 代码质量 (已完成)

- [x] 错误处理完善
  - API 错误解析
  - 网络错误处理
  - 超时处理
- [x] 资源管理
  - HTTP 连接复用
  - Goroutine 清理
  - Channel 关闭
- [x] 代码规范
  - 遵循 Go 编码规范
  - 通过 golangci-lint 检查

### 阶段 6: Token Tailoring (已完成 - 2025-12-17)

- [x] 实现 Token Tailoring 核心逻辑
  - 自动计算 max input tokens
  - 自动设置 max output tokens
  - 支持自定义裁剪策略
- [x] 集成 internal/model 包的辅助函数
  - `ResolveContextWindow()` - 获取模型上下文窗口
  - `CalculateMaxInputTokens()` - 计算最大输入 tokens
  - `CalculateMaxOutputTokens()` - 计算最大输出 tokens
- [x] 添加配置选项
  - `WithEnableTokenTailoring()` - 启用 Token Tailoring
  - `WithMaxInputTokens()` - 设置最大输入 tokens
  - `WithTokenCounter()` - 自定义 token 计数器
  - `WithTailoringStrategy()` - 自定义裁剪策略
  - `WithTokenTailoringConfig()` - 自定义裁剪参数
- [x] 编写完整测试用例
  - 自动计算测试
  - 自定义配置测试
  - 用户配置优先级测试
  - 集成测试
- [x] 所有测试通过 ✅

---

## 📊 当前代码统计

| 文件 | 行数 | 说明 |
|------|------|------|
| `huggingface.go` | 487 | 核心实现 (+75 Token Tailoring) |
| `options.go` | 277 | 配置选项 |
| `types.go` | 178 | 类型定义 |
| `converter.go` | 295 | 格式转换（含多模态支持） |
| `huggingface_test.go` | 1,167 | Mock 测试 (+220 Token Tailoring + 456 多模态测试) |
| `integration_test.go` | 274 | 集成测试 |
| `README.md` | 191 | 使用文档 |
| `TESTING.md` | 158 | 测试文档 |
| **总计** | **~3,027** | **代码+文档** |

---

## 🎯 下一步计划

### ✅ 已完成 - 优先级 P0 & P1

#### 1. Token Tailoring (对话历史裁剪) 🔥 ✅
**目标**: 自动管理对话历史，避免超出模型 token 限制

**完成情况**:
- [x] 研究 OpenAI 模型的 Token Tailoring 实现
  - 参考了 `model/openai/openai.go` 中的 `applyTokenTailoring` 方法
  - 理解了裁剪策略和算法
- [x] 实现 HuggingFace 的 Token 计数
  - 复用 `model.TokenCounter` 接口
  - 支持 `SimpleTokenCounter` 和自定义计数器
- [x] 实现对话历史裁剪逻辑
  - 使用 `TailoringStrategy` 接口
  - 支持 `MiddleOutStrategy` 等策略
  - 自动保留系统消息和最近消息
- [x] 添加配置选项
  - `WithEnableTokenTailoring()` - 启用自动裁剪
  - `WithMaxInputTokens()` - 设置最大输入 tokens
  - `WithTokenCounter()` - 自定义 token 计数器
  - `WithTailoringStrategy()` - 自定义裁剪策略
  - `WithTokenTailoringConfig()` - 自定义裁剪参数
- [x] 编写测试用例
  - 5 个单元测试场景
  - 1 个集成测试
  - 所有测试通过 ✅

**实际工作量**: 2 小时  
**完成时间**: 2025-12-17

**实现亮点**:
1. 完全复用了 OpenAI 模型的实现逻辑
2. 支持自动计算 max input/output tokens
3. 支持自定义裁剪参数（协议开销、安全边界等）
4. 尊重用户显式配置（用户设置的 MaxTokens 不会被覆盖）
5. 完整的测试覆盖

#### 2. 多模态支持（图像输入） 🎨 ✅
**目标**: 支持在对话中发送图像，实现视觉理解能力

**完成情况**:
- [x] 基础结构已存在
  - `ContentPart` 和 `ImageURL` 类型定义
  - `convertContentPart` 转换逻辑
- [x] 完善测试覆盖
  - 图像 URL 测试
  - Base64 编码图像测试
  - 多图像输入测试
  - 流式响应测试
  - 转换函数单元测试
- [x] 支持的图像格式
  - HTTP/HTTPS URL
  - Base64 编码（data URL）
  - 多种图像格式（PNG、JPEG 等）
- [x] 图像细节控制
  - `auto` - 自动选择
  - `low` - 低分辨率
  - `high` - 高分辨率

**实际工作量**: 2 小时  
**完成时间**: 2025-12-18

**实现亮点**:
1. 完全兼容 OpenAI 的多模态 API 格式
2. 支持 URL 和 Base64 两种图像输入方式
3. 支持单个或多个图像输入
4. 支持流式和非流式响应
5. 完整的测试覆盖（10个测试用例）
6. 自动处理图像格式转换

**使用示例**:
```go
// 使用图像 URL
request := &model.Request{
    Messages: []model.Message{
        {
            Role: model.RoleUser,
            ContentParts: []model.ContentPart{
                {
                    Type: model.ContentTypeText,
                    Text: stringPtr("What's in this image?"),
                },
                {
                    Type: model.ContentTypeImage,
                    Image: &model.Image{
                        URL:    "https://example.com/image.jpg",
                        Detail: "high",
                    },
                },
            },
        },
    },
}

// 使用 Base64 编码图像
request := &model.Request{
    Messages: []model.Message{
        {
            Role: model.RoleUser,
            ContentParts: []model.ContentPart{
                {
                    Type: model.ContentTypeText,
                    Text: stringPtr("Describe this image"),
                },
                {
                    Type: model.ContentTypeImage,
                    Image: &model.Image{
                        Data:   []byte(base64Image),
                        Format: "png",
                        Detail: "auto",
                    },
                },
            },
        },
    },
}
```

---

### 优先级 P0 (高优先级 - 建议立即开始)



#### 2. 错误处理增强 🛡️
**目标**: 提供更友好的错误信息和重试机制

**任务清单**:
- [ ] 细化错误类型
  - 认证错误
  - 限流错误
  - 模型不可用错误
  - 网络错误
- [ ] 实现重试机制
  - 指数退避策略
  - 可配置重试次数
  - 特定错误类型重试
- [ ] 添加错误日志
  - 结构化日志输出
  - 请求/响应详情记录
- [ ] 编写测试用例

**预计工作量**: 1-2 天

---

### 优先级 P1 (中优先级 - 后续迭代)

#### 3. 多模态支持 (图像输入) 🖼️
**目标**: 支持视觉语言模型 (VLM)

**任务清单**:
- [ ] 研究 HuggingFace VLM API
  - 支持的模型列表
  - 图像输入格式
- [ ] 扩展消息类型
  - 支持 `ImagePart`
  - Base64 编码支持
  - URL 引用支持
- [ ] 实现图像预处理
  - 格式转换
  - 大小限制
- [ ] 添加示例和文档
- [ ] 编写测试用例

**预计工作量**: 3-4 天  
**依赖**: 需要确认 HuggingFace API 支持情况

---

#### 4. 工具调用支持 (Function Calling) 🔧
**目标**: 支持模型调用外部工具

**任务清单**:
- [ ] 研究 HuggingFace Function Calling API
  - 支持的模型列表
  - 工具定义格式
- [ ] 实现工具定义转换
  - `model.Tool` → HuggingFace 格式
- [ ] 实现工具调用解析
  - 解析模型返回的工具调用
  - 转换为 `model.FunctionCall`
- [ ] 添加示例
  - 天气查询示例
  - 计算器示例
- [ ] 编写测试用例

**预计工作量**: 3-5 天  
**技术难点**: HuggingFace 的 Function Calling 支持可能不如 OpenAI 完善

---

#### 5. 性能优化 ⚡
**目标**: 提升响应速度和资源利用率

**任务清单**:
- [ ] HTTP 连接池优化
  - 调整连接数
  - 连接复用策略
- [ ] 流式响应优化
  - 减少内存分配
  - 优化 channel 缓冲
- [ ] 并发控制
  - 限流器实现
  - 请求队列管理
- [ ] 性能基准测试
  - Benchmark 测试
  - 压力测试
- [ ] 性能文档

**预计工作量**: 2-3 天

---

### 优先级 P2 (低优先级 - 可选增强)

#### 6. 缓存机制 💾
**目标**: 减少重复请求，降低成本

**任务清单**:
- [ ] 设计缓存策略
  - 基于请求内容的缓存 key
  - TTL 配置
- [ ] 实现缓存层
  - 内存缓存
  - Redis 缓存支持
- [ ] 缓存失效策略
- [ ] 添加配置选项
- [ ] 编写测试用例

**预计工作量**: 2-3 天

---

#### 7. 监控和指标 📈
**目标**: 提供运行时监控能力

**任务清单**:
- [ ] 定义关键指标
  - 请求延迟
  - Token 使用量
  - 错误率
- [ ] 集成 Prometheus
  - 暴露 metrics 端点
  - 定义指标类型
- [ ] 添加追踪支持
  - OpenTelemetry 集成
  - 分布式追踪
- [ ] 监控文档

**预计工作量**: 2-3 天

---

#### 8. 批量请求支持 📦
**目标**: 支持批量处理多个请求

**任务清单**:
- [ ] 研究 HuggingFace Batch API
- [ ] 实现批量请求接口
- [ ] 批量响应处理
- [ ] 添加示例和文档
- [ ] 编写测试用例

**预计工作量**: 2-3 天  
**依赖**: 需要确认 HuggingFace API 支持情况

---

## 🚫 不需要实现的功能

### ❌ tRPC 客户端支持
**原因**:
1. HuggingFace 提供的是 HTTP REST API，不是 tRPC 服务
2. 已实现完整的 HTTP 客户端调用
3. A2A 协议用于 Agent 间通信，不是模型调用
4. OpenAI 模型也未实现 tRPC 支持

**结论**: 当前的 HTTP 实现已经满足需求

---

## 📅 开发时间线

### 已完成 (2025-12-12)
- ✅ 基础架构搭建
- ✅ 核心功能实现
- ✅ 测试覆盖 (Mock + 集成)
- ✅ 文档和示例

### 已完成 (2025-12-17)
- ✅ Token Tailoring 实现
  - 自动计算 max input/output tokens
  - 支持自定义裁剪策略和参数
  - 完整测试覆盖

### 已完成 (2025-12-18)
- ✅ 多模态支持实现
  - 图像 URL 支持
  - Base64 编码图像支持
  - 多图像输入支持
  - 流式响应支持
  - 完整测试覆盖（10个测试用例）

### 第一阶段 (预计 1 周)
- 🎯 错误处理增强

### 第二阶段 (预计 2 周)
- 🎯 多模态支持
- 🎯 工具调用支持
- 🎯 性能优化

### 第三阶段 (可选)
- 🎯 缓存机制
- 🎯 监控和指标
- 🎯 批量请求支持

---

## 🎓 技术债务

### 当前已知问题
1. ~~**Token 计数缺失**~~: ✅ 已实现 Token Tailoring，支持自动计算和裁剪
2. **TODO 注释**: 代码中有 2 处 TODO 注释关于 tRPC 支持（已确认不需要实现，建议删除）
3. **错误类型**: 错误处理较为简单，需要细化错误类型

### 改进建议
1. 考虑添加请求/响应日志记录（可选开启）
2. 考虑添加请求超时的细粒度控制
3. 考虑支持自定义 User-Agent

---

## 📚 参考资料

### HuggingFace 官方文档
- [Inference API 文档](https://huggingface.co/docs/api-inference/index)
- [Text Generation API](https://huggingface.co/docs/api-inference/detailed_parameters#text-generation-task)
- [Streaming 支持](https://huggingface.co/docs/api-inference/detailed_parameters#streaming)

### 项目内部参考
- OpenAI 模型实现: `model/openai/`
- Model 接口定义: `model/model.go`
- A2A 协议文档: `docs/mkdocs/zh/a2a.md`

### 相关工具
- [tiktoken-go](https://github.com/pkoukk/tiktoken-go) - Token 计数库
- [go-openai](https://github.com/sashabaranov/go-openai) - OpenAI Go SDK (参考实现)

---

## 🤝 贡献指南

### 开发流程
1. 从 `feature/huggingface-model-integration` 分支创建新分支
2. 实现功能并编写测试
3. 确保所有测试通过: `go test ./model/huggingface/...`
4. 运行 linter: `golangci-lint run ./model/huggingface/...`
5. 更新文档
6. 提交 PR

### 代码规范
- 遵循 Go 官方编码规范
- 所有公开方法必须有文档注释
- 测试覆盖率不低于 80%
- 提交信息使用中文

### 测试要求
- 单元测试 (Mock): 必须
- 集成测试 (真实 API): 推荐
- 性能测试 (Benchmark): 可选

---

## 📞 联系方式

如有问题或建议，请：
1. 提交 Issue
2. 发起 Discussion
3. 联系维护者

---

**最后更新**: 2025-12-17  
**维护者**: @willieyin
