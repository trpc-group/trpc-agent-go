//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"testing"
)

func TestWithStreamingChannelBufSize(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   int
		wantBufSize int
		description string
	}{
		{
			name:        "positive buffer size",
			inputSize:   1024,
			wantBufSize: 1024,
			description: "应该设置指定的正数缓冲区大小",
		},
		{
			name:        "zero buffer size",
			inputSize:   0,
			wantBufSize: 0,
			description: "应该允许设置为0",
		},
		{
			name:        "negative size uses default",
			inputSize:   -1,
			wantBufSize: defaultStreamingChannelSize,
			description: "负数输入应该使用默认大小",
		},
		{
			name:        "large buffer size",
			inputSize:   65536,
			wantBufSize: 65536,
			description: "应该处理较大的缓冲区大小",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建 agent 实例
			agent := &A2AAgent{
				// 初始化其他必要字段
			}

			// 应用选项
			option := WithStreamingChannelBufSize(tt.inputSize)
			option(agent)

			// 验证结果
			if agent.streamingBufSize != tt.wantBufSize {
				t.Errorf("%s: got buf size %d, want %d",
					tt.description, agent.streamingBufSize, tt.wantBufSize)
			}
		})
	}
}
