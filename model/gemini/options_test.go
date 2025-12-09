//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

func TestOptions(t *testing.T) {
	var (
		defaultGeminiClientConfig = &genai.ClientConfig{}
	)
	tests := []struct {
		name string
		opts []Option
		want *options
	}{
		{
			name: "default",
			opts: []Option{
				WithChannelBufferSize(defaultChannelBufferSize),
				WithChatRequestCallback(nil),
				WithChatResponseCallback(nil),
				WithChatChunkCallback(nil),
				WithChatStreamCompleteCallback(nil),
				WithGeminiClientConfig(defaultGeminiClientConfig),
				WithEnableTokenTailoring(true),
				WithMaxInputTokens(1),
				WithTailoringStrategy(nil),
				WithTokenCounter(nil),
				WithTokenTailoringConfig(nil),
			},
			want: &options{
				channelBufferSize:          defaultChannelBufferSize,
				chatRequestCallback:        nil,
				chatResponseCallback:       nil,
				chatChunkCallback:          nil,
				chatStreamCompleteCallback: nil,
				enableTokenTailoring:       true,
				tokenCounter:               nil,
				tailoringStrategy:          nil,
				maxInputTokens:             1,
				tokenTailoringConfig:       nil,
				geminiClientConfig:         defaultGeminiClientConfig,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &options{}
			for _, opt := range tt.opts {
				opt(target)
			}
			assert.Equal(t, tt.want.channelBufferSize, target.channelBufferSize)
			assert.Nil(t, tt.want.chatRequestCallback)
			assert.Nil(t, tt.want.chatResponseCallback)
			assert.Nil(t, tt.want.chatChunkCallback)
			assert.Nil(t, tt.want.chatStreamCompleteCallback)
			assert.Equal(t, tt.want.enableTokenTailoring, target.enableTokenTailoring)
			assert.Nil(t, tt.want.tokenCounter)
			assert.Nil(t, tt.want.tailoringStrategy)
			assert.Equal(t, tt.want.maxInputTokens, target.maxInputTokens)
			assert.Nil(t, tt.want.tokenTailoringConfig)
			assert.Equal(t, tt.want.geminiClientConfig, target.geminiClientConfig)
		})
	}
}

func TestWithTokenTailoringConfig(t *testing.T) {
	tests := []struct {
		name        string
		inputConfig *model.TokenTailoringConfig
		initialOpts *options
		wantCfg     *model.TokenTailoringConfig
	}{
		{
			name:        "nil config",
			inputConfig: nil,
			initialOpts: &options{
				tokenTailoringConfig: &model.TokenTailoringConfig{ProtocolOverheadTokens: 5},
			},
			wantCfg: &model.TokenTailoringConfig{ProtocolOverheadTokens: 5},
		},
		{
			name:        "nil config - 1",
			inputConfig: nil,
			initialOpts: &options{},
			wantCfg:     nil,
		},
		{
			name: "empty value config",
			inputConfig: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: 0,
				ReserveOutputTokens:    0,
				SafetyMarginRatio:      0,
				InputTokensFloor:       0,
				OutputTokensFloor:      0,
				MaxInputTokensRatio:    0,
			},
			initialOpts: &options{},
			wantCfg: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: imodel.DefaultProtocolOverheadTokens,
				ReserveOutputTokens:    imodel.DefaultReserveOutputTokens,
				SafetyMarginRatio:      imodel.DefaultSafetyMarginRatio,
				InputTokensFloor:       imodel.DefaultInputTokensFloor,
				OutputTokensFloor:      imodel.DefaultOutputTokensFloor,
				MaxInputTokensRatio:    imodel.DefaultMaxInputTokensRatio,
			},
		},
		{
			name: "partial value config",
			inputConfig: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: 15,
				ReserveOutputTokens:    0,
				SafetyMarginRatio:      0.2,
				InputTokensFloor:       0,
				OutputTokensFloor:      250,
				MaxInputTokensRatio:    0,
			},
			initialOpts: &options{},
			wantCfg: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: 15,
				ReserveOutputTokens:    imodel.DefaultReserveOutputTokens,
				SafetyMarginRatio:      0.2,
				InputTokensFloor:       imodel.DefaultInputTokensFloor,
				OutputTokensFloor:      250,
				MaxInputTokensRatio:    imodel.DefaultMaxInputTokensRatio,
			},
		},
		{
			name: "all value config",
			inputConfig: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: 20,
				ReserveOutputTokens:    30,
				SafetyMarginRatio:      0.3,
				InputTokensFloor:       300,
				OutputTokensFloor:      400,
				MaxInputTokensRatio:    2.0,
			},
			initialOpts: &options{},
			wantCfg: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: 20,
				ReserveOutputTokens:    30,
				SafetyMarginRatio:      0.3,
				InputTokensFloor:       300,
				OutputTokensFloor:      400,
				MaxInputTokensRatio:    2.0,
			},
		},
		{
			name: "nil initialOpts",
			inputConfig: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: 0,
				ReserveOutputTokens:    0,
			},
			initialOpts: &options{
				tokenTailoringConfig: &model.TokenTailoringConfig{
					ProtocolOverheadTokens: 5,
					ReserveOutputTokens:    10,
				},
			},
			wantCfg: &model.TokenTailoringConfig{
				ProtocolOverheadTokens: imodel.DefaultProtocolOverheadTokens,
				ReserveOutputTokens:    imodel.DefaultReserveOutputTokens,
				SafetyMarginRatio:      imodel.DefaultSafetyMarginRatio,
				InputTokensFloor:       imodel.DefaultInputTokensFloor,
				OutputTokensFloor:      imodel.DefaultOutputTokensFloor,
				MaxInputTokensRatio:    imodel.DefaultMaxInputTokensRatio,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := WithTokenTailoringConfig(tt.inputConfig)
			opt(tt.initialOpts)

			if tt.wantCfg == nil {
				assert.Nil(t, tt.initialOpts.tokenTailoringConfig, "expectation TokenTailoringConfig is nil")
			} else {
				require.NotNil(t, tt.initialOpts.tokenTailoringConfig, "TokenTailoringConfig is not nil")

				assert.Equal(t, tt.wantCfg.ProtocolOverheadTokens, tt.initialOpts.tokenTailoringConfig.ProtocolOverheadTokens, "ProtocolOverheadTokensinconsistent")
				assert.Equal(t, tt.wantCfg.ReserveOutputTokens, tt.initialOpts.tokenTailoringConfig.ReserveOutputTokens, "ReserveOutputTokensinconsistent")
				assert.Equal(t, tt.wantCfg.SafetyMarginRatio, tt.initialOpts.tokenTailoringConfig.SafetyMarginRatio, "SafetyMarginRatio inconsistent")
				assert.Equal(t, tt.wantCfg.InputTokensFloor, tt.initialOpts.tokenTailoringConfig.InputTokensFloor, "InputTokensFloor inconsistent")
				assert.Equal(t, tt.wantCfg.OutputTokensFloor, tt.initialOpts.tokenTailoringConfig.OutputTokensFloor, "OutputTokensFloor inconsistent")
				assert.Equal(t, tt.wantCfg.MaxInputTokensRatio, tt.initialOpts.tokenTailoringConfig.MaxInputTokensRatio, "MaxInputTokensRatioinconsistent")
			}
		})
	}
}

func TestWithTokenCounter_Nil(t *testing.T) {
	opt := defaultOptions
	WithTokenCounter(nil)(&opt)
	assert.NotNil(t, opt.tokenCounter, "tokenCounter is nil")
}
