//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package normalize

import (
	"encoding/json"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestFromSessionNil(t *testing.T) {
	snapshot := FromSession(nil)
	if snapshot.SessionId != "" || len(snapshot.Events) != 0 {
		t.Fatalf("nil session 应返回空快照: %+v", snapshot)
	}
}

func TestFromSessionAndNormalizeHelpers(t *testing.T) {
	updatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoffAt := updatedAt.Add(time.Hour)
	sess := &session.Session{
		ID: "session-1",
		Events: []event.Event{{
			ID: "event-1",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "hello",
					ToolCalls: []model.ToolCall{{
						ID: "call-1",
						Function: model.FunctionDefinitionParam{
							Name: "weather", Arguments: []byte(`{"city":"北京"}`),
						},
					}},
				},
			}}},
			StateDelta: map[string][]byte{"k": []byte("v")},
			Extensions: map[string]json.RawMessage{"x": []byte(`{"v":1}`)},
		}},
		State: session.StateMap{"weather": []byte("sunny")},
		Summaries: map[string]*session.Summary{
			"weather": {
				Summary:   "天气摘要",
				UpdatedAt: updatedAt,
				Boundary: &session.SummaryBoundary{
					Version: 1, CutoffAt: cutoffAt, LastEventID: "event-1",
				},
			},
		},
		Tracks: map[session.Track]*session.TrackEvents{
			"tool": {Events: []session.TrackEvent{{
				Track: "tool", Payload: []byte(`{"status":"ok"}`),
			}}},
		},
	}

	snapshot := FromSession(sess)
	if snapshot.SessionId != "session-1" ||
		snapshot.State["weather"] != "sunny" ||
		snapshot.Events[0].Content != "hello" ||
		snapshot.Events[0].ToolCalls[0].Name != "weather" ||
		snapshot.Events[0].StateDelta["k"] != "v" ||
		snapshot.Events[0].Extensions["x"] != `{"v":1}` {
		t.Fatalf("session 归一化错误: %+v", snapshot)
	}
	if summary, ok := snapshot.Summaries["weather"]; !ok ||
		!summary.UpdatedAtSet || !summary.CutoffAtSet || summary.LastEventID != "event-1" {
		t.Fatalf("summary 归一化错误: %+v", snapshot.Summaries)
	}
	if len(snapshot.Tracks["tool"]) != 1 || snapshot.Tracks["tool"][0].Payload == "" {
		t.Fatalf("track 归一化错误: %+v", snapshot.Tracks)
	}
}

func TestNormalizeEventWithoutMessage(t *testing.T) {
	evt := NormalizeEvent(0, event.Event{
		ID:         "event-1",
		Author:     "agent",
		StateDelta: map[string][]byte{"k": []byte("v")},
		Extensions: map[string]json.RawMessage{"x": []byte(`{"v":1}`)},
	})
	if evt.Index != 0 || evt.ID != "event-1" || evt.Author != "agent" {
		t.Fatalf("无响应事件应保留元数据: %+v", evt)
	}
	if evt.StateDelta["k"] != "v" || evt.Extensions["x"] != `{"v":1}` {
		t.Fatalf("无响应事件应保留 state/extensions: %+v", evt)
	}
	if evt.Role != "" || evt.Content != "" {
		t.Fatalf("无响应事件不应填充消息字段: %+v", evt)
	}
}

func TestNormalizeStateSkipsNilValues(t *testing.T) {
	state := NormalizeState(session.StateMap{"keep": []byte("v"), "drop": nil})
	if len(state) != 1 || state["keep"] != "v" {
		t.Fatalf("应跳过 nil 状态值: %+v", state)
	}
}

func TestNormalizeSummariesNilSession(t *testing.T) {
	if got := NormalizeSummaries(nil); len(got) != 0 {
		t.Fatalf("nil session 应返回空 map: %+v", got)
	}
}

func TestNormalizeTracksNilSession(t *testing.T) {
	if got := NormalizeTracks(nil); len(got) != 0 {
		t.Fatalf("nil session 应返回空 map: %+v", got)
	}
}

func TestNormalizeSummariesSkipsNilEntry(t *testing.T) {
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"skip": nil,
			"keep": {Summary: "ok"},
		},
	}
	got := NormalizeSummaries(sess)
	if len(got) != 1 || got["keep"].Text != "ok" {
		t.Fatalf("应跳过 nil summary: %+v", got)
	}
}

func TestNormalizeTracksSkipsNilHistory(t *testing.T) {
	sess := &session.Session{
		Tracks: map[session.Track]*session.TrackEvents{
			"skip": nil,
			"keep": {Events: []session.TrackEvent{{
				Track: "keep", Payload: []byte(`{"ok":true}`),
			}}},
		},
	}
	got := NormalizeTracks(sess)
	if len(got) != 1 || len(got["keep"]) != 1 {
		t.Fatalf("应跳过 nil track history: %+v", got)
	}
}

func TestNormalizeEventOmitsEmptyMaps(t *testing.T) {
	evt := NormalizeEvent(0, event.Event{
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hi"},
		}}},
	})
	if evt.StateDelta != nil || evt.Extensions != nil {
		t.Fatalf("空 map 应归一化为 nil: %+v", evt)
	}
}
