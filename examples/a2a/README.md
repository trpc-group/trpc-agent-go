# A2A (Agent-to-Agent) 示例

这是 trpc-agent-go 的 A2A 协议示例，展示了如何创建、部署和交互多个 AI 代理。

## 项目结构

```
examples/a2a/
├── agents/                    # AI 代理服务器
│   ├── entrance/             # 入口代理 (端口 8081)
│   │   └── entrance_agent.go
│   ├── codecheck/            # 代码检查代理 (端口 8082)  
│   │   ├── codecc_agent.go
│   │   ├── codecc_tool.go
│   │   └── spec.txt
│   └── agent_utils.go        # 代理工具函数
├── client/                   # A2A 交互式客户端
│   └── client.go
├── registry/                 # 代理注册服务
│   └── registry.go
├── README.md                 # 本文件
└── start.sh                  # 快速启动脚本
```

## 快速开始

### 1. 环境配置

首先设置必要的环境变量：

```bash
# OpenAI API 配置 (必需)
export OPENAI_API_KEY="your-openai-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # 可选，默认值
export OPENAI_MODEL="gpt-4o-mini"                   # 可选，默认值

# 或者使用其他兼容的 API 服务
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_MODEL="deepseek-chat"
```

### 2. 一键启动服务以及客户端

```bash
# 使用提供的启动脚本
chmod +x start.sh
./start.sh
```

## 手动启动

### 1. 启动代理服务器

在不同的终端窗口中启动代理：

```bash
# 注意启动的前后顺序
# 终端 1: 启动 CodeCheck Agent
cd examples/a2a/agents/codecheck
./codecc_agent

# 终端 2: 启动 Entrance Agent
cd examples/a2a/agents/entrance
./entrance_agent

```

### 2. 使用客户端连接

```bash
# 终端 3: 连接到入口代理
cd examples/a2a/client
./client -url http://localhost:8081

# 或连接到代码检查代理
./client -url http://localhost:8082
```

## 代理说明

### 入口 Agent (Entrance Agent)
- **端口**: 8081  
- **功能**: 作为系统入口，可以调用其他代理
- **URL**: http://localhost:8081
- **Agent Card**: http://localhost:8081/.well-known/agent.json

### 代码检查 Agent (CodeCheck Agent)
- **端口**: 8082
- **功能**: 分析 Go 代码质量，检查是否符合 Go 语言标准
- **URL**: http://localhost:8082  
- **Agent Card**: http://localhost:8082/.well-known/agent.json



## 使用示例

### 与入口代理对话

```bash
$ ./client -url http://localhost:8081
🚀 A2A Interactive Client
Agent URL: http://localhost:8081
Type 'exit' to quit
==================================================
🔗 Connecting to agent...
✅ Connected to agent: EntranceAgent
📝 Description: A entrance agent, it will delegate the task to the sub-agent by a2a protocol, or try to solve the task by itself
🏷️  Version: 1.0.0
🛠️  Skills:
   • non_streaming_CodeCheckAgent: Send non-streaming message to CodeCheckAgent agent: A agent that check code quality by Go Language Standard

💬 Start chatting (type 'exit' to quit):

👤 You: 查询golang代码规范
📤 Sending message to agent...
🤖 Agent: 以下是Golang代码规范的核心内容：

### 1.1 【必须】格式化
- 所有代码都必须使用 `gofmt` 工具进行格式化，以确保代码风格的一致性。

### 1.2 【推荐】换行
- 建议一行代码不要超过 `120` 列。如果超过，应使用合理的换行方法。
- 例外场景包括：
  - 函数签名（可能需要重新考虑是否传递了过多参数）。
  - 长字符串文字（如果包含换行符 `\n`，建议使用原始字符串字面量 `` `raw string literal` ``）。
  - `import` 模块语句。
  - 工具生成的代码。
  - `struct tag`。

如果需要更详细的规范或其他部分的内容，可以进一步查询。

conversation finished ctx id: ctx-ef5ee51e-1b44-42ea-832d-1016b1d09fe5
👤 You: exit
👋 Goodbye!
```


## 使用 A2A Inspector 访问 A2A 服务 (可选)

A2A Inspector 是一个用于监控和调试 A2A 通信的 Web 界面工具。

### 1. 启动 A2A Inspector

```bash
# 使用 Docker 运行 A2A Inspector
sudo docker run -d -p 8080:8080 a2a-inspector   


### 2. 访问 Inspector 界面

打开浏览器访问：http://localhost:8080

### 3. 配置代理监控

在网页中与 Agent 聊天

```

## 高级配置

### 自定义 HOST

```bash
# 启动代理到自定义端口
./entrance_agent -host 0.0.0.0
./codecc_agent -host 0.0.0.0
```

### 模型配置

```bash
# 使用不同的模型
export OPENAI_MODEL="gpt-4"
export OPENAI_MODEL="claude-3-sonnet"
export OPENAI_MODEL="deepseek-chat"
```


## 故障排除

### 常见问题

1. **连接失败**
   ```bash
   # 检查代理是否运行
   curl http://localhost:8081/.well-known/agent.json
   curl http://localhost:8082/.well-known/agent.json
   ```

2. **API Key 错误**
   ```bash
   # 验证环境变量设置
   echo $OPENAI_API_KEY
   echo $OPENAI_BASE_URL
   ```

3. **端口占用**
   ```bash
   # 检查端口占用
   lsof -i :8081
   lsof -i :8082
   ```

## 更多信息

- [trpc-agent-go 文档](https://github.com/trpc-group/trpc-agent-go)
- [A2A 协议规范](https://a2a-spec.org/)
- [OpenAI API 文档](https://platform.openai.com/docs)
