# 实施计划：HuggingFace 模型集成

## 任务清单

- [ ] 1. 创建 HuggingFace 模型包结构和核心类型定义
   - 在 `/model/huggingface/` 目录下创建包结构
   - 定义 `HuggingFace` 结构体，包含配置字段（apiToken、baseURL、httpClient、trpcClient 等）
   - 定义 `Option` 函数类型和配置选项函数（WithAPIToken、WithBaseURL、WithTRPCClient 等）
   - 实现 `New(modelID string, opts ...Option)` 构造函数，支持环境变量读取和配置验证
   - _需求：1.1, 1.2, 4.1, 4.2, 4.3, 4.4, 4.5_

- [ ] 2. 实现 Model 接口的核心方法
   - 实现 `Info() Info` 方法，返回模型 ID、提供商等基本信息
   - 实现 `GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)` 方法框架
   - 在 GenerateContent 中添加客户端类型判断逻辑（HTTP vs tRPC）
   - 创建响应 channel 并设置缓冲区大小
   - _需求：1.2, 1.3, 1.4_

- [ ] 3. 实现请求和响应格式转换逻辑
   - 创建 `convertRequest(*model.Request) (map[string]interface{}, error)` 函数
   - 实现 Messages 到 HuggingFace inputs/messages 格式的转换
   - 实现 GenerationConfig 参数映射（MaxTokens、Temperature、TopP、TopK、Stream、StopSequences）
   - 创建 `convertResponse(hfResponse) (*model.Response, error)` 函数
   - 实现 HuggingFace 响应到框架 Response 格式的转换（Choices、Usage、FinishReason）
   - _需求：5.1, 5.2, 5.5_

- [ ] 4. 实现 HTTP 客户端调用逻辑（非流式）
   - 创建 `generateWithHTTP(ctx, request) (<-chan *Response, error)` 方法
   - 构建 HTTP 请求（URL、Headers、Body）
   - 发送 HTTP POST 请求到 HuggingFace Inference API
   - 解析 JSON 响应并转换为 model.Response
   - 实现错误处理（4xx、5xx、网络错误、JSON 解析错误）
   - 通过 channel 发送响应并关闭 channel
   - _需求：2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.10, 6.1, 6.2, 6.3, 6.4_

- [ ] 5. 实现 HTTP 客户端流式调用逻辑
   - 在 `generateWithHTTP` 中添加流式请求处理分支
   - 实现 Server-Sent Events (SSE) 流式响应解析
   - 逐块读取 SSE 数据并解析为 Response 对象
   - 通过 channel 逐个发送 Response 块
   - 处理流式响应的错误和完成信号
   - _需求：2.8, 2.9, 1.4_

- [ ] 6. 实现 tRPC 客户端调用逻辑
   - 创建 `generateWithTRPC(ctx, request) (<-chan *Response, error)` 方法
   - 定义 tRPC 客户端接口（如果需要）
   - 将 model.Request 转换为 tRPC 协议格式
   - 调用 tRPC 客户端的 GenerateContent 方法
   - 处理 tRPC 流式和非流式响应
   - 实现 tRPC 错误处理和错误码转换
   - _需求：3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8_

- [ ] 7. 实现回调机制
   - 在配置选项中添加回调函数字段（requestCallback、responseCallback、chunkCallback、streamCompleteCallback）
   - 实现 WithRequestCallback、WithResponseCallback、WithChunkCallback、WithStreamCompleteCallback 选项函数
   - 在请求发送前调用 RequestCallback
   - 在非流式响应接收后调用 ResponseCallback
   - 在流式响应每个块调用 ChunkCallback
   - 在流式响应完成时调用 StreamCompleteCallback
   - 添加回调错误处理和日志记录
   - _需求：7.1, 7.2, 7.3, 7.4, 7.5, 7.6_

- [ ] 8. 实现日志记录和错误处理
   - 在关键操作点添加日志记录（使用 trpc.group/trpc-go/trpc-agent-go/log）
   - 记录请求发送、响应接收、错误发生等事件
   - 实现敏感信息脱敏逻辑（API Token、用户数据）
   - 添加调试模式支持，记录完整请求/响应内容
   - 统一错误处理格式，区分系统级错误和 API 级错误
   - _需求：6.5, 6.6, 6.7_

- [ ] 9. 编写单元测试
   - 创建 `huggingface_test.go` 文件
   - 编写模型初始化和配置测试（包括环境变量读取、选项设置、配置验证）
   - 编写 Request/Response 转换逻辑测试
   - 使用 httptest 创建 mock HTTP server，测试 HTTP 客户端调用
   - 测试流式和非流式响应处理
   - 测试错误处理逻辑（4xx、5xx、网络错误、JSON 解析错误）
   - 创建 mock tRPC 客户端，测试 tRPC 集成
   - 测试回调机制
   - 确保测试覆盖率达到 80% 以上
   - _需求：8.1, 8.2, 8.3, 8.5_

- [ ] 10. 创建文档和示例代码
   - 在 `/docs/` 目录下创建 `huggingface.md` 使用文档
   - 编写快速开始指南、配置选项说明、常见问题解答
   - 在 `/examples/huggingface/` 目录下创建示例代码
   - 创建 `basic_http_example.go` - 使用 HTTP API 的基本示例
   - 创建 `trpc_client_example.go` - 使用 tRPC 客户端的示例
   - 创建 `streaming_example.go` - 流式响应示例
   - 为所有公开的类型、函数和方法添加 GoDoc 格式注释
   - 更新项目主 README.md，添加 HuggingFace 模型的简要说明
   - _需求：9.1, 9.2, 9.3, 9.4, 9.5_

---

## 实施说明

### 开发顺序
1. 首先完成任务 1-3（基础结构和转换逻辑）
2. 然后实现任务 4-5（HTTP 客户端）
3. 接着实现任务 6（tRPC 客户端）
4. 最后完成任务 7-10（回调、日志、测试、文档）

### 关键依赖
- 任务 2 依赖任务 1
- 任务 4、5、6 依赖任务 2 和 3
- 任务 7、8 可以与任务 4-6 并行开发
- 任务 9 依赖任务 1-8
- 任务 10 可以在任务 1-8 完成后开始

### 参考资料
- 现有 OpenAI 模型实现：`/model/openai/openai.go`
- Agno HuggingFace 实现：https://github.com/agno-agi/agno/tree/main/libs/agno/agno/models/huggingface
- HuggingFace Inference API 文档：https://huggingface.co/docs/api-inference/

### 注意事项
1. 严格遵循现有 OpenAI 模型的代码风格和设计模式
2. 确保所有公开 API 都有完整的 GoDoc 注释
3. 敏感信息（API Token）必须脱敏处理
4. 测试覆盖率目标：80% 以上
5. 保持向后兼容，不引入破坏性变更
