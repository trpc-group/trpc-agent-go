//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewTimeRequestProcessor(t *testing.T) {
	tests := []struct {
		name           string
		opts           []TimeOption
		expectedAdd    bool
		expectedTz     string
		expectedFormat string
	}{
		{
			name:           "default values",
			opts:           []TimeOption{},
			expectedAdd:    false,
			expectedTz:     "",
			expectedFormat: "2006-01-02 15:04:05 MST",
		},
		{
			name: "with add current time",
			opts: []TimeOption{
				WithAddCurrentTime(true),
			},
			expectedAdd:    true,
			expectedTz:     "",
			expectedFormat: "2006-01-02 15:04:05 MST",
		},
		{
			name: "with timezone",
			opts: []TimeOption{
				WithTimezone("UTC"),
			},
			expectedAdd:    false,
			expectedTz:     "UTC",
			expectedFormat: "2006-01-02 15:04:05 MST",
		},
		{
			name: "with custom format",
			opts: []TimeOption{
				WithTimeFormat("2006-01-02"),
			},
			expectedAdd:    false,
			expectedTz:     "",
			expectedFormat: "2006-01-02",
		},
		{
			name: "with all options",
			opts: []TimeOption{
				WithAddCurrentTime(true),
				WithTimezone("Asia/Shanghai"),
				WithTimeFormat("15:04:05"),
			},
			expectedAdd:    true,
			expectedTz:     "Asia/Shanghai",
			expectedFormat: "15:04:05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewTimeRequestProcessor(tt.opts...)
			if processor.AddCurrentTime != tt.expectedAdd {
				t.Errorf("AddCurrentTime = %v, want %v", processor.AddCurrentTime, tt.expectedAdd)
			}
			if processor.Timezone != tt.expectedTz {
				t.Errorf("Timezone = %v, want %v", processor.Timezone, tt.expectedTz)
			}
			if processor.TimeFormat != tt.expectedFormat {
				t.Errorf("TimeFormat = %v, want %v", processor.TimeFormat, tt.expectedFormat)
			}
		})
	}
}

func TestTimeRequestProcessor_ProcessRequest_Disabled(t *testing.T) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(false))
	req := &model.Request{
		Messages: []model.Message{},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	// Should not add any messages when disabled.
	if len(req.Messages) != 0 {
		t.Errorf("Expected no messages, got %d", len(req.Messages))
	}

	// Should not send any events.
	select {
	case <-ch:
		t.Error("Expected no events to be sent")
	default:
		// This is expected
	}
}

func TestTimeRequestProcessor_ProcessRequest_Enabled(t *testing.T) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	req := &model.Request{
		Messages: []model.Message{},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	// Should add a system message with time.
	if len(req.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(req.Messages))
	}

	msg := req.Messages[0]
	if msg.Role != model.RoleSystem {
		t.Errorf("Expected system message, got %s", msg.Role)
	}

	if !strings.Contains(msg.Content, "The current time is:") {
		t.Errorf("Expected time content, got: %s", msg.Content)
	}
}

func TestTimeRequestProcessor_ProcessRequest_WithExistingSystemMessage(t *testing.T) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("Existing system message"),
		},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	// Should still have only 1 message.
	if len(req.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(req.Messages))
	}

	msg := req.Messages[0]
	if !strings.Contains(msg.Content, "Existing system message") {
		t.Errorf("Expected existing content to be preserved, got: %s", msg.Content)
	}

	if !strings.Contains(msg.Content, "The current time is:") {
		t.Errorf("Expected time content to be added, got: %s", msg.Content)
	}
}

func TestTimeRequestProcessor_ProcessRequest_AppendsToLastSystem(
	t *testing.T,
) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system 1"),
			model.NewSystemMessage("system 2"),
			model.NewUserMessage("hello"),
		},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	if len(req.Messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(req.Messages))
	}

	if strings.Contains(req.Messages[0].Content, "The current time is:") {
		t.Errorf(
			"Expected time appended to last system message, got: %s",
			req.Messages[0].Content,
		)
	}
	if !strings.Contains(req.Messages[1].Content, "The current time is:") {
		t.Errorf(
			"Expected time appended to last system message, got: %s",
			req.Messages[1].Content,
		)
	}
}

func TestTimeRequestProcessor_ProcessRequest_SetsEmptyLastSystem(
	t *testing.T,
) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system 1"),
			model.NewSystemMessage(""),
			model.NewUserMessage("hello"),
		},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	if len(req.Messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(req.Messages))
	}

	if strings.Contains(req.Messages[0].Content, "The current time is:") {
		t.Errorf(
			"Expected time set on last system message, got: %s",
			req.Messages[0].Content,
		)
	}
	if !strings.HasPrefix(
		req.Messages[1].Content,
		"The current time is:",
	) {
		t.Errorf(
			"Expected time set on last system message, got: %s",
			req.Messages[1].Content,
		)
	}
}

func TestTimeRequestProcessor_ProcessRequest_WithTimezone(t *testing.T) {
	processor := NewTimeRequestProcessor(
		WithAddCurrentTime(true),
		WithTimezone("UTC"),
	)
	req := &model.Request{
		Messages: []model.Message{},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	msg := req.Messages[0]
	if !strings.Contains(msg.Content, "UTC") {
		t.Errorf("Expected UTC timezone, got: %s", msg.Content)
	}
}

func TestTimeRequestProcessor_ProcessRequest_WithCustomFormat(t *testing.T) {
	processor := NewTimeRequestProcessor(
		WithAddCurrentTime(true),
		WithTimeFormat("2006-01-02"),
	)
	req := &model.Request{
		Messages: []model.Message{},
	}
	ch := make(chan *event.Event, 1)

	processor.ProcessRequest(context.Background(), nil, req, ch)

	msg := req.Messages[0]
	// Should contain date in YYYY-MM-DD format.
	if !strings.Contains(msg.Content, time.Now().Format("2006-01-02")) {
		t.Errorf("Expected custom date format, got: %s", msg.Content)
	}
}

func TestTimeRequestProcessor_ProcessRequest_WithInvocation(t *testing.T) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	req := &model.Request{
		Messages: []model.Message{},
	}
	ch := make(chan *event.Event, 1)
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
	}

	processor.ProcessRequest(context.Background(), invocation, req, ch)
}

func TestTimeRequestProcessor_ProcessRequest_NilRequest(t *testing.T) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	ch := make(chan *event.Event, 1)

	// Should not panic with nil request.
	processor.ProcessRequest(context.Background(), nil, nil, ch)

	// Should not send any events.
	select {
	case <-ch:
		t.Error("Expected no events to be sent")
	default:
		// This is expected
	}
}

func TestTimeRequestProcessor_ProcessRequest_ContextCancelled(t *testing.T) {
	processor := NewTimeRequestProcessor(WithAddCurrentTime(true))
	req := &model.Request{
		Messages: []model.Message{},
	}
	ch := make(chan *event.Event, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	processor.ProcessRequest(ctx, nil, req, ch)

	// Should handle cancelled context gracefully.
	// The time message should still be added even if event sending fails.
	if len(req.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(req.Messages))
	}
}

func TestTimeRequestProcessor_GetCurrentTime(t *testing.T) {
	tests := []struct {
		name     string
		timezone string
		format   string
	}{
		{
			name:     "local timezone",
			timezone: "",
			format:   "2006-01-02 15:04:05 MST",
		},
		{
			name:     "UTC timezone",
			timezone: "UTC",
			format:   "2006-01-02 15:04:05 MST",
		},
		{
			name:     "custom format",
			timezone: "",
			format:   "2006-01-02",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &TimeRequestProcessor{
				AddCurrentTime: true,
				Timezone:       tt.timezone,
				TimeFormat:     tt.format,
			}

			result := processor.getCurrentTime()
			if result == "" {
				t.Error("Expected non-empty time string")
			}

			// Verify the format is correct.
			if tt.format == "2006-01-02" {
				// Should be able to parse the result as a date.
				_, err := time.Parse("2006-01-02", result)
				if err != nil {
					t.Errorf("Expected valid date format, got error: %v", err)
				}
			}
		})
	}
}

func TestTimeRequestProcessor_InvalidTimezone(t *testing.T) {
	processor := &TimeRequestProcessor{
		AddCurrentTime: true,
		Timezone:       "Invalid/Timezone",
		TimeFormat:     "2006-01-02 15:04:05 MST",
	}

	result := processor.getCurrentTime()
	if result == "" {
		t.Error("Expected non-empty time string even with invalid timezone")
	}

	// Should fall back to UTC format.
	if !strings.Contains(result, "UTC") {
		t.Errorf("Expected UTC fallback, got: %s", result)
	}
}
