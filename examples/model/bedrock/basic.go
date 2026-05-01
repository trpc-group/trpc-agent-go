package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/bedrock"
)

// ============================================================================
// 示例 1: 基础对话
// ============================================================================

// RunBasicExamples 运行所有基础对话示例。
func RunBasicExamples(ctx context.Context, m *bedrock.Model) {
	printHeader("基础对话示例 (Basic Conversation)")

	basicSimpleConversation(ctx, m)
	basicWithSystemMessage(ctx, m)
	basicWithInferenceConfig(ctx, m)
}

// basicSimpleConversation 最简单的单轮文本对话。
//
// 场景：用户发送一条消息，模型返回一条回复。
// 这是最基本的使用方式，适合简单的问答场景。
func basicSimpleConversation(ctx context.Context, m *bedrock.Model) {
	printSubHeader("1.1 简单单轮对话")
	start := time.Now()

	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("What is AWS Bedrock? Answer in 2-3 sentences."),
		},
	})
	if err != nil {
		log.Printf("❌ 请求失败: %v\n", err)
		return
	}

	for resp := range ch {
		if resp.Error != nil {
			log.Printf("❌ API 错误: %s\n", resp.Error.Message)
			return
		}
		if len(resp.Choices) > 0 {
			fmt.Printf("🤖 回复: %s\n", resp.Choices[0].Message.Content)
			printFinishReason(resp.Choices[0].FinishReason)
		}
		printUsage(resp.Usage)
	}

	printElapsed(start)
	printDivider()
}

// basicWithSystemMessage 带系统消息的对话。
//
// 场景：通过 system message 设定模型的角色和行为约束。
// 系统消息可以控制模型的回复风格、语言、专业领域等。
func basicWithSystemMessage(ctx context.Context, m *bedrock.Model) {
	printSubHeader("1.2 带系统消息的对话")
	start := time.Now()

	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(
				"You are a senior Go developer. " +
					"Answer questions concisely with code examples when appropriate. " +
					"Always use idiomatic Go patterns.",
			),
			model.NewUserMessage("How do I implement a graceful shutdown in a Go HTTP server?"),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: intPtr(512),
		},
	})
	if err != nil {
		log.Printf("❌ 请求失败: %v\n", err)
		return
	}

	for resp := range ch {
		if resp.Error != nil {
			log.Printf("❌ API 错误: %s\n", resp.Error.Message)
			return
		}
		if resp.Done && len(resp.Choices) > 0 {
			fmt.Printf("🤖 Go 专家回复:\n%s\n", resp.Choices[0].Message.Content)
			printFinishReason(resp.Choices[0].FinishReason)
		}
		printUsage(resp.Usage)
	}

	printElapsed(start)
	printDivider()
}

// basicWithInferenceConfig 带推理配置的对话。
//
// 场景：通过调整 Temperature、TopP、MaxTokens 等参数控制生成行为。
// - Temperature 越高，回复越有创意但可能不够精确
// - Temperature 越低，回复越确定性和一致性
// - TopP 控制核采样的概率阈值
func basicWithInferenceConfig(ctx context.Context, m *bedrock.Model) {
	printSubHeader("1.3 不同推理配置对比")

	configs := []struct {
		name        string
		temperature float64
		topP        float64
		maxTokens   int
		desc        string
	}{
		{"保守模式", 0.1, 0.8, 100, "低温度 + 低 TopP → 确定性强、回复简短"},
		{"平衡模式", 0.5, 0.9, 200, "中等温度 → 兼顾准确性和多样性"},
		{"创意模式", 1.0, 0.95, 300, "高温度 + 高 TopP → 更有创意和多样性"},
	}

	for _, cfg := range configs {
		fmt.Printf("\n🎨 [%s] %s\n", cfg.name, cfg.desc)
		fmt.Printf("   Temperature=%.1f, TopP=%.1f, MaxTokens=%d\n", cfg.temperature, cfg.topP, cfg.maxTokens)

		start := time.Now()
		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("Write a one-sentence description of cloud computing."),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: float64Ptr(cfg.temperature),
				TopP:        float64Ptr(cfg.topP),
				MaxTokens:   intPtr(cfg.maxTokens),
			},
		})
		if err != nil {
			log.Printf("   ❌ 请求失败: %v\n", err)
			continue
		}

		for resp := range ch {
			if resp.Error != nil {
				log.Printf("   ❌ API 错误: %s\n", resp.Error.Message)
				break
			}
			if resp.Done && len(resp.Choices) > 0 {
				fmt.Printf("   🤖 回复: %s\n", resp.Choices[0].Message.Content)
			}
		}
		printElapsed(start)
	}

	printDivider()
}
