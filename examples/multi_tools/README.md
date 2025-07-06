# 多工具智能助手示例

这个示例展示了如何创建一个集成多种工具的智能助手，包括计算器、时间工具、文本处理工具、文件操作工具和网络搜索工具。

## 功能特性

### 🧮 计算器工具 (calculator)
- **基本运算**: 加法、减法、乘法、除法
- **科学函数**: sqrt(平方根), sin(正弦), cos(余弦), abs(绝对值)
- **数学常数**: pi(圆周率), e(自然对数底数)
- **示例**: 
  - `计算 123 + 456 * 789`
  - `计算 sqrt(16)`
  - `计算 sin(30*pi/180)`

### ⏰ 时间工具 (time_tool)
- **当前时间**: 获取当前日期和时间
- **日期信息**: 获取当前日期
- **星期信息**: 获取今天是星期几
- **时间戳**: 获取Unix时间戳
- **示例**:
  - `现在是几点？`
  - `今天是星期几？`
  - `获取当前时间戳`

### 📝 文本处理工具 (text_tool)
- **大小写转换**: 转换为大写或小写
- **字符统计**: 计算文本长度和单词数
- **文本反转**: 反转文本内容
- **示例**:
  - `把 'Hello World' 转换为大写`
  - `统计 'Hello World' 的字符数`
  - `反转文本 'Hello World'`

### 📁 文件操作工具 (file_tool)
- **读取文件**: 读取文件内容
- **写入文件**: 创建或写入文件
- **列出目录**: 查看目录内容
- **检查存在**: 检查文件是否存在
- **示例**:
  - `读取 README.md 文件`
  - `在当前目录创建一个测试文件`
  - `列出当前目录的所有文件`

### 🔍 网络搜索工具 (duckduckgo_search)
- **实体搜索**: 搜索人物、公司、地点等信息
- **定义查询**: 查找概念和术语的定义
- **历史信息**: 查找历史事实和数据
- **示例**:
  - `搜索史蒂夫乔布斯的信息`
  - `查找特斯拉公司的资料`
  - `什么是光合作用？`

## 使用方法

### 1. 准备环境
确保已经设置好相关的环境变量和依赖：
```bash
# 设置API密钥等环境变量
export OPENAI_API_KEY="your-api-key"
```

### 2. 运行示例
```bash
cd src/git.code.oa.com/trpc-go/trpc-agent-go/examples/multi_tools
go run main.go
```

### 3. 指定模型
```bash
go run main.go -model="gpt-4"
```

### 4. 交互式使用
程序启动后，您可以：
- 输入各种问题和请求
- 观察助手如何选择和使用不同的工具
- 输入 `exit` 退出程序

## 示例对话

```
🚀 多工具智能助手演示
模型: deepseek-chat
输入 'exit' 结束对话
可用工具: calculator, time_tool, text_tool, file_tool, duckduckgo_search
============================================================
✅ 多工具智能助手已就绪! 会话ID: multi-tool-session-1703123456

💡 试试问这些问题：
   【计算器】计算 123 + 456 * 789
   【计算器】计算圆周率的平方根
   【时间】现在是几点？
   【时间】今天是星期几？
   【文本】把 'Hello World' 转换为大写
   【文本】统计 'Hello World' 的字符数
   【文件】读取 README.md 文件
   【文件】在当前目录创建一个测试文件
   【搜索】搜索史蒂夫乔布斯的信息
   【搜索】查找特斯拉公司的资料

👤 jessemjchen: 计算 100 + 200 * 3
🔧 工具调用:
   🧮 calculator (ID: call_abc123)
     参数: {"expression":"100 + 200 * 3"}

⚡ 执行中...
✅ 工具结果 (ID: call_abc123): {"expression":"100 + 200 * 3","result":700,"message":"计算结果: 700"}

🤖 助手: 根据数学运算规则，先计算乘法再计算加法：
100 + 200 * 3 = 100 + 600 = 700

计算结果是 700。
```

## 工具设计原则

### 1. 安全性
- 文件操作工具限制访问当前目录及其子目录
- 防止路径遍历攻击
- 限制文件读取内容长度

### 2. 用户体验
- 中文界面和提示信息
- 清晰的工具调用可视化
- 丰富的使用示例和帮助信息

### 3. 扩展性
- 模块化的工具设计
- 统一的工具接口
- 易于添加新工具

## 扩展新工具

要添加新的工具，请遵循以下步骤：

1. **定义请求和响应结构**
```go
type myToolRequest struct {
    Input string `json:"input" jsonschema:"description=输入描述"`
}

type myToolResponse struct {
    Output string `json:"output"`
    Status string `json:"status"`
}
```

2. **实现工具函数**
```go
func myToolHandler(req myToolRequest) myToolResponse {
    // 实现工具逻辑
    return myToolResponse{
        Output: "处理结果",
        Status: "成功",
    }
}
```

3. **创建工具实例**
```go
func createMyTool() tool.CallableTool {
    return function.NewFunctionTool(
        myToolHandler,
        function.WithName("my_tool"),
        function.WithDescription("工具描述"),
    )
}
```

4. **注册工具**
```go
tools := []tool.Tool{
    createMyTool(),
    // 其他工具...
}
```

## 注意事项

1. **API限制**: 某些工具可能需要网络访问或API密钥
2. **性能考虑**: 大文件操作可能影响性能
3. **错误处理**: 工具调用失败时会返回错误信息
4. **安全性**: 文件操作受到路径限制保护

## 技术架构

- **框架**: trpc-agent-go
- **模型**: 支持OpenAI兼容的各种模型
- **工具系统**: 基于function calling的工具调用
- **流式处理**: 支持流式响应和实时交互

## 许可证

本示例遵循项目的许可证条款。 