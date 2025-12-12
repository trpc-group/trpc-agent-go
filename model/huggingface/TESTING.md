# HuggingFace 模型集成测试指南

本目录包含两种类型的测试：

## 📋 测试类型

### 1. 单元测试（Mock 测试）
位于 `huggingface_test.go`，使用 mock HTTP 服务器，无需真实 API key。

**运行方式：**
```bash
go test -v ./model/huggingface/...
```

### 2. 集成测试（真实 API 测试）
位于 `integration_test.go`，调用真实的 HuggingFace API，需要有效的 API key。

## 🚀 运行集成测试

### 前置条件

1. **获取 HuggingFace API Key**
   - 访问 https://huggingface.co/settings/tokens
   - 创建一个新的 Access Token（Read 权限即可）

2. **设置环境变量**
   ```bash
   # 必需：设置你的 API Key
   export HUGGINGFACE_API_KEY=your_api_key_here
   
   # 必需：启用集成测试
   export RUN_INTEGRATION_TESTS=true
   
   # 可选：指定测试模型（默认使用 microsoft/DialoGPT-small）
   export HUGGINGFACE_TEST_MODEL=mistralai/Mistral-7B-Instruct-v0.2
   ```

### 运行所有集成测试

```bash
# 运行所有集成测试
go test -v -run TestIntegration ./model/huggingface/...

# 运行特定的集成测试
go test -v -run TestIntegration_RealAPI_NonStreaming ./model/huggingface/...
go test -v -run TestIntegration_RealAPI_Streaming ./model/huggingface/...
go test -v -run TestIntegration_RealAPI_WithCallbacks ./model/huggingface/...
```

### 一键运行脚本

创建一个 shell 脚本 `run_integration_tests.sh`：

```bash
#!/bin/bash

# 检查 API Key
if [ -z "$HUGGINGFACE_API_KEY" ]; then
    echo "❌ Error: HUGGINGFACE_API_KEY is not set"
    echo "Please set it with: export HUGGINGFACE_API_KEY=your_api_key"
    exit 1
fi

echo "🚀 Running HuggingFace integration tests..."
echo "📝 Using API Key: ${HUGGINGFACE_API_KEY:0:10}..."

# 启用集成测试并运行
RUN_INTEGRATION_TESTS=true go test -v -run TestIntegration ./model/huggingface/...

echo "✅ Integration tests completed!"
```

使用方式：
```bash
chmod +x run_integration_tests.sh
export HUGGINGFACE_API_KEY=your_api_key
./run_integration_tests.sh
```

## 📊 集成测试覆盖

### TestIntegration_RealAPI_NonStreaming
- ✅ 测试非流式响应
- ✅ 验证响应内容和格式
- ✅ 验证 Token 使用统计
- ✅ 超时处理

### TestIntegration_RealAPI_Streaming
- ✅ 测试流式响应
- ✅ 验证多个 chunk 的接收
- ✅ 验证内容累积
- ✅ 记录每个 chunk 的内容

### TestIntegration_RealAPI_WithCallbacks
- ✅ 测试请求回调
- ✅ 测试 chunk 回调
- ✅ 测试流式完成回调
- ✅ 验证回调执行顺序

## 🎯 推荐测试模型

以下模型适合用于集成测试（响应快、免费）：

1. **microsoft/DialoGPT-small** (默认)
   - 轻量级对话模型
   - 响应速度快
   - 适合快速测试

2. **gpt2**
   - 经典文本生成模型
   - 稳定可靠

3. **distilgpt2**
   - GPT-2 的精简版
   - 更快的响应速度

使用自定义模型：
```bash
export HUGGINGFACE_TEST_MODEL=gpt2
```

## ⚠️ 注意事项

1. **API 限制**
   - 免费 API 有速率限制
   - 某些模型可能需要付费或特殊权限
   - 建议使用小型模型进行测试

2. **超时设置**
   - 集成测试设置了 60 秒超时
   - 某些大型模型可能需要更长时间

3. **错误处理**
   - 如果模型不可用，测试会记录错误但不会失败
   - 这是正常的，因为 HuggingFace 的模型可用性会变化

4. **CI/CD 集成**
   - 在 CI 环境中，可以通过环境变量控制是否运行集成测试
   - 建议只在特定的 CI job 中运行集成测试

## 🔍 调试技巧

### 查看详细日志
```bash
go test -v -run TestIntegration ./model/huggingface/... 2>&1 | tee test.log
```

### 只运行单个测试
```bash
go test -v -run TestIntegration_RealAPI_NonStreaming ./model/huggingface/...
```

### 使用不同的超时时间
修改测试代码中的 `WithTimeout` 参数：
```go
WithTimeout(60*time.Second)  // 增加到 60 秒
```

## 📈 测试结果示例

成功的测试输出：
```
=== RUN   TestIntegration_RealAPI_NonStreaming
    integration_test.go:35: Running real HuggingFace API integration test (non-streaming)...
    integration_test.go:58: Sending request to HuggingFace API...
    integration_test.go:88: ✅ Received response from real API:
    integration_test.go:89:    Model: microsoft/DialoGPT-small
    integration_test.go:90:    Content: Hello! I'm doing great, thanks for asking!
    integration_test.go:94:    Token usage - Prompt: 8, Completion: 12, Total: 20
--- PASS: TestIntegration_RealAPI_NonStreaming (2.34s)
```

## 🤝 贡献指南

添加新的集成测试时：
1. 使用 `RUN_INTEGRATION_TESTS` 环境变量控制
2. 使用 `t.Skip()` 在没有 API key 时跳过
3. 添加详细的日志输出
4. 处理可能的 API 错误（不要让测试失败）
5. 更新本文档
