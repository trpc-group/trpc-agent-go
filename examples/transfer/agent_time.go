//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// createTimeAgent creates a specialized time calculation agent.
func (c *transferChat) createTimeAgent(modelInstance model.Model) agent.Agent {
	// Time calculation tool.
	timeDiffTool := function.NewFunctionTool(
		c.calculateTimeDiff,
		function.WithName("calculate_time_diff"),
		function.WithDescription("Calculate the time difference between two timestamps"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1500),
		Temperature: floatPtr(0.4), // Moderate precision for time calculations.
		Stream:      true,
	}

	return llmagent.New(
		"time-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized time calculation agent"),
		llmagent.WithInstruction("You are a time calculation expert. Calculate time differences accurately and provide clear explanations of the duration."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{timeDiffTool}),
	)
}

// calculateTimeDiff calculates the difference between two timestamps.
func (c *transferChat) calculateTimeDiff(_ context.Context, args timeDiffArgs) (timeDiffResult, error) {
	// Try multiple time formats for better compatibility.
	var startTime, endTime time.Time
	var err error

	// Common formats to try.
	formats := []string{
		time.RFC3339,          // 2023-01-01T00:00:00Z
		time.RFC3339Nano,      // 2023-01-01T00:00:00.123Z
		"2006-01-02 15:04:05", // 2023-01-01 00:00:00 (no timezone)
		"2006-01-02T15:04:05", // 2023-01-01T00:00:00 (no timezone)
	}

	// Parse start time.
	for _, format := range formats {
		startTime, err = time.Parse(format, args.StartTime)
		if err == nil {
			// If no timezone in format, assume UTC for consistency
			if format == "2006-01-02 15:04:05" || format == "2006-01-02T15:04:05" {
				startTime = time.Date(startTime.Year(), startTime.Month(), startTime.Day(),
					startTime.Hour(), startTime.Minute(), startTime.Second(), startTime.Nanosecond(), time.UTC)
			}
			break
		}
	}
	if err != nil {
		return timeDiffResult{
			StartTime: args.StartTime,
			EndTime:   args.EndTime,
			Error:     "Invalid start time format. Supported formats: RFC3339 (2023-01-01T00:00:00Z), DateTime (2023-01-01 00:00:00), or 2006-01-02T15:04:05",
		}, nil
	}

	// Parse end time.
	for _, format := range formats {
		endTime, err = time.Parse(format, args.EndTime)
		if err == nil {
			// If no timezone in format, assume UTC for consistency
			if format == "2006-01-02 15:04:05" || format == "2006-01-02T15:04:05" {
				endTime = time.Date(endTime.Year(), endTime.Month(), endTime.Day(),
					endTime.Hour(), endTime.Minute(), endTime.Second(), endTime.Nanosecond(), time.UTC)
			}
			break
		}
	}
	if err != nil {
		return timeDiffResult{
			StartTime: args.StartTime,
			EndTime:   args.EndTime,
			Error:     "Invalid end time format. Supported formats: RFC3339 (2023-01-01T00:00:00Z), DateTime (2023-01-01 00:00:00), or 2006-01-02T15:04:05",
		}, nil
	}

	// Calculate the difference.
	duration := endTime.Sub(startTime)

	// Handle negative duration.
	if duration < 0 {
		return timeDiffResult{
			StartTime: args.StartTime,
			EndTime:   args.EndTime,
			Error:     "End time must be after start time",
		}, nil
	}

	// Calculate different time units.
	days := int(duration.Hours() / 24)
	hours := int(duration.Hours()) % 24
	minutes := int(duration.Minutes()) % 60
	seconds := int(duration.Seconds()) % 60

	result := timeDiffResult{
		StartTime:    args.StartTime,
		EndTime:      args.EndTime,
		Duration:     duration.String(),
		TotalSeconds: int(duration.Seconds()),
		TotalMinutes: int(duration.Minutes()),
		TotalHours:   duration.Hours(),
		Days:         days,
		Hours:        hours,
		Minutes:      minutes,
		Seconds:      seconds,
		IsPositive:   true,
	}

	return result, nil
}

// Data structures for time difference tool.
type timeDiffArgs struct {
	StartTime string `json:"startTime" jsonschema:"description=Start time. Supported formats: RFC3339 (2023-01-01T00:00:00Z). DateTime (2023-01-01 00:00:00). or 2006-01-02T15:04:05,required"`
	EndTime   string `json:"endTime" jsonschema:"description=End time. Supported formats: RFC3339 (2023-01-02T12:30:45Z). DateTime (2023-01-02 12:30:45). or 2006-01-02T15:04:05,required"`
}

type timeDiffResult struct {
	StartTime    string  `json:"startTime"`
	EndTime      string  `json:"endTime"`
	Duration     string  `json:"duration"`
	TotalSeconds int     `json:"totalSeconds"`
	TotalMinutes int     `json:"totalMinutes"`
	TotalHours   float64 `json:"totalHours"`
	Days         int     `json:"days"`
	Hours        int     `json:"hours"`
	Minutes      int     `json:"minutes"`
	Seconds      int     `json:"seconds"`
	IsPositive   bool    `json:"isPositive"`
	Error        string  `json:"error,omitempty"`
}
