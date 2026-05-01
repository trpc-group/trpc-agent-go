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
// 示例 3: 多轮对话
// ============================================================================

// RunMultiTurnExamples 运行所有多轮对话示例。
func RunMultiTurnExamples(ctx context.Context, m *bedrock.Model) {
	printHeader("多轮对话示例 (Multi-turn Conversation)")

	multiTurnContextKeeping(ctx, m)
	multiTurnLongConversation(ctx, m)
	multiTurnStateReset(ctx, m)
}

// multiTurnContextKeeping 上下文保持的连续对话。
//
// 场景：模拟真实的多轮对话，模型能够记住之前的对话内容。
// 关键点：每轮对话都将完整的消息历史传给模型。
func multiTurnContextKeeping(ctx context.Context, m *bedrock.Model) {
	printSubHeader("3.1 上下文保持的连续对话")
	start := time.Now()

	// 对话历史
	messages := []model.Message{
		model.NewSystemMessage("You are a helpful math tutor. Be concise and clear."),
	}

	// 模拟多轮对话
	turns := []string{
		"What is the Pythagorean theorem?",
		"Can you give me an example with a 3-4-5 triangle?",
		"What if one side is 5 and the hypotenuse is 13, what's the other side?",
	}

	for i, question := range turns {
		fmt.Printf("👤 用户 (第 %d 轮): %s\n", i+1, question)
		messages = append(messages, model.NewUserMessage(question))

		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages: messages,
			GenerationConfig: model.GenerationConfig{
				MaxTokens: intPtr(256),
				Stream:    true,
			},
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		fmt.Printf("🤖 助手 (第 %d 轮): ", i+1)
		var fullContent string
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("\n❌ 错误: %s\n", resp.Error.Message)
				return
			}
			if resp.IsPartial && len(resp.Choices) > 0 {
				fmt.Print(resp.Choices[0].Delta.Content)
			}
			if resp.Done && len(resp.Choices) > 0 {
				fullContent = resp.Choices[0].Message.Content
			}
		}
		fmt.Println()
		fmt.Println()

		// 将助手回复加入对话历史，保持上下文
		messages = append(messages, model.NewAssistantMessage(fullContent))
	}

	fmt.Printf("📝 对话历史共 %d 条消息（含系统消息）\n", len(messages))
	printElapsed(start)
	printDivider()
}

// multiTurnLongConversation 长对话历史管理。
//
// 场景：当对话历史过长时，需要进行截断或摘要以避免超出模型上下文窗口。
// 策略：保留系统消息 + 最近 N 轮对话。
func multiTurnLongConversation(ctx context.Context, m *bedrock.Model) {
	printSubHeader("3.2 长对话历史管理")
	start := time.Now()

	const maxHistoryTurns = 4 // 最多保留最近 4 轮对话

	systemMsg := model.NewSystemMessage(
		"You are a knowledgeable assistant. " +
			"Keep track of the conversation context. " +
			"If asked about previous topics, refer back to them.",
	)

	// 模拟一个较长的对话
	allMessages := []model.Message{systemMsg}
	questions := []string{
		"What is Go programming language?",
		"What are its main features?",
		"How does concurrency work in Go?",
		"What are channels used for?",
		"Can you compare Go with Rust?",
		"Which one should I learn first?",
	}

	for i, question := range questions {
		fmt.Printf("👤 用户 (第 %d 轮): %s\n", i+1, question)
		allMessages = append(allMessages, model.NewUserMessage(question))

		// 构建请求消息：系统消息 + 最近 N 轮
		requestMessages := buildTruncatedHistory(allMessages, systemMsg, maxHistoryTurns)
		fmt.Printf("   📋 发送消息数: %d (历史截断至最近 %d 轮)\n", len(requestMessages), maxHistoryTurns)

		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages: requestMessages,
			GenerationConfig: model.GenerationConfig{
				MaxTokens: intPtr(150),
				Stream:    true,
			},
		})
		if err != nil {
			log.Printf("❌ 请求失败: %v\n", err)
			return
		}

		fmt.Printf("🤖 助手: ")
		var fullContent string
		for resp := range ch {
			if resp.Error != nil {
				fmt.Printf("\n❌ 错误: %s\n", resp.Error.Message)
				return
			}
			if resp.IsPartial && len(resp.Choices) > 0 {
				fmt.Print(resp.Choices[0].Delta.Content)
			}
			if resp.Done && len(resp.Choices) > 0 {
				fullContent = resp.Choices[0].Message.Content
			}
		}
		fmt.Println()
		fmt.Println()

		allMessages = append(allMessages, model.NewAssistantMessage(fullContent))
	}

	fmt.Printf("📝 完整对话历史: %d 条消息\n", len(allMessages))
	printElapsed(start)
	printDivider()
}

// multiTurnStateReset 对话状态重置示例。
//
// 场景：在某些情况下需要重置对话状态，例如切换话题或开始新会话。
// 展示如何优雅地重置对话上下文。
func multiTurnStateReset(ctx context.Context, m *bedrock.Model) {
	printSubHeader("3.3 对话状态重置")
	start := time.Now()

	systemMsg := model.NewSystemMessage("You are a helpful assistant. Be concise.")

	// 第一段对话
	fmt.Println("📌 第一段对话（关于编程）:")
	messages := []model.Message{systemMsg}

	firstTopicQuestions := []string{
		"What is Python?",
		"What's it used for?",
	}

	for _, q := range firstTopicQuestions {
		fmt.Printf("   👤 %s\n", q)
		messages = append(messages, model.NewUserMessage(q))

		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages:         messages,
			GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(100)},
		})
		if err != nil {
			log.Printf("   ❌ 请求失败: %v\n", err)
			return
		}

		for resp := range ch {
			if resp.Error != nil {
				log.Printf("   ❌ 错误: %s\n", resp.Error.Message)
				return
			}
			if resp.Done && len(resp.Choices) > 0 {
				content := resp.Choices[0].Message.Content
				fmt.Printf("   🤖 %s\n", content)
				messages = append(messages, model.NewAssistantMessage(content))
			}
		}
	}

	fmt.Printf("\n🔄 重置对话状态（清除历史，切换话题）\n\n")

	// 重置：只保留系统消息，开始新话题
	messages = []model.Message{
		model.NewSystemMessage("You are a cooking expert. Give brief recipe suggestions."),
	}

	// 第二段对话
	fmt.Println("📌 第二段对话（关于烹饪）:")
	secondTopicQuestions := []string{
		"How do I make scrambled eggs?",
		"Any tips for making them fluffy?",
	}

	for _, q := range secondTopicQuestions {
		fmt.Printf("   👤 %s\n", q)
		messages = append(messages, model.NewUserMessage(q))

		ch, err := m.GenerateContent(ctx, &model.Request{
			Messages:         messages,
			GenerationConfig: model.GenerationConfig{MaxTokens: intPtr(150)},
		})
		if err != nil {
			log.Printf("   ❌ 请求失败: %v\n", err)
			return
		}

		for resp := range ch {
			if resp.Error != nil {
				log.Printf("   ❌ 错误: %s\n", resp.Error.Message)
				return
			}
			if resp.Done && len(resp.Choices) > 0 {
				content := resp.Choices[0].Message.Content
				fmt.Printf("   🤖 %s\n", content)
				messages = append(messages, model.NewAssistantMessage(content))
			}
		}
	}

	fmt.Printf("\n✅ 对话状态重置成功，两段对话互不影响\n")
	printElapsed(start)
	printDivider()
}

// buildTruncatedHistory 构建截断的对话历史。
// 保留系统消息 + 最近 maxTurns 轮对话（一轮 = 一个 user + 一个 assistant）。
func buildTruncatedHistory(allMessages []model.Message, systemMsg model.Message, maxTurns int) []model.Message {
	// 分离系统消息和对话消息
	var conversationMsgs []model.Message
	for _, msg := range allMessages {
		if msg.Role != model.RoleSystem {
			conversationMsgs = append(conversationMsgs, msg)
		}
	}

	// 计算需要保留的消息数（每轮 2 条：user + assistant）
	maxMsgs := maxTurns * 2
	if len(conversationMsgs) > maxMsgs {
		conversationMsgs = conversationMsgs[len(conversationMsgs)-maxMsgs:]
	}

	// 重新组合：系统消息 + 截断后的对话
	result := []model.Message{systemMsg}
	result = append(result, conversationMsgs...)
	return result
}
