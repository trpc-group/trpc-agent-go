//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewTimeTool creates a time tool.
func NewTimeTool() tool.CallableTool {
	return function.NewFunctionTool(
		getTimeInfo,
		function.WithName("time_tool"),
		function.WithDescription("Get time and date information. Supported operations: 'current'(current time), 'date'(current date), 'weekday'(day of week), 'timestamp'(ISO format timestamp), 'unixtime'(Unix timestamp)"),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Time operation to perform",
			Required:    []string{"operation"},
			Properties: map[string]*tool.Schema{
				"operation": {
					Type:        "string",
					Description: "Time operation type to perform",
					Enum:        []any{"current", "date", "weekday", "timestamp", "unixtime"},
				},
			},
		}),
	)
}

type timeRequest struct {
	Operation string `json:"operation"`
}

type timeResponse struct {
	Operation string `json:"operation"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
}

func getTimeInfo(_ context.Context, req timeRequest) (timeResponse, error) {
	now := time.Now()
	var result string
	switch req.Operation {
	case "current":
		result = now.Format("2006-01-02 15:04:05")
	case "date":
		result = now.Format("2006-01-02")
	case "weekday":
		weekdays := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		result = weekdays[now.Weekday()]
	case "timestamp":
		result = now.Format(time.RFC3339)
	case "unixtime":
		result = fmt.Sprintf("%d", now.Unix())
	default:
		result = fmt.Sprintf("Current time: %s", now.Format("2006-01-02 15:04:05"))
	}
	return timeResponse{
		Operation: req.Operation,
		Result:    result,
		Timestamp: now.Unix(),
	}, nil
}
