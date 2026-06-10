//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package currenttime provides the current_time framework tool.
package currenttime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// ToolName is the built-in exact current time tool name.
	ToolName = "current_time"

	defaultFormat = "2006-01-02 15:04:05 MST"
)

// Request is the input payload for the current_time tool.
type Request struct {
	// Timezone is an optional IANA timezone name such as UTC,
	// Asia/Shanghai, or America/New_York. Empty uses the machine local time.
	Timezone string `json:"timezone,omitempty"`
	// Format is optional. Supported shortcuts are full, date, time, and unix.
	// Any other non-empty value is treated as a Go time layout.
	Format string `json:"format,omitempty"`
}

// Response is the current_time tool result.
type Response struct {
	Success bool `json:"success"`
	// CurrentTime is the formatted current time.
	CurrentTime string `json:"current_time,omitempty"`
	// Timezone is the timezone used for CurrentTime.
	Timezone string `json:"timezone,omitempty"`
	// Format is the effective format or shortcut used for CurrentTime.
	Format string `json:"format,omitempty"`
	// Note reminds the model not to treat the result as durable memory.
	Note string `json:"note,omitempty"`
	// Error describes invalid input when Success is false.
	Error string `json:"error,omitempty"`
}

// Tool returns the current date/time for the requested timezone and format.
type Tool struct {
	now func() time.Time
}

// New creates a current_time tool.
func New() *Tool {
	return &Tool{now: time.Now}
}

// Declaration implements tool.Tool.
func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: ToolName,
		Description: "Get the exact current date/time when clock-level " +
			"precision is required. The result is valid only for the " +
			"current request; call this tool again instead of reusing " +
			"older time results.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"timezone": {
					Type: "string",
					Description: "Optional IANA timezone name, for example " +
						"UTC, Asia/Shanghai, or America/New_York. Empty uses " +
						"the machine local timezone.",
				},
				"format": {
					Type: "string",
					Description: "Optional format: full, date, time, unix, " +
						"or a Go time layout.",
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
			return Response{
				Success: false,
				Error:   fmt.Sprintf("invalid request format: %v", err),
			}, nil
		}
	}

	loc := time.Local
	timezone := loc.String()
	if strings.TrimSpace(req.Timezone) != "" {
		parsed, err := time.LoadLocation(strings.TrimSpace(req.Timezone))
		if err != nil {
			return Response{
				Success: false,
				Error: fmt.Sprintf(
					"invalid timezone %q: %v",
					req.Timezone,
					err,
				),
			}, nil
		}
		loc = parsed
		timezone = strings.TrimSpace(req.Timezone)
	}

	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = "full"
	}
	now := t.now().In(loc)
	currentTime := formatTime(now, format)

	return Response{
		Success:     true,
		CurrentTime: currentTime,
		Timezone:    timezone,
		Format:      format,
		Note: "This time value is valid only for the current request. " +
			"Do not treat it as durable memory or reuse it as current time " +
			"in later turns.",
	}, nil
}

func formatTime(t time.Time, format string) string {
	switch strings.ToLower(format) {
	case "full":
		return t.Format(defaultFormat)
	case "date":
		return t.Format("2006-01-02")
	case "time":
		return t.Format("15:04:05")
	case "unix":
		return fmt.Sprintf("%d", t.Unix())
	default:
		return t.Format(format)
	}
}
