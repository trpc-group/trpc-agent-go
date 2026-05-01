//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package bedrock provides an AWS Bedrock-compatible model implementation.
package bedrock

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

const (
	defaultChannelBufferSize = 256
)

// options 包含创建 Bedrock 模型的配置选项。
type options struct {
	// AWS 配置，用于创建 Bedrock 客户端
	awsConfig aws.Config
	// Bedrock 客户端选项
	bedrockOptions []func(*bedrockruntime.Options)
	// 自定义 Bedrock 客户端（用于测试或自定义场景）
	client BedrockClient
	// 响应通道缓冲区大小
	channelBufferSize int
}

var defaultOptions = options{
	channelBufferSize: defaultChannelBufferSize,
}

// Option 是配置 Bedrock 模型的函数类型。
type Option func(*options)

// WithAWSConfig 设置 AWS 配置，用于创建 Bedrock Runtime 客户端。
// 通常通过 config.LoadDefaultConfig(ctx) 获取。
//
// 示例:
//
//	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
//	if err != nil {
//	    log.Fatal(err)
//	}
//	m := bedrock.New("anthropic.claude-3-sonnet-20240229-v1:0",
//	    bedrock.WithAWSConfig(cfg),
//	)
func WithAWSConfig(cfg aws.Config) Option {
	return func(o *options) {
		o.awsConfig = cfg
	}
}

// WithBedrockOptions 设置 Bedrock 客户端的额外选项。
// 这些选项会在创建 Bedrock Runtime 客户端时传入。
//
// 示例:
//
//	m := bedrock.New("anthropic.claude-3-sonnet-20240229-v1:0",
//	    bedrock.WithAWSConfig(cfg),
//	    bedrock.WithBedrockOptions(func(o *bedrockruntime.Options) {
//	        o.Region = "us-west-2"
//	    }),
//	)
func WithBedrockOptions(opts ...func(*bedrockruntime.Options)) Option {
	return func(o *options) {
		o.bedrockOptions = append(o.bedrockOptions, opts...)
	}
}

// WithClient 设置自定义的 Bedrock 客户端。
// 当提供自定义客户端时，WithAWSConfig 和 WithBedrockOptions 将被忽略。
// 主要用于测试和 mock 场景。
func WithClient(client BedrockClient) Option {
	return func(o *options) {
		o.client = client
	}
}

// WithChannelBufferSize 设置响应通道的缓冲区大小，默认为 256。
func WithChannelBufferSize(size int) Option {
	return func(o *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}
