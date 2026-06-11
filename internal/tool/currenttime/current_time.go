//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package currenttime provides the internal environment_context_current_time
// framework tool.
package currenttime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// ToolName is the built-in exact current time tool name.
	ToolName = "environment_context_current_time"

	defaultFormat = "2006-01-02 15:04:05 MST"
)

// Request is the input payload for the environment_context_current_time tool.
type Request struct {
	// Timezone is an optional IANA timezone name such as UTC,
	// Asia/Shanghai, or America/New_York. Empty uses the agent default timezone,
	// falling back to the machine local time.
	Timezone string `json:"timezone,omitempty"`
}

// Response is the environment_context_current_time tool result.
type Response struct {
	// CurrentTime is the formatted current time.
	CurrentTime string `json:"current_time"`
	// Timezone is the timezone used for CurrentTime.
	Timezone string `json:"timezone"`
	// Note reminds the model not to treat the result as durable memory.
	Note string `json:"note"`
}

// Tool returns the current date/time for the requested timezone.
type Tool struct {
	defaultTimezone string
	now             func() time.Time
}

// New creates an environment_context_current_time tool.
func New(defaultTimezone string) *Tool {
	return &Tool{
		defaultTimezone: strings.TrimSpace(defaultTimezone),
		now:             time.Now,
	}
}

// Declaration implements tool.Tool.
func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: ToolName,
		Description: "Built-in tool to get exact current date/time when " +
			"clock-level precision is required. The result is valid only " +
			"for the current request; call this tool again instead of " +
			"reusing older time results.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"timezone": {
					Type: "string",
					Description: "Optional IANA timezone name, for example " +
						"UTC, Asia/Shanghai, or America/New_York. Empty uses " +
						"the agent default timezone, then the machine local " +
						"timezone.",
				},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *Tool) Call(_ context.Context, jsonArgs []byte) (any, error) {
	var req Request
	if len(bytes.TrimSpace(jsonArgs)) > 0 {
		if err := json.Unmarshal(jsonArgs, &req); err != nil {
			return nil, fmt.Errorf("invalid request format: %w", err)
		}
	}

	loc, timezone := t.resolveLocation(req.Timezone)
	return Response{
		CurrentTime: t.now().In(loc).Format(defaultFormat),
		Timezone:    timezone,
		Note: "This time value is valid only for the current request. " +
			"Do not treat it as durable memory or reuse it as current time " +
			"in later turns.",
	}, nil
}

func (t *Tool) resolveLocation(requestTimezone string) (*time.Location, string) {
	timezone := strings.TrimSpace(requestTimezone)
	if timezone == "" {
		timezone = t.defaultTimezone
	}
	if timezone == "" {
		return time.Local, time.Local.String()
	}
	loc, err := time.LoadLocation(timezone)
	if err == nil {
		return loc, timezone
	}
	log.Warnf("Invalid timezone '%s', falling back to UTC: %v", timezone, err)
	return time.UTC, "UTC"
}
