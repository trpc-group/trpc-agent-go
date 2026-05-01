package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/bedrock"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// 示例 2: 流式调用
// ============================================================================

// RunStreamingExamples 运行所有流式调用示例。
func RunStreamingExamples(ctx context.Context, m *bedrock.Model) {
	printHeader("流式调用示例 (Streaming)")

	streamingTextConversation(ctx, m)
	streamingToolCallConversation(ctx, m)
	streamingMixedContent(ctx, m)
}

// streamingTextConversation 文本流式对话。
//
// 场景：实时逐字显示模型回复，提升用户体验。
// 流式模式下，模型会逐步返回文本片段（chunk），客户端可以立即展示。
func streamingTextConversation(ctx context.Context, m *bedrock.Model) {
	printSubHeader("2.1 文本流式对话")
	start := time.Now()

	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a creative storyteller. Write vivid and engaging content."),
			model.NewUserMessage("Write a short story (about 100 words) about a robot learning to paint."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   intPtr(300),
			Temperature: float64Ptr(0.8),
			Stream:      true, // 开启流式
		},
	})
	if err != nil {
		log.Printf("❌ 请求失败: %v\n", err)
		return
	}

	fmt.Print("🤖 流式回复: ")
	var totalChunks int
	for resp := range ch {
		if resp.Error != nil {
			fmt.Printf("\n❌ 错误: %s\n", resp.Error.Message)
			return
		}
		// 流式模式下，IsPartial=true 的响应包含增量内容
		if resp.IsPartial && len(resp.Choices) > 0 {
			fmt.Print(resp.Choices[0].Delta.Content)
			totalChunks++
		}
		// Done=true 表示流结束
		if resp.Done {
			fmt.Println()
			printFinishReason(resp.Choices[0].FinishReason)
		}
	}

	fmt.Printf("📊 共收到 %d 个文本块\n", totalChunks)
	printElapsed(start)
	printDivider()
}

// streamingToolCallConversation 流式工具调用对话。
//
// 场景：在流式模式下，模型决定调用工具，客户端执行工具后将结果返回模型。
// 流式工具调用的特点是工具参数也是增量返回的，最终在 Done 响应中汇总。
func streamingToolCallConversation(ctx context.Context, m *bedrock.Model) {
	printSubHeader("2.2 流式工具调用对话")
	start := time.Now()

	tools := buildBasicTools()

	messages := []model.Message{
		model.NewSystemMessage("You are a helpful assistant. Use tools when needed."),
		model.NewUserMessage("What's the weather in Beijing? Also calculate 256 * 128 for me."),
	}

	fmt.Printf("👤 用户: %s\n\n", messages[len(messages)-1].Content)

	// 自动工具调用循环（最多 3 轮）
	for round := 1; round <= 3; round++ {
		fmt.Printf("🔄 第 %d 轮请求 (流式)\n", round)

		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages: messages,
			GenerationConfig: model.GenerationConfig{
				MaxTokens: intPtr(512),
				Stream:    true,
			},
			Tools: tools,
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		var finalResp *model.Response
		fmt.Print("   ")
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("\n❌ 错误: %s\n", resp.Error.Message)
				return
			}
			if resp.IsPartial && len(resp.Choices) > 0 {
				content := resp.Choices[0].Delta.Content
				if content != "" {
					fmt.Print(content)
				}
			}
			if resp.Done {
				finalResp = resp
			}
		}
		fmt.Println()

		if finalResp == nil {
			log.Println("❌ 未收到最终响应")
			return
		}

		// 检查是否有工具调用
		toolCalls := finalResp.Choices[0].Message.ToolCalls
		if len(toolCalls) == 0 {
			fmt.Printf("✅ 对话完成\n")
			break
		}

		// 将助手消息加入历史
		messages = append(messages, finalResp.Choices[0].Message)

		// 执行工具调用
		for _, tc := range toolCalls {
			fmt.Printf("   🔧 调用工具: %s(%s)\n", tc.Function.Name, string(tc.Function.Arguments))

			result, callErr := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)
			if callErr != nil {
				result = fmt.Sprintf(`{"error": "%s"}`, callErr.Error())
			}
			fmt.Printf("   ✅ 结果: %s\n", result)

			messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
		}
	}

	printElapsed(start)
	printDivider()
}

// streamingMixedContent 混合内容流式对话。
//
// 场景：模型在流式回复中既包含文本内容，又可能触发工具调用。
// 展示如何在流式模式下同时处理文本和工具调用。
func streamingMixedContent(ctx context.Context, m *bedrock.Model) {
	printSubHeader("2.3 混合内容流式对话")
	start := time.Now()

	tools := map[string]tool.Tool{
		"get_weather": &weatherTool{},
	}

	messages := []model.Message{
		model.NewSystemMessage(
			"You are a travel advisor. When asked about destinations, " +
				"first check the weather, then provide recommendations. " +
				"Always include the weather information in your response.",
		),
		model.NewUserMessage("I'm planning a trip to Tokyo next week. What should I know?"),
	}

	fmt.Printf("👤 用户: %s\n\n", messages[len(messages)-1].Content)

	// 多轮流式对话
	for round := 1; round <= 3; round++ {
		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages: messages,
			GenerationConfig: model.GenerationConfig{
				MaxTokens: intPtr(512),
				Stream:    true,
			},
			Tools: tools,
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		var (
			finalResp *model.Response
			textParts []string
		)

		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("❌ 错误: %s\n", resp.Error.Message)
				return
			}
			if resp.IsPartial && len(resp.Choices) > 0 {
				delta := resp.Choices[0].Delta.Content
				if delta != "" {
					fmt.Print(delta)
					textParts = append(textParts, delta)
				}
			}
			if resp.Done {
				finalResp = resp
			}
		}

		if len(textParts) > 0 {
			fmt.Println()
		}

		if finalResp == nil {
			break
		}

		toolCalls := finalResp.Choices[0].Message.ToolCalls
		if len(toolCalls) == 0 {
			fmt.Printf("✅ 旅行建议生成完成\n")
			break
		}

		messages = append(messages, finalResp.Choices[0].Message)

		for _, tc := range toolCalls {
			fmt.Printf("🔧 查询: %s(%s)\n", tc.Function.Name, string(tc.Function.Arguments))

			result, callErr := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)
			if callErr != nil {
				result = fmt.Sprintf(`{"error": "%s"}`, callErr.Error())
			}

			var prettyResult map[string]interface{}
			if json.Unmarshal([]byte(result), &prettyResult) == nil {
				fmt.Printf("   🌤️ %s: %s, %s, 湿度 %s\n",
					prettyResult["city"], prettyResult["temperature"],
					prettyResult["condition"], prettyResult["humidity"])
			}

			messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
		}
		fmt.Println()
	}

	printElapsed(start)
	printDivider()
}

// collectStreamResponse 收集流式响应的完整内容（辅助函数）。
func collectStreamResponse(ch <-chan *model.Response) (fullContent string, finalResp *model.Response, err error) {
	var parts []string
	for resp := range ch {
		if resp.Error != nil {
			return "", nil, fmt.Errorf("API 错误: %s", resp.Error.Message)
		}
		if resp.IsPartial && len(resp.Choices) > 0 {
			parts = append(parts, resp.Choices[0].Delta.Content)
		}
		if resp.Done {
			finalResp = resp
		}
	}
	return strings.Join(parts, ""), finalResp, nil
}
