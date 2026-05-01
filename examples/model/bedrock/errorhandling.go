package main

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/bedrock"
)

// ============================================================================
// 示例 6: 错误处理
// ============================================================================

// RunErrorHandlingExamples 运行所有错误处理示例。
func RunErrorHandlingExamples(ctx context.Context, m *bedrock.Model) {
	printHeader("错误处理示例 (Error Handling)")

	errorHandlingAPIError(ctx, m)
	errorHandlingTimeout(ctx, m)
	errorHandlingRetry(ctx, m)
}

// errorHandlingAPIError API 错误处理。
//
// 场景：当请求参数不合法或模型返回错误时的处理方式。
// 展示如何检查和处理 Response.Error 字段。
func errorHandlingAPIError(ctx context.Context, m *bedrock.Model) {
	printSubHeader("6.1 API 错误处理")

	// 场景 1：空消息列表
	fmt.Println("📌 场景 1: 空消息列表")
	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{},
	})
	if err != nil {
		fmt.Printf("   ✅ 捕获到函数级错误: %v\n", err)
	} else {
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("   ✅ 捕获到 API 错误: [%s] %s\n", resp.Error.Type, resp.Error.Message)
			}
		}
	}
	fmt.Println()

	// 场景 2：MaxTokens 设置为极小值
	fmt.Println("📌 场景 2: MaxTokens=1（极小值）")
	ch, err = m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Tell me a long story about the universe."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: intPtr(1),
		},
	})
	if err != nil {
		fmt.Printf("   ❌ 请求失败: %v\n", err)
	} else {
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("   ✅ API 错误: [%s] %s\n", resp.Error.Type, resp.Error.Message)
			} else if resp.Done && len(resp.Choices) > 0 {
				reason := "unknown"
				if resp.Choices[0].FinishReason != nil {
					reason = *resp.Choices[0].FinishReason
				}
				fmt.Printf("   ℹ️ 响应正常但可能被截断 (finish_reason=%s)\n", reason)
				fmt.Printf("   📝 内容: %s\n", resp.Choices[0].Message.Content)
			}
		}
	}
	fmt.Println()

	// 场景 3：使用不存在的模型 ID
	fmt.Println("📌 场景 3: 无效模型 ID")
	invalidModel := bedrock.New("invalid-model-id-12345",
		bedrock.WithClient(m.GetClient()), // 复用客户端
	)
	ch, err = invalidModel.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Hello"),
		},
	})
	if err != nil {
		fmt.Printf("   ✅ 函数级错误: %v\n", err)
	} else {
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("   ✅ API 错误: [%s] %s\n", resp.Error.Type, resp.Error.Message)
			}
		}
	}

	printDivider()
}

// errorHandlingTimeout 超时控制示例。
//
// 场景：通过 context 设置请求超时，防止长时间等待。
// 当超时发生时，请求会被取消，通道会被关闭。
func errorHandlingTimeout(ctx context.Context, m *bedrock.Model) {
	printSubHeader("6.2 超时控制")

	// 设置一个非常短的超时（用于演示）
	timeouts := []time.Duration{
		100 * time.Millisecond, // 极短超时，很可能触发
		30 * time.Second,       // 正常超时
	}

	for _, timeout := range timeouts {
		fmt.Printf("📌 超时设置: %v\n", timeout)

		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)

		start := time.Now()
		ch, err := m.GenerateContent(timeoutCtx, &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("Write a brief hello message."),
			},
			GenerationConfig: model.GenerationConfig{
				MaxTokens: intPtr(50),
			},
		})

		if err != nil {
			elapsed := time.Since(start)
			if timeoutCtx.Err() != nil {
				fmt.Printf("   ⏰ 超时触发 (耗时 %v): %v\n", elapsed.Round(time.Millisecond), err)
			} else {
				fmt.Printf("   ❌ 其他错误 (耗时 %v): %v\n", elapsed.Round(time.Millisecond), err)
			}
			cancel()
			fmt.Println()
			continue
		}

		var gotResponse bool
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("   ⚠️ 响应错误: %s\n", resp.Error.Message)
			} else if resp.Done && len(resp.Choices) > 0 {
				elapsed := time.Since(start)
				fmt.Printf("   ✅ 正常完成 (耗时 %v): %s\n",
					elapsed.Round(time.Millisecond),
					resp.Choices[0].Message.Content)
				gotResponse = true
			}
		}

		if !gotResponse && timeoutCtx.Err() != nil {
			elapsed := time.Since(start)
			fmt.Printf("   ⏰ 超时触发 (耗时 %v)\n", elapsed.Round(time.Millisecond))
		}

		cancel()
		fmt.Println()
	}

	printDivider()
}

// errorHandlingRetry 重试机制示例。
//
// 场景：当请求失败时，实现指数退避重试策略。
// 适用于网络抖动、限流等临时性错误。
func errorHandlingRetry(ctx context.Context, m *bedrock.Model) {
	printSubHeader("6.3 重试机制")

	maxRetries := 3
	baseDelay := 500 * time.Millisecond

	fmt.Printf("📌 重试策略: 最多 %d 次, 基础延迟 %v (指数退避)\n\n", maxRetries, baseDelay)

	request := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Say 'Hello, retry test!' in exactly those words."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: intPtr(50),
		},
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<(attempt-1)) // 指数退避
			fmt.Printf("   ⏳ 等待 %v 后重试...\n", delay)
			time.Sleep(delay)
		}

		fmt.Printf("   🔄 尝试 %d/%d\n", attempt+1, maxRetries+1)
		start := time.Now()

		ch, err := m.GenerateContent(ctx, request)
		if err != nil {
			lastErr = err
			fmt.Printf("   ❌ 请求失败: %v\n", err)
			continue
		}

		var success bool
		for resp := range ch {
			if resp.Error != nil {
				lastErr = fmt.Errorf("%s", resp.Error.Message)
				fmt.Printf("   ❌ API 错误: %s\n", resp.Error.Message)
				break
			}
			if resp.Done && len(resp.Choices) > 0 {
				fmt.Printf("   ✅ 成功 (耗时 %v): %s\n",
					time.Since(start).Round(time.Millisecond),
					resp.Choices[0].Message.Content)
				success = true
			}
		}

		if success {
			lastErr = nil
			break
		}
	}

	if lastErr != nil {
		fmt.Printf("\n   ❌ 所有重试均失败: %v\n", lastErr)
	} else {
		fmt.Printf("\n   ✅ 请求最终成功\n")
	}

	printDivider()
}
