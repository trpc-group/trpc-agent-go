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

// options contains configuration options for creating a Bedrock model.
type options struct {
	// awsConfig is the AWS configuration used to create the Bedrock client.
	awsConfig aws.Config
	// bedrockOptions is the list of Bedrock client options.
	bedrockOptions []func(*bedrockruntime.Options)
	// client is a custom Bedrock client for testing or custom scenarios.
	client BedrockClient
	// channelBufferSize is the buffer size of the response channel.
	channelBufferSize int
}

var defaultOptions = options{
	channelBufferSize: defaultChannelBufferSize,
}

// Option is a function type for configuring the Bedrock model.
type Option func(*options)

// WithAWSConfig sets the AWS configuration used to create the Bedrock Runtime client.
// Typically obtained via config.LoadDefaultConfig(ctx).
//
// Example:
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

// WithBedrockOptions sets additional options for the Bedrock client.
// These options are passed when creating the Bedrock Runtime client.
//
// Example:
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

// WithClient sets a custom Bedrock client.
// When a custom client is provided, WithAWSConfig and WithBedrockOptions are ignored.
// Primarily used for testing and mock scenarios.
func WithClient(client BedrockClient) Option {
	return func(o *options) {
		o.client = client
	}
}

// WithChannelBufferSize sets the buffer size of the response channel, defaults to 256.
func WithChannelBufferSize(size int) Option {
	return func(o *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}
