package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
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
