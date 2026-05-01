// Package main 演示如何使用 trpc-agent-go 的 AWS Bedrock 模型适配器。
//
// 本示例涵盖以下场景：
//   - 基础对话（单轮、系统消息、推理配置）
//   - 流式调用（文本流式、工具调用流式、混合内容流式）
//   - 多轮对话（上下文保持、长对话历史管理、状态重置）
//   - 工具调用（简单/复杂工具、结果处理、错误处理）
//   - 高级功能（图片处理、并发调用）
//   - 错误处理（API 错误、超时控制、重试机制）
//
// 支持的模型：
//   - Anthropic Claude 系列 (us.anthropic.claude-sonnet-4-20250514-v1:0)
//   - Mistral 系列 (mistral.mistral-large-3-675b-instruct)
//   - Amazon Nova 系列
//   - Meta Llama 系列
//
// 运行前请确保：
//  1. 已配置 AWS 凭证（环境变量 / ~/.aws/credentials / IAM Role）
//  2. 已在 Bedrock 控制台开通对应模型的访问权限
//
// 运行方式：
//
//	go run . -demo all
//	go run . -demo basic
//	go run . -demo streaming
//	go run . -demo multiturn
//	go run . -demo tool
//	go run . -demo advanced
//	go run . -demo error
//	go run . -model us.anthropic.claude-sonnet-4-20250514-v1:0 -region us-east-1
//	go run . -model mistral.mistral-large-3-675b-instruct -region us-east-1
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"trpc.group/trpc-go/trpc-agent-go/model/bedrock"
)

var (
	flagModel  = flag.String("model", "mistral.mistral-large-3-675b-instruct", "Bedrock 模型 ID")
	flagRegion = flag.String("region", "us-east-1", "AWS 区域")
	flagDemo   = flag.String("demo", "all", "运行的示例: basic, streaming, multiturn, tool, advanced, error, all")
)

func main() {
	flag.Parse()

	ctx := context.Background()

	// 加载 AWS 配置
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(*flagRegion))
	if err != nil {
		log.Fatalf("❌ 加载 AWS 配置失败: %v\n请确保已配置 AWS 凭证（环境变量/~/.aws/credentials/IAM Role）", err)
	}

	// 创建 Bedrock 模型
	m := bedrock.New(*flagModel, bedrock.WithAWSConfig(cfg))

	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║           AWS Bedrock 模型使用示例 (trpc-agent-go)              ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  模型: %-57s║\n", *flagModel)
	fmt.Printf("║  区域: %-57s║\n", *flagRegion)
	fmt.Printf("║  示例: %-57s║\n", *flagDemo)
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

	// 示例注册表
	demos := map[string]func(context.Context, *bedrock.Model){
		"basic":     RunBasicExamples,
		"streaming": RunStreamingExamples,
		"multiturn": RunMultiTurnExamples,
		"tool":      RunToolCallExamples,
		"advanced":  RunAdvancedExamples,
		"error":     RunErrorHandlingExamples,
	}

	// 有序执行列表
	demoOrder := []string{"basic", "streaming", "multiturn", "tool", "advanced", "error"}

	if *flagDemo == "all" {
		for _, name := range demoOrder {
			demos[name](ctx, m)
		}
	} else {
		// 支持逗号分隔的多个示例
		names := strings.Split(*flagDemo, ",")
		for _, name := range names {
			name = strings.TrimSpace(name)
			fn, ok := demos[name]
			if !ok {
				fmt.Fprintf(os.Stderr, "❌ 未知示例: %s\n", name)
				fmt.Fprintf(os.Stderr, "可选: basic, streaming, multiturn, tool, advanced, error, all\n")
				os.Exit(1)
			}
			fn(ctx, m)
		}
	}

	fmt.Println()
	fmt.Println("🎉 所有示例运行完成！")
}
