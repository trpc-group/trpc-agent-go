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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const currentTimeMessagePrefix = "The current time is: "

var timeNow = time.Now

// TimeRequestProcessor implements time processing logic.
type TimeRequestProcessor struct {
	// AddCurrentTime controls whether to add current time to the system prompt.
	AddCurrentTime bool
	// Timezone specifies the timezone to use for time display.
	Timezone string
	// TimeFormat specifies the format for time display.
	TimeFormat string
}

// TimeOption is a function that can be used to configure the time request processor.
type TimeOption func(*TimeRequestProcessor)

// WithAddCurrentTime enables or disables adding current time to the system prompt.
func WithAddCurrentTime(add bool) TimeOption {
	return func(p *TimeRequestProcessor) {
		p.AddCurrentTime = add
	}
}

// WithTimezone sets the timezone for time display.
func WithTimezone(tz string) TimeOption {
	return func(p *TimeRequestProcessor) {
		p.Timezone = tz
	}
}

// WithTimeFormat sets the format for time display.
func WithTimeFormat(format string) TimeOption {
	return func(p *TimeRequestProcessor) {
		p.TimeFormat = format
	}
}

// NewTimeRequestProcessor creates a new time request processor.
func NewTimeRequestProcessor(opts ...TimeOption) *TimeRequestProcessor {
	p := &TimeRequestProcessor{
		AddCurrentTime: false,
		Timezone:       "",
		TimeFormat:     "2006-01-02 15:04:05 MST",
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds current time information to the system prompt if enabled.
func (p *TimeRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if !p.AddCurrentTime {
		return
	}

	if req == nil {
		log.ErrorfContext(
			ctx,
			"Time request processor: request is nil",
		)
		return
	}

	agentName := ""
	if invocation != nil {
		agentName = invocation.AgentName
	}
	log.DebugfContext(
		ctx,
		"Time request processor: processing request for agent %s",
		agentName,
	)

	// Get current time with timezone support.
	currentTime := p.getCurrentTime()
	timeContent := fmt.Sprintf("%s%s", currentTimeMessagePrefix, currentTime)

	// Add time information to the system message.
	p.addTimeToSystemMessage(req, timeContent)
}

// SupportsContextCompactionRebuild reports that time decoration can be safely
// replayed during the sync-summary rebuild path.
func (p *TimeRequestProcessor) SupportsContextCompactionRebuild(
	_ *agent.Invocation,
) bool {
	return true
}

// RebuildRequestForContextCompaction re-applies time decoration during the
// safe sync-summary rebuild path without replaying the full processor chain.
func (p *TimeRequestProcessor) RebuildRequestForContextCompaction(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	p.ProcessRequest(ctx, invocation, req, nil)
}

// getCurrentTime returns the current time string with timezone support.
func (p *TimeRequestProcessor) getCurrentTime() string {
	var loc *time.Location
	var err error

	if p.Timezone != "" {
		loc, err = time.LoadLocation(p.Timezone)
		if err != nil {
			log.Warnf("Invalid timezone '%s', falling back to UTC: %v", p.Timezone, err)
			loc = time.UTC
		}
	} else {
		loc = time.Local
	}

	now := timeNow().In(loc)
	format := p.TimeFormat
	if format == "" {
		format = "2006-01-02 15:04:05 MST"
	}

	return now.Format(format)
}

// addTimeToSystemMessage adds time information as a dedicated system message.
func (p *TimeRequestProcessor) addTimeToSystemMessage(req *model.Request, timeContent string) {
	if idx := findCurrentTimeMessageIndex(req.Messages); idx >= 0 {
		req.Messages[idx].Content = timeContent
		return
	}

	insertAfterLastSystemMessage(req, model.NewSystemMessage(timeContent))
}

func findCurrentTimeMessageIndex(messages []model.Message) int {
	for i, msg := range messages {
		if msg.Role == model.RoleSystem &&
			strings.HasPrefix(msg.Content, currentTimeMessagePrefix) {
			return i
		}
	}
	return -1
}
