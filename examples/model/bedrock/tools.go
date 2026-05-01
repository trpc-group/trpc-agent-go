package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// 天气查询工具
// ============================================================================

// weatherTool 模拟天气查询工具。
type weatherTool struct{}

func (w *weatherTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "get_weather",
		Description: "获取指定城市的当前天气信息，包括温度、天气状况和湿度。",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"city": {
					Type:        "string",
					Description: "城市名称，例如：Beijing、Shanghai、Tokyo",
				},
				"unit": {
					Type:        "string",
					Description: "温度单位: celsius 或 fahrenheit",
					Enum:        []any{"celsius", "fahrenheit"},
					Default:     "celsius",
				},
			},
			Required: []string{"city"},
		},
	}
}

func (w *weatherTool) Call(_ context.Context, args []byte) (any, error) {
	var params struct {
		City string `json:"city"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("解析参数失败: %w", err)
	}

	// 模拟天气数据
	weatherDB := map[string]map[string]interface{}{
		"beijing":  {"temperature": 22, "condition": "晴", "humidity": 35, "wind": "北风3级"},
		"shanghai": {"temperature": 26, "condition": "多云", "humidity": 65, "wind": "东南风2级"},
		"tokyo":    {"temperature": 24, "condition": "阴", "humidity": 70, "wind": "西风2级"},
		"london":   {"temperature": 15, "condition": "小雨", "humidity": 80, "wind": "西南风4级"},
		"new york": {"temperature": 20, "condition": "晴", "humidity": 45, "wind": "西风3级"},
	}

	city := strings.ToLower(params.City)
	data, ok := weatherDB[city]
	if !ok {
		conditions := []string{"晴", "多云", "阴", "小雨", "大风"}
		data = map[string]interface{}{
			"temperature": 10 + rand.Intn(25),
			"condition":   conditions[rand.Intn(len(conditions))],
			"humidity":    30 + rand.Intn(50),
			"wind":        "微风",
		}
	}

	unit := "°C"
	temp := data["temperature"].(int)
	if params.Unit == "fahrenheit" {
		temp = temp*9/5 + 32
		unit = "°F"
	}

	return map[string]interface{}{
		"city":        params.City,
		"temperature": fmt.Sprintf("%d%s", temp, unit),
		"condition":   data["condition"],
		"humidity":    fmt.Sprintf("%d%%", data["humidity"]),
		"wind":        data["wind"],
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

// ============================================================================
// 计算器工具
// ============================================================================

// calculatorTool 模拟计算器工具。
type calculatorTool struct{}

func (c *calculatorTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "calculator",
		Description: "执行数学计算。支持加减乘除四则运算。",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"operation": {
					Type:        "string",
					Description: "运算类型",
					Enum:        []any{"add", "subtract", "multiply", "divide"},
				},
				"a": {
					Type:        "number",
					Description: "第一个操作数",
				},
				"b": {
					Type:        "number",
					Description: "第二个操作数",
				},
			},
			Required: []string{"operation", "a", "b"},
		},
	}
}

func (c *calculatorTool) Call(_ context.Context, args []byte) (any, error) {
	var params struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("解析参数失败: %w", err)
	}

	var result float64
	switch params.Operation {
	case "add":
		result = params.A + params.B
	case "subtract":
		result = params.A - params.B
	case "multiply":
		result = params.A * params.B
	case "divide":
		if params.B == 0 {
			return nil, fmt.Errorf("除数不能为零")
		}
		result = params.A / params.B
	default:
		return nil, fmt.Errorf("不支持的运算: %s", params.Operation)
	}

	return map[string]interface{}{
		"operation":  params.Operation,
		"a":          params.A,
		"b":          params.B,
		"result":     result,
		"expression": fmt.Sprintf("%.2f %s %.2f = %.2f", params.A, params.Operation, params.B, result),
	}, nil
}

// ============================================================================
// 搜索工具（复杂多参数工具）
// ============================================================================

// searchTool 模拟搜索工具，展示复杂参数。
type searchTool struct{}

func (s *searchTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "web_search",
		Description: "搜索互联网获取最新信息。支持按类型、时间范围和语言过滤。",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"query": {
					Type:        "string",
					Description: "搜索关键词",
				},
				"type": {
					Type:        "string",
					Description: "搜索类型",
					Enum:        []any{"web", "news", "image"},
					Default:     "web",
				},
				"time_range": {
					Type:        "string",
					Description: "时间范围",
					Enum:        []any{"day", "week", "month", "year"},
				},
				"language": {
					Type:        "string",
					Description: "结果语言，如 zh、en、ja",
					Default:     "zh",
				},
				"max_results": {
					Type:        "number",
					Description: "最大返回结果数，1-10",
					Default:     3,
				},
			},
			Required: []string{"query"},
		},
	}
}

func (s *searchTool) Call(_ context.Context, args []byte) (any, error) {
	var params struct {
		Query      string `json:"query"`
		Type       string `json:"type"`
		TimeRange  string `json:"time_range"`
		Language   string `json:"language"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("解析参数失败: %w", err)
	}

	if params.MaxResults <= 0 {
		params.MaxResults = 3
	}

	// 模拟搜索结果
	results := []map[string]string{
		{
			"title":   fmt.Sprintf("关于「%s」的最新资讯", params.Query),
			"url":     "https://example.com/article/1",
			"snippet": fmt.Sprintf("这是关于 %s 的详细介绍和最新动态...", params.Query),
		},
		{
			"title":   fmt.Sprintf("%s - 维基百科", params.Query),
			"url":     "https://example.com/wiki/" + params.Query,
			"snippet": fmt.Sprintf("%s 是一个广泛讨论的话题，涉及多个领域...", params.Query),
		},
		{
			"title":   fmt.Sprintf("深入了解 %s 的技术细节", params.Query),
			"url":     "https://example.com/tech/" + params.Query,
			"snippet": fmt.Sprintf("本文从技术角度深入分析 %s 的原理和应用...", params.Query),
		},
	}

	if params.MaxResults < len(results) {
		results = results[:params.MaxResults]
	}

	return map[string]interface{}{
		"query":       params.Query,
		"type":        params.Type,
		"total_found": 1234,
		"results":     results,
	}, nil
}

// ============================================================================
// 会出错的工具（用于错误处理示例）
// ============================================================================

// unreliableTool 模拟一个不稳定的工具，有概率失败。
type unreliableTool struct {
	callCount int
}

func (u *unreliableTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "unreliable_service",
		Description: "调用一个不稳定的外部服务，可能会失败。用于演示错误处理。",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"action": {
					Type:        "string",
					Description: "要执行的操作",
				},
			},
			Required: []string{"action"},
		},
	}
}

func (u *unreliableTool) Call(_ context.Context, args []byte) (any, error) {
	u.callCount++
	var params struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("解析参数失败: %w", err)
	}

	// 前两次调用失败，第三次成功
	if u.callCount <= 2 {
		return nil, fmt.Errorf("服务暂时不可用 (尝试 %d/3)，请稍后重试", u.callCount)
	}

	return map[string]interface{}{
		"status":  "success",
		"action":  params.Action,
		"message": fmt.Sprintf("操作 '%s' 执行成功（第 %d 次尝试）", params.Action, u.callCount),
	}, nil
}

// ============================================================================
// 工具集合构建函数
// ============================================================================

// buildBasicTools 构建基础工具集（天气 + 计算器）。
func buildBasicTools() map[string]tool.Tool {
	return map[string]tool.Tool{
		"get_weather": &weatherTool{},
		"calculator":  &calculatorTool{},
	}
}

// buildFullTools 构建完整工具集（天气 + 计算器 + 搜索）。
func buildFullTools() map[string]tool.Tool {
	return map[string]tool.Tool{
		"get_weather": &weatherTool{},
		"calculator":  &calculatorTool{},
		"web_search":  &searchTool{},
	}
}

// executeTool 执行工具调用并返回结果字符串。
func executeTool(ctx context.Context, tools map[string]tool.Tool, name string, args []byte) (string, error) {
	t, ok := tools[name]
	if !ok {
		return "", fmt.Errorf("未知工具: %s", name)
	}

	callable, ok := t.(tool.CallableTool)
	if !ok {
		return "", fmt.Errorf("工具 %s 不支持调用", name)
	}

	result, err := callable.Call(ctx, args)
	if err != nil {
		return "", err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}

	return string(resultJSON), nil
}
