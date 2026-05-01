package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/bedrock"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// 示例 4: 工具调用
// ============================================================================

// RunToolCallExamples 运行所有工具调用示例。
func RunToolCallExamples(ctx context.Context, m *bedrock.Model) {
	printHeader("工具调用示例 (Tool Calling)")

	toolCallSimple(ctx, m)
	toolCallComplex(ctx, m)
	toolCallResultProcessing(ctx, m)
	toolCallErrorHandling(ctx, m)
}

// toolCallSimple 简单函数调用。
//
// 场景：模型判断需要调用工具（如天气查询），客户端执行后返回结果。
// 这是最基本的工具调用流程：请求 → 工具调用 → 执行 → 返回结果 → 最终回复。
func toolCallSimple(ctx context.Context, m *bedrock.Model) {
	printSubHeader("4.1 简单函数调用（天气查询）")
	start := time.Now()

	tools := map[string]tool.Tool{
		"get_weather": &weatherTool{},
	}

	messages := []model.Message{
		model.NewSystemMessage("You are a helpful assistant. Use tools when needed. Reply in Chinese."),
		model.NewUserMessage("What's the weather like in Shanghai today?"),
	}

	fmt.Printf("👤 用户: %s\n\n", messages[len(messages)-1].Content)

	// 第一轮：模型决定调用工具
	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages:         messages,
		GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(256)},
		Tools:            tools,
	})
	if err != nil {
		log.Printf("❌ 请求失败: %v\n", err)
		return
	}

	var firstResp *model.Response
	for resp := range ch {
		if resp.Error != nil {
			log.Printf("❌ 错误: %s\n", resp.Error.Message)
			return
		}
		firstResp = resp
	}

	if firstResp == nil || len(firstResp.Choices) == 0 {
		log.Println("❌ 未收到响应")
		return
	}

	toolCalls := firstResp.Choices[0].Message.ToolCalls
	if len(toolCalls) == 0 {
		fmt.Printf("🤖 直接回复: %s\n", firstResp.Choices[0].Message.Content)
		printDivider()
		return
	}

	// 将助手消息加入历史
	messages = append(messages, firstResp.Choices[0].Message)

	// 执行工具调用
	for _, tc := range toolCalls {
		fmt.Printf("🔧 模型请求调用: %s\n", tc.Function.Name)
		fmt.Printf("   参数: %s\n", string(tc.Function.Arguments))

		result, callErr := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)
		if callErr != nil {
			result = fmt.Sprintf(`{"error": "%s"}`, callErr.Error())
		}
		fmt.Printf("   结果: %s\n\n", result)

		messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
	}

	// 第二轮：模型根据工具结果生成回复
	ch, err = m.GenerateContent(ctx, &model.Request{
		Messages:         messages,
		GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(256)},
		Tools:            tools,
	})
	if err != nil {
		log.Printf("❌ 第二轮请求失败: %v\n", err)
		return
	}

	for resp := range ch {
		if resp.Error != nil {
			log.Printf("❌ 错误: %s\n", resp.Error.Message)
			return
		}
		if resp.Done && len(resp.Choices) > 0 {
			fmt.Printf("🤖 最终回复: %s\n", resp.Choices[0].Message.Content)
		}
	}

	printElapsed(start)
	printDivider()
}

// toolCallComplex 复杂工具调用（多参数函数 + 多工具并行）。
//
// 场景：模型同时调用多个工具，每个工具有复杂的参数结构。
// Bedrock 支持在一次回复中返回多个工具调用请求。
func toolCallComplex(ctx context.Context, m *bedrock.Model) {
	printSubHeader("4.2 复杂工具调用（多工具并行）")
	start := time.Now()

	tools := buildFullTools()

	messages := []model.Message{
		model.NewSystemMessage("You are a research assistant. Use tools to gather information."),
		model.NewUserMessage(
			"I need to know: 1) the weather in Tokyo, " +
				"2) calculate 1024 divided by 16, " +
				"3) search for 'AWS Bedrock pricing'. " +
				"Please help me with all three.",
		),
	}

	fmt.Printf("👤 用户: %s\n\n", messages[len(messages)-1].Content)

	// 工具调用循环
	for round := 1; round <= 5; round++ {
		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages:         messages,
			GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(512)},
			Tools:            tools,
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		var finalResp *model.Response
		for resp := range ch {
			if resp.Error != nil {
				log.Printf("❌ 错误: %s\n", resp.Error.Message)
				return
			}
			finalResp = resp
		}

		if finalResp == nil || len(finalResp.Choices) == 0 {
			break
		}

		toolCalls := finalResp.Choices[0].Message.ToolCalls
		if len(toolCalls) == 0 {
			fmt.Printf("🤖 最终回复:\n%s\n", finalResp.Choices[0].Message.Content)
			break
		}

		fmt.Printf("🔄 第 %d 轮: 模型请求 %d 个工具调用\n", round, len(toolCalls))
		messages = append(messages, finalResp.Choices[0].Message)

		for i, tc := range toolCalls {
			fmt.Printf("   [%d] 🔧 %s: %s\n", i+1, tc.Function.Name, string(tc.Function.Arguments))

			result, callErr := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)
			if callErr != nil {
				result = fmt.Sprintf(`{"error": "%s"}`, callErr.Error())
			}

			// 简化输出
			var resultMap map[string]interface{}
			if json.Unmarshal([]byte(result), &resultMap) == nil {
				fmt.Printf("       ✅ 成功返回结果\n")
			}

			messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
		}
		fmt.Println()
	}

	printElapsed(start)
	printDivider()
}

// toolCallResultProcessing 工具调用结果处理。
//
// 场景：展示如何处理不同格式的工具返回结果，
// 包括 JSON 对象、数组、嵌套结构等。
func toolCallResultProcessing(ctx context.Context, m *bedrock.Model) {
	printSubHeader("4.3 工具调用结果处理")
	start := time.Now()

	tools := map[string]tool.Tool{
		"web_search": &searchTool{},
	}

	messages := []model.Message{
		model.NewSystemMessage(
			"You are a research assistant. " +
				"When you get search results, summarize the key findings concisely.",
		),
		model.NewUserMessage("Search for information about Go 1.22 new features and summarize."),
	}

	fmt.Printf("👤 用户: %s\n\n", messages[len(messages)-1].Content)

	// 工具调用循环
	for round := 1; round <= 3; round++ {
		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages:         messages,
			GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(512)},
			Tools:            tools,
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		var finalResp *model.Response
		for resp := range ch {
			if resp.Error != nil {
				log.Printf("❌ 错误: %s\n", resp.Error.Message)
				return
			}
			finalResp = resp
		}

		if finalResp == nil || len(finalResp.Choices) == 0 {
			break
		}

		toolCalls := finalResp.Choices[0].Message.ToolCalls
		if len(toolCalls) == 0 {
			fmt.Printf("🤖 摘要回复:\n%s\n", finalResp.Choices[0].Message.Content)
			break
		}

		messages = append(messages, finalResp.Choices[0].Message)

		for _, tc := range toolCalls {
			fmt.Printf("🔧 搜索: %s\n", string(tc.Function.Arguments))

			result, callErr := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)
			if callErr != nil {
				result = fmt.Sprintf(`{"error": "%s"}`, callErr.Error())
				fmt.Printf("   ❌ 搜索失败: %s\n", callErr.Error())
			} else {
				// 解析并展示搜索结果
				var searchResult map[string]interface{}
				if json.Unmarshal([]byte(result), &searchResult) == nil {
					if results, ok := searchResult["results"].([]interface{}); ok {
						fmt.Printf("   📄 找到 %d 条结果:\n", len(results))
						for j, r := range results {
							if item, ok := r.(map[string]interface{}); ok {
								fmt.Printf("      [%d] %s\n", j+1, item["title"])
							}
						}
					}
				}
			}

			messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
		}
		fmt.Println()
	}

	printElapsed(start)
	printDivider()
}

// toolCallErrorHandling 工具调用错误处理。
//
// 场景：工具执行失败时，将错误信息返回给模型，让模型决定如何处理。
// 模型可能会重试、换一种方式调用、或直接告知用户。
func toolCallErrorHandling(ctx context.Context, m *bedrock.Model) {
	printSubHeader("4.4 工具调用错误处理")
	start := time.Now()

	unreliable := &unreliableTool{}
	tools := map[string]tool.Tool{
		"unreliable_service": unreliable,
	}

	messages := []model.Message{
		model.NewSystemMessage(
			"You are a helpful assistant. " +
				"If a tool call fails, you may retry or inform the user about the issue. " +
				"Be transparent about errors.",
		),
		model.NewUserMessage("Please call the unreliable_service with action 'fetch_data'."),
	}

	fmt.Printf("👤 用户: %s\n\n", messages[len(messages)-1].Content)

	// 工具调用循环（允许重试）
	for round := 1; round <= 5; round++ {
		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages:         messages,
			GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(256)},
			Tools:            tools,
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		var finalResp *model.Response
		for resp := range ch {
			if resp.Error != nil {
				log.Printf("❌ 错误: %s\n", resp.Error.Message)
				return
			}
			finalResp = resp
		}

		if finalResp == nil || len(finalResp.Choices) == 0 {
			break
		}

		toolCalls := finalResp.Choices[0].Message.ToolCalls
		if len(toolCalls) == 0 {
			fmt.Printf("🤖 模型回复: %s\n", finalResp.Choices[0].Message.Content)
			break
		}

		messages = append(messages, finalResp.Choices[0].Message)

		for _, tc := range toolCalls {
			fmt.Printf("🔧 [第 %d 轮] 调用: %s(%s)\n", round, tc.Function.Name, string(tc.Function.Arguments))

			result, callErr := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)
			if callErr != nil {
				// 将错误信息作为工具结果返回给模型
				errorResult := fmt.Sprintf(`{"error": "%s", "retryable": true}`, callErr.Error())
				fmt.Printf("   ⚠️ 工具执行失败: %s\n", callErr.Error())
				messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, errorResult))
			} else {
				fmt.Printf("   ✅ 执行成功: %s\n", result)
				messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
			}
		}
		fmt.Println()
	}

	printElapsed(start)
	printDivider()
}
