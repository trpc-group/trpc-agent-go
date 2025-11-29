# TDesign Chat + AG-UI Demo

## ✨ 特性

这是一个使用 **Vite + React + TDesign Chat** 构建的 AG-UI 客户端示例，展示了如何与 Go AG-UI 服务进行集成。

TDesign Chat 现已支持适配 AG-UI 协议：
- 完整适配16个标准AG-UI协议事件
- 原生 `TOOL_CALL_*` 事件处理和自定义工具组件注册机制
- 内置 `STATE_SNAPSHOT` 和 `STATE_DELTA` 支持
- 自动工具调用生命周期管理
- 支持消息回填
- 支持多种框架（vue/react/webcomponent），多端（web和微信小程序）
- 继续了解更多，[react版本](https://tdesign.tencent.com/react-chat/agui)，[vue版本](https://tdesign.tencent.com/chat/components/chatbot)

## 📋 环境要求

- Node.js >= 16
- Go >= 1.21
- pnpm 或 npm

## 🚀 快速开始

### 一键启动（推荐）

**终端 1：启动后端**

```bash
cd /Users/caolin/workspace/private/trpc-agent-go/examples/agui/server/default
go run main.go
```

服务将运行在 `http://127.0.0.1:8080/agui`

**终端 2：启动前端**

```bash
cd /Users/caolin/workspace/private/trpc-agent-go/examples/agui/client/tdesign-chat
pnpm install && pnpm dev
```

**访问应用**

打开浏览器访问：`http://localhost:3000`

**测试示例**

输入：`Calculate 2*(10+11)`

应该看到：
1. AI 解释计算思路
2. 计算器工具被调用
3. 显示计算过程和结果（42）

### 分步启动

如果你想分步执行，可以按照以下步骤：

**1. 启动 AG-UI 服务端**

```bash
cd ../../server/default
go run main.go
```

**2. 安装依赖**

```bash
pnpm install
# 或
npm install
```

**3. 启动开发服务器**

```bash
pnpm dev
# 或
npm run dev
```

**4. 打开浏览器**

访问 `http://localhost:3000`，尝试发送消息：

```
Calculate 2*(10+11), first explain the idea, then calculate, and finally give the conclusion.
```

## 🌍 环境变量

可以通过环境变量自定义 AG-UI 端点：

```bash
# 创建 .env 文件
cp .env.example .env

# 编辑 .env
VITE_AGUI_ENDPOINT=http://your-server:port/agui
```

默认值：`http://127.0.0.1:8080/agui`

## 📁 项目结构

```
tdesign-chat/
├── src/
│   ├── App.tsx           # 主应用组件（聊天逻辑 + 工具注册）
│   ├── main.tsx          # 应用入口
│   └── index.css         # 全局样式
├── index.html            # HTML 模板
├── package.json          # 依赖配置
├── tsconfig.json         # TypeScript 配置
├── vite.config.ts        # Vite 配置
└── README.md             # 本文件
```

## 🔧 核心实现

### 工具注册

使用 `useAgentToolcall` 注册计算器工具：

```tsx
useAgentToolcall([
  {
    name: 'calculator',
    description: '计算器工具',
    parameters: [
      { name: 'operation', type: 'string', required: true },
      { name: 'a', type: 'number', required: true },
      { name: 'b', type: 'number', required: true },
    ],
    component: CalculatorTool,  // 自定义 React 组件
  },
]);
```

### 聊天配置

配置聊天服务，发送正确的 AG-UI 协议消息：

```tsx
const { chatEngine, messages, status } = useChat({
  chatServiceConfig: {
    endpoint: 'http://127.0.0.1:8080/agui',
    protocol: 'agui',  // 启用 AG-UI 协议
    stream: true,
    onRequest: (params: ChatRequestParams) => {
      const runId = `run-${Date.now()}`;
      
      return {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          threadId: threadId,  // 会话ID（整个对话保持不变）
          runId: runId,        // 运行ID（每次请求唯一）
          messages: [
            {
              role: 'user',
              content: params.prompt,
            }
          ],
        }),
      };
    },
  },
});
```

**请求格式：**

后端期望的 JSON 结构：
```json
{
  "threadId": "session-1732777776000",
  "runId": "run-1732777776123",
  "messages": [
    {
      "role": "user",
      "content": "Calculate 2*(10+11)..."
    }
  ]
}
```

### 工具组件示例

```tsx
const CalculatorTool: React.FC<ToolcallComponentProps<Args, Result>> = ({
  status,   // 'pending' | 'executing' | 'complete' | 'error'
  args,     // 工具参数
  result,   // 工具执行结果
  error,    // 错误对象（如果失败）
}) => {
  return (
    <Card>
      {/* 根据 status 和数据渲染工具 UI */}
    </Card>
  );
};
```