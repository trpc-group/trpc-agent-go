package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/bedrock"
)

// ============================================================================
// 示例 5: 高级功能
// ============================================================================

// RunAdvancedExamples 运行所有高级功能示例。
func RunAdvancedExamples(ctx context.Context, m *bedrock.Model) {
	printHeader("高级功能示例 (Advanced Features)")

	advancedImageConversation(ctx, m)
	advancedConcurrentCalls(ctx, m)
}

// advancedImageConversation 图片处理对话。
//
// 场景：发送图片给模型进行分析（视觉理解）。
// Bedrock 支持通过 ContentParts 发送图片数据。
// 注意：需要使用支持视觉的模型（如 Claude 3 系列）。
func advancedImageConversation(ctx context.Context, m *bedrock.Model) {
	printSubHeader("5.1 图片处理对话")
	start := time.Now()

	// 生成一个简单的测试图片
	imgData, err := generateTestImage()
	if err != nil {
		log.Printf("❌ 生成测试图片失败: %v\n", err)
		return
	}

	fmt.Printf("📷 生成测试图片: %d bytes (100x100 渐变色块)\n\n", len(imgData))

	// 构建带图片的消息
	userMsg := model.Message{
		Role:    model.RoleUser,
		Content: "Describe this image. What colors and patterns do you see?",
	}
	userMsg.AddImageData(imgData, "auto", "png")

	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a visual analysis assistant. Describe images concisely."),
			userMsg,
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: intPtr(256),
			Stream:    true,
		},
	})
	if err != nil {
		log.Printf("❌ 请求失败: %v\n", err)
		return
	}

	fmt.Print("🤖 图片分析: ")
	for resp := range ch {
		if resp.Error != nil {
			fmt.Printf("\n❌ 错误: %s\n", resp.Error.Message)
			return
		}
		if resp.IsPartial && len(resp.Choices) > 0 {
			fmt.Print(resp.Choices[0].Delta.Content)
		}
		if resp.Done {
			fmt.Println()
		}
	}

	printElapsed(start)
	printDivider()
}

// advancedConcurrentCalls 并发调用示例。
//
// 场景：同时向模型发送多个独立请求，提高吞吐量。
// 适用于批量处理、A/B 测试、多模型对比等场景。
func advancedConcurrentCalls(ctx context.Context, m *bedrock.Model) {
	printSubHeader("5.2 并发调用示例")
	start := time.Now()

	// 定义多个并发请求
	questions := []struct {
		id       int
		question string
	}{
		{1, "What is cloud computing? (one sentence)"},
		{2, "What is serverless? (one sentence)"},
		{3, "What is containerization? (one sentence)"},
		{4, "What is microservices? (one sentence)"},
	}

	fmt.Printf("🚀 并发发送 %d 个请求...\n\n", len(questions))

	type result struct {
		id      int
		content string
		elapsed time.Duration
		err     error
	}

	results := make([]result, len(questions))
	var wg sync.WaitGroup

	for i, q := range questions {
		wg.Add(1)
		go func(idx int, question string, id int) {
			defer wg.Done()
			reqStart := time.Now()

			ch, err := m.GenerateContent(ctx, &model.Request{
				Messages: []model.Message{
					model.NewUserMessage(question),
				},
				GenerationConfig: model.GenerationConfig{
					MaxTokens: intPtr(100),
				},
			})
			if err != nil {
				results[idx] = result{id: id, err: err, elapsed: time.Since(reqStart)}
				return
			}

			var content string
			for resp := range ch {
				if resp.Error != nil {
					results[idx] = result{id: id, err: fmt.Errorf("%s", resp.Error.Message), elapsed: time.Since(reqStart)}
					return
				}
				if resp.Done && len(resp.Choices) > 0 {
					content = resp.Choices[0].Message.Content
				}
			}

			results[idx] = result{id: id, content: content, elapsed: time.Since(reqStart)}
		}(i, q.question, q.id)
	}

	wg.Wait()

	// 展示结果
	for i, r := range results {
		if r.err != nil {
			fmt.Printf("   [%d] ❌ 失败: %v (耗时 %v)\n", r.id, r.err, r.elapsed.Round(time.Millisecond))
		} else {
			fmt.Printf("   [%d] ✅ %s\n", r.id, questions[i].question)
			fmt.Printf("       🤖 %s\n", r.content)
			fmt.Printf("       ⏱️  %v\n\n", r.elapsed.Round(time.Millisecond))
		}
	}

	printElapsed(start)
	printDivider()
}

// generateTestImage 生成一个简单的测试 PNG 图片（渐变色块）。
func generateTestImage() ([]byte, error) {
	width, height := 100, 100
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := uint8(x * 255 / width)
			g := uint8(y * 255 / height)
			b := uint8(128)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	tmpFile, err := os.CreateTemp("", "bedrock-test-*.png")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if err := png.Encode(tmpFile, img); err != nil {
		return nil, err
	}

	tmpFile.Seek(0, 0)
	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		return nil, err
	}

	return data, nil
}
