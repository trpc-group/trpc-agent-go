//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestFakeSummarizer(t *testing.T) {
	summarizer := FakeSummarizer{}
	if !summarizer.ShouldSummarize(&session.Session{ID: "s1"}) {
		t.Fatal("应始终触发摘要")
	}
	// 临时 summary session 的 ID 形如 "<baseID>:<filterKey>"，用于还原 filter key。
	sess := &session.Session{
		ID:     "s1:weather",
		Events: []event.Event{{ID: "e1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "你好"}}}}}},
	}
	text, err := summarizer.Summarize(context.Background(), sess)
	if err != nil {
		t.Fatalf("摘要失败: %v", err)
	}
	if !strings.Contains(text, "filter=weather") || !strings.Contains(text, "e1") {
		t.Fatalf("摘要应反映 filter key 与事件输入: %q", text)
	}
	if _, err := summarizer.Summarize(context.Background(), nil); err == nil {
		t.Fatal("nil session 应返回错误")
	}
	summarizer.SetPrompt("prompt")
	summarizer.SetModel(nil)
	if summarizer.Metadata() != nil {
		t.Fatal("metadata 应为 nil")
	}
}

// TestFakeSummarizerReflectsInputEvents 断言摘要内容随输入事件集合变化，
// 后端使用陈旧事件集或缺失刷新时会被检出。
func TestFakeSummarizerReflectsInputEvents(t *testing.T) {
	summarizer := FakeSummarizer{}
	base := &session.Session{
		ID: "s1:",
		Events: []event.Event{
			{ID: "e1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "第一轮"}}}}},
			{ID: "e2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "第一答"}}}}},
		},
	}
	stale, err := summarizer.Summarize(context.Background(), base)
	if err != nil {
		t.Fatalf("摘要失败: %v", err)
	}

	// 第二次摘要应包含新增事件，证明刷新确实发生。
	refreshed := base.Clone()
	refreshed.Events = append(refreshed.Events, event.Event{
		ID: "e3", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "第二轮"}}}},
	})
	second, err := summarizer.Summarize(context.Background(), refreshed)
	if err != nil {
		t.Fatalf("摘要失败: %v", err)
	}
	if second == stale {
		t.Fatalf("强制刷新后摘要内容应反映新增事件: stale=%q second=%q", stale, second)
	}
	if !strings.Contains(second, "e3") {
		t.Fatalf("第二次摘要应包含新事件 e3: %q", second)
	}
}

// TestFakeSummarizerReflectsFilterKey 断言摘要包含调用方请求的 filter key，
// 后端使用了错误的 filter-key 时会被检出。
func TestFakeSummarizerReflectsFilterKey(t *testing.T) {
	summarizer := FakeSummarizer{}
	events := []event.Event{
		{ID: "e1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "查天气"}}}}},
	}
	weather, err := summarizer.Summarize(context.Background(), &session.Session{ID: "s1:weather", Events: events})
	if err != nil {
		t.Fatalf("摘要失败: %v", err)
	}
	profile, err := summarizer.Summarize(context.Background(), &session.Session{ID: "s1:profile", Events: events})
	if err != nil {
		t.Fatalf("摘要失败: %v", err)
	}
	if weather == profile {
		t.Fatalf("不同 filter key 的摘要应不同: weather=%q profile=%q", weather, profile)
	}
	if !strings.Contains(weather, "filter=weather") || !strings.Contains(profile, "filter=profile") {
		t.Fatalf("摘要应包含 filter key: weather=%q profile=%q", weather, profile)
	}
}
