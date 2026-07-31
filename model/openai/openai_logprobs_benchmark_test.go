//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openai

import (
	"testing"

	openaigo "github.com/openai/openai-go"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	benchmarkLogprobTokens = 1024
	benchmarkTopLogprobs   = 20
)

var benchmarkLogprobsResult *model.Logprobs

func BenchmarkConvertChatCompletionChoiceLogprobs(b *testing.B) {
	logprobs := benchmarkChoiceLogprobs(benchmarkLogprobTokens, benchmarkTopLogprobs)

	b.Run("per_slice_allocations", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkLogprobsResult = convertChatCompletionChoiceLogprobsPerSlice(logprobs)
		}
	})

	b.Run("per_token_bytes_arena", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkLogprobsResult = convertChatCompletionChoiceLogprobs(logprobs)
		}
	})
}

func benchmarkChoiceLogprobs(
	tokenCount int,
	topLogprobs int,
) openaigo.ChatCompletionChoiceLogprobs {
	content := make([]openaigo.ChatCompletionTokenLogprob, tokenCount)
	for i := range content {
		top := make([]openaigo.ChatCompletionTokenLogprobTopLogprob, topLogprobs)
		for j := range top {
			top[j] = openaigo.ChatCompletionTokenLogprobTopLogprob{
				Token:   "alternative",
				Logprob: -float64(j + 1),
				Bytes:   []int64{97, 108, 116, 10},
			}
		}
		content[i] = openaigo.ChatCompletionTokenLogprob{
			Token:       "token",
			Logprob:     -0.1,
			Bytes:       []int64{116, 111, 107, 101, 110},
			TopLogprobs: top,
		}
	}
	return openaigo.ChatCompletionChoiceLogprobs{Content: content}
}

// convertChatCompletionChoiceLogprobsPerSlice preserves the previous
// implementation as a benchmark baseline. It allocates a separate []int for
// every generated token and top-logprob candidate.
func convertChatCompletionChoiceLogprobsPerSlice(
	logprobs openaigo.ChatCompletionChoiceLogprobs,
) *model.Logprobs {
	if len(logprobs.Content) == 0 {
		return nil
	}
	converted := &model.Logprobs{
		Content: make([]model.TokenLogprob, len(logprobs.Content)),
	}
	for i, token := range logprobs.Content {
		converted.Content[i] = model.TokenLogprob{
			Token:       token.Token,
			Logprob:     token.Logprob,
			Bytes:       convertLogprobBytesPerSlice(token.Bytes),
			TopLogprobs: make([]model.TopLogprob, len(token.TopLogprobs)),
		}
		for j, top := range token.TopLogprobs {
			converted.Content[i].TopLogprobs[j] = model.TopLogprob{
				Token:   top.Token,
				Logprob: top.Logprob,
				Bytes:   convertLogprobBytesPerSlice(top.Bytes),
			}
		}
	}
	return converted
}

func convertLogprobBytesPerSlice(values []int64) []int {
	if values == nil {
		return nil
	}
	converted := make([]int, len(values))
	for i, value := range values {
		converted[i] = int(value)
	}
	return converted
}
