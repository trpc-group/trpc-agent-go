//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package updatepolicy carries the built-in extractor's update policy across
// the memory package boundary without exposing policy discovery publicly.
package updatepolicy

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	negatedDestructiveRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(
			`(?i)\b(?:do\s+not|don't|dont|never|should\s+not|shouldn't)\s+(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(
			`(?i)\b(?:do\s+not|don't|dont)\s+(?:want|need)\s+(?:me\s+|you\s+)?to\s+(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(`(?:不要|别|请勿|不必|不应该)(?:再)?(?:忘记|删除|移除|清除|清空)`),
	}
	destructiveRequestCancellationPatterns = []*regexp.Regexp{
		regexp.MustCompile(
			`(?i)^\s*(?:actually[,.]?\s*)?(?:never\s*mind(?:[,.]?\s*keep\s+(?:it|that|them))?|cancel\s+(?:that|it|the\s+request)|keep\s+(?:it|that|them)|do\s+nothing\s+with\s+(?:it|that|them)|do\s+not\s+(?:do|process|change)\s+(?:it|that|them))\s*[.!?]*\s*$`,
		),
		regexp.MustCompile(
			`^\s*(?:(?:还是|那就)?算了(?:[，,\s]*(?:保留(?:它|这些|原样)?|不要(?:处理|操作|改动)(?:它|这些|任何内容)?))?|取消(?:刚才|之前)?(?:的)?(?:请求|操作)?|保留(?:它|这些|原样)?|不要(?:处理|操作|改动)(?:它|这些|任何内容)?)\s*[。！？!?]*\s*$`,
		),
	}
	explicitDestructiveRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?im)^\s*(?:please\s+)?(?:forget|delete|remove|erase|clear)\b`),
		regexp.MustCompile(`(?i)\bplease\s+(?:forget|delete|remove|erase|clear)\b`),
		regexp.MustCompile(
			`(?i)\b(?:can|could|would|will)\s+you\s+(?:please\s+)?(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(
			`(?i)\bi\s+(?:want|need|would\s+like)\s+you\s+to\s+(?:forget|delete|remove|erase|clear)\b`,
		),
		regexp.MustCompile(
			`(?:^|[\n。！？!?])\s*(?:(?:请|麻烦)(?:你)?|帮我)?(?:忘记|删除|移除|清除|清空)`,
		),
		regexp.MustCompile(`(?:我(?:想|希望|要求)(?:让)?你|请你|麻烦你|帮我)(?:忘记|删除|移除|清除|清空)`),
	}
	explicitClearAllRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(
			`(?i)\bforget\s+(?:(?:absolutely\s+)?everything|all(?:\s+of)?\s+(?:my\s+)?(?:stored\s+)?(?:memories|memory|data|information))\b`,
		),
		regexp.MustCompile(
			`(?i)\b(?:delete|remove|erase|clear)\s+(?:(?:all(?:\s+of)?\s+)(?:my\s+)?(?:stored\s+)?(?:memories|memory|data|information)|(?:my\s+)?memories|everything)\b`,
		),
		regexp.MustCompile(`忘记(?:关于我(?:的)?)?(?:一切|全部|所有)(?:已(?:经)?(?:存储|保存)(?:的)?)?(?:记忆|信息|数据)?`),
		regexp.MustCompile(`(?:删除|移除|清除|清空)(?:我(?:的)?)?(?:全部|所有)(?:的)?(?:已(?:经)?(?:存储|保存)(?:的)?)?(?:记忆|信息|数据)`),
		regexp.MustCompile(`清空(?:我(?:的)?)?(?:全部|所有)?(?:的)?(?:已(?:经)?(?:存储|保存)(?:的)?)?(?:记忆|信息|数据|记忆库)`),
	}
	partialClearRequestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:except|excluding|other\s+than|but\s+keep)\b`),
		regexp.MustCompile(`(?:除了|除去|排除).{0,12}(?:以外|之外|外)?|(?:但是|但要|保留).{0,12}`),
	}
)

// Value identifies an update policy configured by a built-in extractor.
type Value string

// DestructiveRequest describes the latest explicit user request to remove
// memory. It is internal to the memory implementation.
type DestructiveRequest struct {
	Text     string
	Explicit bool
	ClearAll bool
	Partial  bool
	Index    int
}

type provider interface {
	ConfiguredUpdatePolicy() Value
}

type workerConfigurationKey struct{}

// From returns the policy carried by a built-in extractor.
func From(value any) Value {
	provider, ok := value.(provider)
	if !ok {
		return ""
	}
	return provider.ConfiguredUpdatePolicy()
}

// WithWorkerConfiguration records the policy selected by an Auto memory
// worker for one extraction call.
func WithWorkerConfiguration(ctx context.Context, policy Value) context.Context {
	return context.WithValue(ctx, workerConfigurationKey{}, policy)
}

// WorkerConfiguration returns the policy supplied by an Auto memory worker.
// The second result is false for direct extractor calls outside a worker.
func WorkerConfiguration(ctx context.Context) (Value, bool) {
	policy, ok := ctx.Value(workerConfigurationKey{}).(Value)
	return policy, ok
}

// LatestDestructiveRequest returns the latest active destructive request and
// its message index. A later cancellation supersedes an earlier request.
func LatestDestructiveRequest(messages []model.Message) DestructiveRequest {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != model.RoleUser {
			continue
		}
		request, terminal := classifyDestructiveRequest(messageText(message), index)
		if terminal {
			return request
		}
	}
	return DestructiveRequest{}
}

// LatestUserDestructiveRequest classifies only the latest user instruction.
// It is used when operations lack source-turn provenance, so an earlier request
// cannot authorize destructive operations for a later user turn.
func LatestUserDestructiveRequest(messages []model.Message) DestructiveRequest {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != model.RoleUser {
			continue
		}
		request, _ := classifyDestructiveRequest(messageText(messages[index]), index)
		return request
	}
	return DestructiveRequest{}
}

func classifyDestructiveRequest(text string, index int) (DestructiveRequest, bool) {
	if matchesAnyPattern(text, destructiveRequestCancellationPatterns) {
		return DestructiveRequest{}, true
	}
	for _, pattern := range negatedDestructiveRequestPatterns {
		if pattern.MatchString(text) {
			return DestructiveRequest{}, true
		}
	}
	if !matchesAnyPattern(text, explicitDestructiveRequestPatterns) {
		return DestructiveRequest{}, false
	}
	return DestructiveRequest{
		Text:     text,
		Explicit: true,
		ClearAll: matchesAnyPattern(text, explicitClearAllRequestPatterns),
		Partial:  matchesAnyPattern(text, partialClearRequestPatterns),
		Index:    index,
	}, true
}

func messageText(message model.Message) string {
	parts := make([]string, 0, len(message.ContentParts)+1)
	if text := strings.TrimSpace(message.Content); text != "" {
		parts = append(parts, text)
	}
	for _, part := range message.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if text := strings.TrimSpace(*part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func matchesAnyPattern(text string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}
