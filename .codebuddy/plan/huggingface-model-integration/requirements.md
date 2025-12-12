# 需求文档：HuggingFace 模型集成

## 引言

本需求文档旨在为 tRPC-Agent-Go 框架的 `/model` 目录引入 HuggingFace 模型支持。该集成将参考现有的 OpenAI 模型实现模式，提供两种访问方式：
1. 使用 HuggingFace 原生 REST API（通过 HTTP 客户端）
2. 使用 tRPC 客户端调用（适用于公司内部 tRPC 服务封装的 HuggingFace 模型）

该功能将使开发者能够无缝地在 tRPC-Agent-Go 框架中使用 HuggingFace 托管的各类大语言模型，包括开源模型如 Llama、Mistral、Qwen 等，同时保持与现有 Model 接口的一致性。

参考资料：
- Agno HuggingFace 实现：https://github.com/agno-agi/agno/tree/main/libs/agno/agno/models/huggingface
- HuggingFace Inference API 文档
- 现有 OpenAI 模型实现：`/model/openai/`

---

## 需求

### 需求 1：核心 Model 接口实现

**用户故事：** 作为一名框架开发者，我希望创建一个符合 `model.Model` 接口的 HuggingFace 模型实现，以便用户能够像使用 OpenAI 模型一样使用 HuggingFace 模型。

#### 验收标准

1. WHEN 创建 HuggingFace 模型包时 THEN 系统 SHALL 在 `/model/huggingface/` 目录下创建完整的包结构
2. WHEN 实现 Model 接口时 THEN 系统 SHALL 实现 `GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)` 方法
3. WHEN 实现 Model 接口时 THEN 系统 SHALL 实现 `Info() Info` 方法返回模型基本信息
4. WHEN 处理请求时 THEN 系统 SHALL 支持流式（stream）和非流式两种响应模式
5. IF 发生系统级错误（如网络故障、参数无效）THEN 系统 SHALL 通过函数返回的 error 返回错误
6. IF 发生 API 级错误（如速率限制、内容过滤）THEN 系统 SHALL 通过 Response.Error 字段返回结构化错误

---

### 需求 2：HuggingFace 原生 API 支持

**用户故事：** 作为一名开发者，我希望通过 HuggingFace 原生 REST API 调用模型，以便直接使用 HuggingFace 托管的推理服务。

#### 验收标准

1. WHEN 初始化模型时 THEN 系统 SHALL 支持通过 API Token 进行身份认证
2. WHEN 配置 API Token 时 THEN 系统 SHALL 支持从环境变量 `HUGGINGFACE_API_TOKEN` 或 `HF_TOKEN` 读取
3. WHEN 配置 API Token 时 THEN 系统 SHALL 支持通过代码选项 `WithAPIToken()` 显式设置
4. WHEN 发送请求时 THEN 系统 SHALL 使用 HuggingFace Inference API 端点（默认：`https://api-inference.huggingface.co/models/{model_id}`）
5. WHEN 配置 BaseURL 时 THEN 系统 SHALL 支持自定义 API 端点（用于私有部署或 Inference Endpoints）
6. WHEN 构建 HTTP 请求时 THEN 系统 SHALL 正确转换 `model.Request` 到 HuggingFace API 格式
7. WHEN 处理 HTTP 响应时 THEN 系统 SHALL 正确解析 HuggingFace API 响应并转换为 `model.Response`
8. WHEN 启用流式输出时 THEN 系统 SHALL 支持 Server-Sent Events (SSE) 流式响应
9. WHEN 处理流式响应时 THEN 系统 SHALL 逐块解析并通过 channel 发送 Response 对象
10. IF HTTP 请求失败 THEN 系统 SHALL 返回包含详细错误信息的系统级错误

---

### 需求 3：tRPC 客户端支持

**用户故事：** 作为一名公司内部开发者，我希望通过 tRPC 客户端调用 HuggingFace 模型服务，以便利用公司内部的服务治理能力（如服务发现、负载均衡、监控等）。

#### 验收标准

1. WHEN 初始化模型时 THEN 系统 SHALL 支持通过选项 `WithTRPCClient()` 注入 tRPC 客户端
2. IF 提供了 tRPC 客户端 THEN 系统 SHALL 优先使用 tRPC 客户端而非 HTTP 客户端
3. WHEN 使用 tRPC 客户端时 THEN 系统 SHALL 调用 tRPC 服务的对应方法（如 `GenerateContent`）
4. WHEN 构建 tRPC 请求时 THEN 系统 SHALL 正确转换 `model.Request` 到 tRPC 协议格式
5. WHEN 处理 tRPC 响应时 THEN 系统 SHALL 正确解析 tRPC 响应并转换为 `model.Response`
6. WHEN 使用 tRPC 流式调用时 THEN 系统 SHALL 支持 tRPC 的流式 RPC 机制
7. IF tRPC 调用失败 THEN 系统 SHALL 返回包含 tRPC 错误码和错误信息的系统级错误
8. WHEN 配置 tRPC 客户端时 THEN 系统 SHALL 支持传递 tRPC 客户端配置选项（如超时、重试策略等）

---

### 需求 4：配置选项和灵活性

**用户故事：** 作为一名开发者，我希望有丰富的配置选项来定制 HuggingFace 模型的行为，以便满足不同场景的需求。

#### 验收标准

1. WHEN 创建模型实例时 THEN 系统 SHALL 支持通过 `New(modelID string, opts ...Option)` 函数创建
2. WHEN 配置模型时 THEN 系统 SHALL 支持以下选项：
   - `WithAPIToken(token string)` - 设置 API Token
   - `WithBaseURL(url string)` - 设置自定义 API 端点
   - `WithTRPCClient(client interface{})` - 注入 tRPC 客户端
   - `WithHTTPClientOptions(...HTTPClientOption)` - 配置 HTTP 客户端（超时、重试等）
   - `WithChannelBufferSize(size int)` - 设置响应 channel 缓冲区大小
   - `WithExtraFields(fields map[string]any)` - 添加额外的请求字段
   - `WithHeaders(headers map[string]string)` - 添加自定义 HTTP 头
3. WHEN 未显式配置 API Token 时 THEN 系统 SHALL 尝试从环境变量读取
4. WHEN 未显式配置 BaseURL 时 THEN 系统 SHALL 使用默认的 HuggingFace Inference API 端点
5. IF 既未提供 API Token 也未提供 tRPC 客户端 THEN 系统 SHALL 在初始化时返回配置错误

---

### 需求 5：请求和响应转换

**用户故事：** 作为一名框架维护者，我希望正确地在框架的统一 Request/Response 格式与 HuggingFace API 格式之间进行转换，以便保证数据的准确传递。

#### 验收标准

1. WHEN 转换 Request 时 THEN 系统 SHALL 正确映射以下字段：
   - `Messages` → HuggingFace 的 `inputs` 或 `messages` 格式
   - `GenerationConfig.MaxTokens` → `max_new_tokens` 或 `max_tokens`
   - `GenerationConfig.Temperature` → `temperature`
   - `GenerationConfig.TopP` → `top_p`
   - `GenerationConfig.TopK` → `top_k`
   - `GenerationConfig.Stream` → `stream`
   - `GenerationConfig.StopSequences` → `stop_sequences` 或 `stop`
2. WHEN 转换 Response 时 THEN 系统 SHALL 正确映射以下字段：
   - HuggingFace 的生成文本 → `Response.Choices[].Message.Content`
   - Token 使用统计 → `Response.Usage`
   - 完成原因 → `Response.Choices[].FinishReason`
3. WHEN 处理多模态内容时 THEN 系统 SHALL 支持文本和图像输入（如果模型支持）
4. WHEN 处理工具调用时 THEN 系统 SHALL 支持 HuggingFace 的函数调用格式（如果模型支持）
5. IF HuggingFace API 返回的格式与预期不符 THEN 系统 SHALL 记录警告日志并尽可能解析可用字段

---

### 需求 6：错误处理和日志

**用户故事：** 作为一名开发者，我希望有清晰的错误处理和日志记录，以便快速定位和解决问题。

#### 验收标准

1. WHEN 发生 HTTP 4xx 错误时 THEN 系统 SHALL 通过 Response.Error 返回 API 级错误，包含错误消息和错误类型
2. WHEN 发生 HTTP 5xx 错误时 THEN 系统 SHALL 通过 Response.Error 返回 API 级错误，标记为服务端错误
3. WHEN 发生网络错误时 THEN 系统 SHALL 通过函数返回值返回系统级错误
4. WHEN 发生 JSON 解析错误时 THEN 系统 SHALL 通过函数返回值返回系统级错误
5. WHEN 处理请求和响应时 THEN 系统 SHALL 使用 `trpc.group/trpc-go/trpc-agent-go/log` 记录关键操作
6. WHEN 发生错误时 THEN 系统 SHALL 记录详细的错误日志，包含请求上下文信息
7. IF 启用了调试模式 THEN 系统 SHALL 记录完整的请求和响应内容（脱敏后）

---

### 需求 7：回调机制

**用户故事：** 作为一名开发者，我希望能够注册回调函数来监控和处理请求/响应的生命周期事件，以便实现自定义的日志、监控或数据处理逻辑。

#### 验收标准

1. WHEN 配置模型时 THEN 系统 SHALL 支持以下回调选项：
   - `WithRequestCallback(func(ctx, request))` - 请求发送前回调
   - `WithResponseCallback(func(ctx, request, response))` - 非流式响应接收后回调
   - `WithChunkCallback(func(ctx, request, chunk))` - 流式响应每个块的回调
   - `WithStreamCompleteCallback(func(ctx, request, err))` - 流式响应完成回调
2. WHEN 发送请求前 THEN 系统 SHALL 调用 RequestCallback（如果已配置）
3. WHEN 接收到完整响应时 THEN 系统 SHALL 调用 ResponseCallback（如果已配置）
4. WHEN 接收到流式响应块时 THEN 系统 SHALL 调用 ChunkCallback（如果已配置）
5. WHEN 流式响应完成或出错时 THEN 系统 SHALL 调用 StreamCompleteCallback（如果已配置）
6. IF 回调函数执行出错 THEN 系统 SHALL 记录错误日志但不中断主流程

---

### 需求 8：测试覆盖

**用户故事：** 作为一名框架维护者，我希望有完善的单元测试和集成测试，以便确保代码质量和功能正确性。

#### 验收标准

1. WHEN 编写测试时 THEN 系统 SHALL 为核心功能提供单元测试，包括：
   - 模型初始化和配置
   - Request/Response 转换逻辑
   - HTTP 客户端调用（使用 mock server）
   - 流式和非流式响应处理
   - 错误处理逻辑
2. WHEN 编写测试时 THEN 系统 SHALL 为 tRPC 客户端集成提供单元测试（使用 mock client）
3. WHEN 测试覆盖率统计时 THEN 系统 SHALL 确保核心代码的测试覆盖率达到 80% 以上
4. WHEN 编写集成测试时 THEN 系统 SHALL 提供至少一个端到端的示例测试（可选，需要真实 API Token）
5. IF 测试失败 THEN 系统 SHALL 提供清晰的失败原因和调试信息

---

### 需求 9：文档和示例

**用户故事：** 作为一名用户，我希望有清晰的文档和示例代码，以便快速上手使用 HuggingFace 模型。

#### 验收标准

1. WHEN 提供文档时 THEN 系统 SHALL 在 `/docs/` 目录下创建 HuggingFace 模型使用文档
2. WHEN 编写文档时 THEN 系统 SHALL 包含以下内容：
   - 快速开始指南
   - 配置选项说明
   - HTTP API 和 tRPC 客户端两种使用方式的示例
   - 常见问题解答
3. WHEN 提供示例时 THEN 系统 SHALL 在 `/examples/` 目录下创建至少 2 个示例：
   - 使用 HTTP API 的基本示例
   - 使用 tRPC 客户端的示例（如果适用）
4. WHEN 编写代码注释时 THEN 系统 SHALL 为所有公开的类型、函数和方法提供 GoDoc 格式的注释
5. WHEN 更新 README 时 THEN 系统 SHALL 在项目主 README 中添加 HuggingFace 模型的简要说明

---

### 需求 10：兼容性和扩展性

**用户故事：** 作为一名框架维护者，我希望实现具有良好的兼容性和扩展性，以便未来能够轻松支持更多 HuggingFace 的功能和变体。

#### 验收标准

1. WHEN 设计代码结构时 THEN 系统 SHALL 遵循现有 OpenAI 模型的设计模式和代码风格
2. WHEN 实现功能时 THEN 系统 SHALL 确保与框架其他模块（如 Agent、Tool、Runner）的兼容性
3. WHEN 处理不同的 HuggingFace 模型时 THEN 系统 SHALL 支持通过 Variant 机制处理特定模型的差异（类似 OpenAI 的 Variant）
4. WHEN 扩展功能时 THEN 系统 SHALL 预留扩展点以支持未来的新特性（如 Inference Endpoints、专用硬件加速等）
5. IF HuggingFace API 发生变化 THEN 系统 SHALL 能够通过配置选项或小幅修改适配新版本
6. WHEN 集成到现有项目时 THEN 系统 SHALL 不引入破坏性变更，保持向后兼容

---

## 技术约束

1. **编程语言**：Go 1.21+
2. **依赖管理**：使用 Go Modules
3. **HTTP 客户端**：复用框架现有的 `model.DefaultNewHTTPClient`
4. **日志库**：使用 `trpc.group/trpc-go/trpc-agent-go/log`
5. **tRPC 框架**：兼容 tRPC-Go 框架的客户端接口
6. **代码风格**：遵循 Go 官方代码规范和项目现有风格
7. **测试框架**：使用 Go 标准库 `testing` 和 `httptest`

---

## 非功能性需求

1. **性能**：流式响应的延迟应控制在 100ms 以内（网络延迟除外）
2. **可靠性**：支持请求重试和超时控制
3. **可维护性**：代码结构清晰，注释完善，易于理解和维护
4. **安全性**：API Token 不应出现在日志中，敏感信息需脱敏处理
5. **可观测性**：关键操作应有日志记录，支持分布式追踪（如果框架支持）

---

## 参考实现

- **Agno HuggingFace**：https://github.com/agno-agi/agno/tree/main/libs/agno/agno/models/huggingface
- **项目内 OpenAI 实现**：`/model/openai/`
- **HuggingFace Inference API 文档**：https://huggingface.co/docs/api-inference/
- **tRPC-Go 文档**：公司内部 tRPC 框架文档
