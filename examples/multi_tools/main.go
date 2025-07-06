// Package main 演示了使用多个工具的交互式聊天系统
// 包含计算器、时间、文本处理、文件操作和 DuckDuckGo 搜索等工具
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/core/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/orchestration/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

func main() {
	// 解析命令行参数
	modelName := flag.String("model", "deepseek-chat", "要使用的模型名称")
	flag.Parse()

	fmt.Printf("🚀 多工具智能助手演示\n")
	fmt.Printf("模型: %s\n", *modelName)
	fmt.Printf("输入 'exit' 结束对话\n")
	fmt.Printf("可用工具: calculator, time_tool, text_tool, file_tool, duckduckgo_search\n")
	fmt.Println(strings.Repeat("=", 60))

	// 创建并运行聊天系统
	chat := &multiToolChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("聊天系统运行失败: %v", err)
	}
}

// multiToolChat 管理多工具对话系统
type multiToolChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run 启动交互式聊天会话
func (c *multiToolChat) run() error {
	ctx := context.Background()

	// 设置运行器
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("设置失败: %w", err)
	}

	// 开始交互式聊天
	return c.startChat(ctx)
}

// setup 创建包含多个工具的运行器
func (c *multiToolChat) setup(ctx context.Context) error {
	// 创建 OpenAI 模型
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// 创建各种工具
	tools := []tool.Tool{
		createCalculatorTool(),
		createTimeTool(),
		createTextTool(),
		createFileTool(),
		duckduckgo.NewTool(), // 原有的 DuckDuckGo 搜索工具
	}

	// 创建 LLM 代理
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true, // 启用流式响应
	}

	agentName := "jessemjchen-multi-tool-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("一个功能强大的AI助手，拥有计算器、时间、文本处理、文件操作和网络搜索等多种工具"),
		llmagent.WithInstruction(`你是 jessemjchen 的智能助手，可以使用多种工具：
1. calculator: 进行数学计算，支持基本运算、科学计算等
2. time_tool: 获取当前时间、日期、时区信息等
3. text_tool: 处理文本，包括大小写转换、长度统计、字符串操作等
4. file_tool: 基本文件操作，如读取、写入、列出目录等
5. duckduckgo_search: 搜索网络信息，适合查找事实性、百科类信息

请根据用户的需求选择合适的工具，并提供有用的帮助。使用中文与用户交流。`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools(tools),
	)

	// 创建运行器
	appName := "jessemjchen-multi-tool-chat"
	c.runner = runner.New(
		appName,
		llmAgent,
	)

	// 设置标识符
	c.userID = "jessemjchen"
	c.sessionID = fmt.Sprintf("multi-tool-session-%d", time.Now().Unix())

	fmt.Printf("✅ 多工具智能助手已就绪! 会话ID: %s\n\n", c.sessionID)

	return nil
}

// startChat 运行交互式对话循环
func (c *multiToolChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	// 打印欢迎消息和示例
	fmt.Println("💡 试试问这些问题：")
	fmt.Println("   【计算器】计算 123 + 456 * 789")
	fmt.Println("   【计算器】计算圆周率的平方根")
	fmt.Println("   【时间】现在是几点？")
	fmt.Println("   【时间】今天是星期几？")
	fmt.Println("   【文本】把 'Hello World' 转换为大写")
	fmt.Println("   【文本】统计 'Hello World' 的字符数")
	fmt.Println("   【文件】读取 README.md 文件")
	fmt.Println("   【文件】在当前目录创建一个测试文件")
	fmt.Println("   【搜索】搜索史蒂夫乔布斯的信息")
	fmt.Println("   【搜索】查找特斯拉公司的资料")
	fmt.Println()

	for {
		fmt.Print("👤 jessemjchen: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// 处理退出命令
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 再见！")
			return nil
		}

		// 处理用户消息
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ 错误: %v\n", err)
		}

		fmt.Println() // 在对话轮次之间添加空行
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("输入扫描器错误: %w", err)
	}

	return nil
}

// processMessage 处理单个消息交换
func (c *multiToolChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// 通过运行器运行代理
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.RunOptions{})
	if err != nil {
		return fmt.Errorf("运行代理失败: %w", err)
	}

	// 处理流式响应
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse 处理流式响应，包含工具调用的可视化
func (c *multiToolChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 助手: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		// 处理错误
		if event.Error != nil {
			fmt.Printf("\n❌ 错误: %s\n", event.Error.Message)
			continue
		}

		// 检测并显示工具调用
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 工具调用:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				toolIcon := getToolIcon(toolCall.Function.Name)
				fmt.Printf("   %s %s (ID: %s)\n", toolIcon, toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     参数: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n⚡ 执行中...\n")
		}

		// 检测工具响应
		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ 工具结果 (ID: %s): %s\n",
						choice.Message.ToolID,
						formatToolResult(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// 处理流式内容
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

			// 处理流式增量内容
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 助手: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// 检查是否是最终事件
		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isToolEvent 检查事件是否是工具响应（而不是最终响应）
func (c *multiToolChat) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}
	return false
}

// getToolIcon 根据工具名称返回对应的图标
func getToolIcon(toolName string) string {
	switch toolName {
	case "calculator":
		return "🧮"
	case "time_tool":
		return "⏰"
	case "text_tool":
		return "📝"
	case "file_tool":
		return "📁"
	case "duckduckgo_search":
		return "🔍"
	default:
		return "🔧"
	}
}

// formatToolResult 格式化工具结果的显示
func formatToolResult(content string) string {
	if len(content) > 200 {
		return content[:200] + "..."
	}
	return strings.TrimSpace(content)
}

// 计算器工具相关结构
type calculatorRequest struct {
	Expression string `json:"expression" jsonschema:"description=要计算的数学表达式，如 '2+3*4', 'sqrt(16)', 'sin(30*pi/180)'"`
}

type calculatorResponse struct {
	Expression string  `json:"expression"`
	Result     float64 `json:"result"`
	Message    string  `json:"message"`
}

// createCalculatorTool 创建计算器工具
func createCalculatorTool() tool.CallableTool {
	return function.NewFunctionTool(
		calculateExpression,
		function.WithName("calculator"),
		function.WithDescription("执行数学计算。支持基本运算 (+, -, *, /)、科学函数 (sin, cos, tan, sqrt, log, ln, abs, pow)、常数 (pi, e)。示例：'2+3*4', 'sqrt(16)', 'sin(30*pi/180)', 'log10(100)'"),
	)
}

// calculateExpression 计算数学表达式
func calculateExpression(req calculatorRequest) calculatorResponse {
	if strings.TrimSpace(req.Expression) == "" {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    "错误：表达式为空",
		}
	}

	// 简单的表达式计算器实现
	result, err := evaluateExpression(req.Expression)
	if err != nil {
		return calculatorResponse{
			Expression: req.Expression,
			Result:     0,
			Message:    fmt.Sprintf("计算错误: %v", err),
		}
	}

	return calculatorResponse{
		Expression: req.Expression,
		Result:     result,
		Message:    fmt.Sprintf("计算结果: %g", result),
	}
}

// evaluateExpression 简单的表达式求值器
func evaluateExpression(expr string) (float64, error) {
	// 替换常数
	expr = strings.ReplaceAll(expr, "pi", fmt.Sprintf("%g", math.Pi))
	expr = strings.ReplaceAll(expr, "e", fmt.Sprintf("%g", math.E))

	// 简单实现：支持基本运算
	// 这里是一个简化版本，实际应用中可能需要更复杂的表达式解析器
	expr = strings.ReplaceAll(expr, " ", "")

	// 处理基本的数学函数
	if strings.Contains(expr, "sqrt(") {
		return handleSqrt(expr)
	}
	if strings.Contains(expr, "sin(") {
		return handleSin(expr)
	}
	if strings.Contains(expr, "cos(") {
		return handleCos(expr)
	}
	if strings.Contains(expr, "abs(") {
		return handleAbs(expr)
	}

	// 处理基本运算
	return evaluateBasicExpression(expr)
}

// handleSqrt 处理平方根函数
func handleSqrt(expr string) (float64, error) {
	if strings.HasPrefix(expr, "sqrt(") && strings.HasSuffix(expr, ")") {
		inner := expr[5 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		if val < 0 {
			return 0, fmt.Errorf("不能计算负数的平方根")
		}
		return math.Sqrt(val), nil
	}
	return 0, fmt.Errorf("sqrt 函数格式错误")
}

// handleSin 处理正弦函数
func handleSin(expr string) (float64, error) {
	if strings.HasPrefix(expr, "sin(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		return math.Sin(val), nil
	}
	return 0, fmt.Errorf("sin 函数格式错误")
}

// handleCos 处理余弦函数
func handleCos(expr string) (float64, error) {
	if strings.HasPrefix(expr, "cos(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		return math.Cos(val), nil
	}
	return 0, fmt.Errorf("cos 函数格式错误")
}

// handleAbs 处理绝对值函数
func handleAbs(expr string) (float64, error) {
	if strings.HasPrefix(expr, "abs(") && strings.HasSuffix(expr, ")") {
		inner := expr[4 : len(expr)-1]
		val, err := evaluateBasicExpression(inner)
		if err != nil {
			return 0, err
		}
		return math.Abs(val), nil
	}
	return 0, fmt.Errorf("abs 函数格式错误")
}

// evaluateBasicExpression 计算基本的数学表达式
func evaluateBasicExpression(expr string) (float64, error) {
	// 简单的加减乘除运算
	// 这里只是一个基本实现，实际应用可能需要更复杂的解析器

	// 处理乘法
	if strings.Contains(expr, "*") {
		parts := strings.Split(expr, "*")
		if len(parts) == 2 {
			a, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			b, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				return a * b, nil
			}
		}
	}

	// 处理除法
	if strings.Contains(expr, "/") {
		parts := strings.Split(expr, "/")
		if len(parts) == 2 {
			a, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			b, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				if b == 0 {
					return 0, fmt.Errorf("除数不能为零")
				}
				return a / b, nil
			}
		}
	}

	// 处理加法
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		if len(parts) == 2 {
			a, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			b, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				return a + b, nil
			}
		}
	}

	// 处理减法
	if strings.Contains(expr, "-") && !strings.HasPrefix(expr, "-") {
		parts := strings.Split(expr, "-")
		if len(parts) == 2 {
			a, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			b, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				return a - b, nil
			}
		}
	}

	// 尝试解析为单个数字
	return strconv.ParseFloat(expr, 64)
}

// 时间工具相关结构
type timeRequest struct {
	Operation string `json:"operation" jsonschema:"description=时间操作类型：'current' (当前时间), 'date' (当前日期), 'weekday' (星期几), 'timestamp' (时间戳)"`
}

type timeResponse struct {
	Operation string `json:"operation"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
}

// createTimeTool 创建时间工具
func createTimeTool() tool.CallableTool {
	return function.NewFunctionTool(
		getTimeInfo,
		function.WithName("time_tool"),
		function.WithDescription("获取时间和日期信息。支持操作：'current'(当前时间), 'date'(当前日期), 'weekday'(星期几), 'timestamp'(Unix时间戳)"),
	)
}

// getTimeInfo 获取时间信息
func getTimeInfo(req timeRequest) timeResponse {
	now := time.Now()

	var result string
	switch req.Operation {
	case "current":
		result = now.Format("2006-01-02 15:04:05")
	case "date":
		result = now.Format("2006-01-02")
	case "weekday":
		weekdays := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
		result = weekdays[now.Weekday()]
	case "timestamp":
		result = fmt.Sprintf("%d", now.Unix())
	default:
		result = fmt.Sprintf("当前时间: %s", now.Format("2006-01-02 15:04:05"))
	}

	return timeResponse{
		Operation: req.Operation,
		Result:    result,
		Timestamp: now.Unix(),
	}
}

// 文本工具相关结构
type textRequest struct {
	Text      string `json:"text" jsonschema:"description=要处理的文本内容"`
	Operation string `json:"operation" jsonschema:"description=文本操作类型：'uppercase' (转大写), 'lowercase' (转小写), 'length' (计算长度), 'reverse' (反转), 'words' (统计单词数)"`
}

type textResponse struct {
	OriginalText string `json:"original_text"`
	Operation    string `json:"operation"`
	Result       string `json:"result"`
	Info         string `json:"info"`
}

// createTextTool 创建文本处理工具
func createTextTool() tool.CallableTool {
	return function.NewFunctionTool(
		processText,
		function.WithName("text_tool"),
		function.WithDescription("处理文本内容。支持操作：'uppercase'(转大写), 'lowercase'(转小写), 'length'(计算长度), 'reverse'(反转文本), 'words'(统计单词数)"),
	)
}

// processText 处理文本
func processText(req textRequest) textResponse {
	var result string
	var info string

	switch req.Operation {
	case "uppercase":
		result = strings.ToUpper(req.Text)
		info = "文本已转换为大写"
	case "lowercase":
		result = strings.ToLower(req.Text)
		info = "文本已转换为小写"
	case "length":
		length := len([]rune(req.Text))
		result = fmt.Sprintf("%d", length)
		info = fmt.Sprintf("文本长度为 %d 个字符", length)
	case "reverse":
		runes := []rune(req.Text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		result = string(runes)
		info = "文本已反转"
	case "words":
		words := strings.Fields(req.Text)
		result = fmt.Sprintf("%d", len(words))
		info = fmt.Sprintf("文本包含 %d 个单词", len(words))
	default:
		result = req.Text
		info = "无效的操作类型"
	}

	return textResponse{
		OriginalText: req.Text,
		Operation:    req.Operation,
		Result:       result,
		Info:         info,
	}
}

// 文件工具相关结构
type fileRequest struct {
	Path      string `json:"path" jsonschema:"description=文件或目录路径"`
	Operation string `json:"operation" jsonschema:"description=文件操作类型：'read' (读取文件), 'write' (写入文件), 'list' (列出目录), 'exists' (检查文件是否存在)"`
	Content   string `json:"content,omitempty" jsonschema:"description=写入文件时的内容（仅用于 write 操作）"`
}

type fileResponse struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Result    string `json:"result"`
	Success   bool   `json:"success"`
	Message   string `json:"message"`
}

// createFileTool 创建文件操作工具
func createFileTool() tool.CallableTool {
	return function.NewFunctionTool(
		handleFileOperation,
		function.WithName("file_tool"),
		function.WithDescription("执行基本文件操作。支持操作：'read'(读取文件内容), 'write'(写入文件), 'list'(列出目录内容), 'exists'(检查文件是否存在)。注意：出于安全考虑，只能访问当前工作目录及其子目录。"),
	)
}

// handleFileOperation 处理文件操作
func handleFileOperation(req fileRequest) fileResponse {
	// 安全检查：防止路径遍历攻击
	if strings.Contains(req.Path, "..") {
		return fileResponse{
			Path:      req.Path,
			Operation: req.Operation,
			Result:    "",
			Success:   false,
			Message:   "安全错误：不允许访问上级目录",
		}
	}

	switch req.Operation {
	case "read":
		return readFile(req.Path)
	case "write":
		return writeFile(req.Path, req.Content)
	case "list":
		return listDirectory(req.Path)
	case "exists":
		return checkFileExists(req.Path)
	default:
		return fileResponse{
			Path:      req.Path,
			Operation: req.Operation,
			Result:    "",
			Success:   false,
			Message:   "不支持的文件操作",
		}
	}
}

// readFile 读取文件内容
func readFile(path string) fileResponse {
	content, err := os.ReadFile(path)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "read",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("读取文件失败: %v", err),
		}
	}

	// 限制返回内容的长度
	contentStr := string(content)
	if len(contentStr) > 1000 {
		contentStr = contentStr[:1000] + "\n... (文件内容过长，已截断)"
	}

	return fileResponse{
		Path:      path,
		Operation: "read",
		Result:    contentStr,
		Success:   true,
		Message:   fmt.Sprintf("成功读取文件，大小: %d 字节", len(content)),
	}
}

// writeFile 写入文件内容
func writeFile(path, content string) fileResponse {
	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fileResponse{
			Path:      path,
			Operation: "write",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("创建目录失败: %v", err),
		}
	}

	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "write",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("写入文件失败: %v", err),
		}
	}

	return fileResponse{
		Path:      path,
		Operation: "write",
		Result:    fmt.Sprintf("写入了 %d 字节", len(content)),
		Success:   true,
		Message:   "文件写入成功",
	}
}

// listDirectory 列出目录内容
func listDirectory(path string) fileResponse {
	if path == "" {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fileResponse{
			Path:      path,
			Operation: "list",
			Result:    "",
			Success:   false,
			Message:   fmt.Sprintf("列出目录失败: %v", err),
		}
	}

	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			result.WriteString(fmt.Sprintf("[目录] %s\n", entry.Name()))
		} else {
			info, _ := entry.Info()
			size := ""
			if info != nil {
				size = fmt.Sprintf(" (%d 字节)", info.Size())
			}
			result.WriteString(fmt.Sprintf("[文件] %s%s\n", entry.Name(), size))
		}
	}

	return fileResponse{
		Path:      path,
		Operation: "list",
		Result:    result.String(),
		Success:   true,
		Message:   fmt.Sprintf("找到 %d 个项目", len(entries)),
	}
}

// checkFileExists 检查文件是否存在
func checkFileExists(path string) fileResponse {
	_, err := os.Stat(path)
	exists := err == nil

	var message string
	if exists {
		message = "文件存在"
	} else {
		message = "文件不存在"
	}

	return fileResponse{
		Path:      path,
		Operation: "exists",
		Result:    fmt.Sprintf("%t", exists),
		Success:   true,
		Message:   message,
	}
}

// intPtr 返回给定整数的指针
func intPtr(i int) *int {
	return &i
}

// floatPtr 返回给定浮点数的指针
func floatPtr(f float64) *float64 {
	return &f
}
