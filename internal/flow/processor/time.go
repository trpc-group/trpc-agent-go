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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultCurrentDateFormat = "2006-01-02"

	// TimePromptPlacementSystem keeps the historical behavior of adding clock
	// context to the last system message.
	TimePromptPlacementSystem TimePromptPlacement = "system"
	// TimePromptPlacementUser adds clock context to the latest user turn so the
	// stable system prefix remains eligible for provider prompt caching.
	TimePromptPlacementUser TimePromptPlacement = "user"
)

// TimePromptPlacement controls which message receives request-time clock data.
type TimePromptPlacement string

// TimeRequestProcessor implements time processing logic.
type TimeRequestProcessor struct {
	// AddCurrentTime controls whether to add current time to the request.
	// PromptPlacement selects the message that receives it.
	AddCurrentTime bool
	// Timezone specifies the timezone to use for time display.
	Timezone string
	// TimeFormat specifies the format for time display.
	TimeFormat string
	// PromptPlacement controls whether clock context mutates system or user
	// content. The zero value preserves system placement.
	PromptPlacement TimePromptPlacement
	// CurrentTimeToolName is the exact-time tool the model should call when it
	// needs clock-level precision.
	CurrentTimeToolName string
	// CurrentTimeToolAvailable controls whether the clock context should guide
	// the model to call CurrentTimeToolName for exact time.
	CurrentTimeToolAvailable bool
}

// TimeOption is a function that can be used to configure the time request processor.
type TimeOption func(*TimeRequestProcessor)

// WithAddCurrentTime enables or disables adding current time to the request.
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

// WithTimePromptPlacement selects the message role that receives clock context.
// An empty placement keeps the default system placement.
func WithTimePromptPlacement(placement TimePromptPlacement) TimeOption {
	return func(p *TimeRequestProcessor) {
		if placement == "" {
			placement = TimePromptPlacementSystem
		}
		p.PromptPlacement = placement
	}
}

// WithCurrentTimeTool configures the exact-time tool guidance.
func WithCurrentTimeTool(name string, available bool) TimeOption {
	return func(p *TimeRequestProcessor) {
		p.CurrentTimeToolName = strings.TrimSpace(name)
		p.CurrentTimeToolAvailable = available
	}
}

// NewTimeRequestProcessor creates a new time request processor.
func NewTimeRequestProcessor(opts ...TimeOption) *TimeRequestProcessor {
	p := &TimeRequestProcessor{
		AddCurrentTime:           false,
		Timezone:                 "",
		TimeFormat:               defaultCurrentDateFormat,
		PromptPlacement:          TimePromptPlacementSystem,
		CurrentTimeToolName:      "",
		CurrentTimeToolAvailable: false,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It adds current time information to the system prompt if enabled, or to the
// latest user turn when PromptPlacement is TimePromptPlacementUser.
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
	timeContent := p.formatTimePrompt(currentTime)

	if p.PromptPlacement == TimePromptPlacementUser {
		p.addTimeToUserMessage(req, timeContent)
		return
	}
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

	now := time.Now().In(loc)
	format := p.TimeFormat
	if format == "" {
		format = defaultCurrentDateFormat
	}

	return now.Format(format)
}

func (p *TimeRequestProcessor) formatTimePrompt(currentTime string) string {
	label := "The current time is"
	if p.effectiveTimeFormat() == defaultCurrentDateFormat {
		label = "The current date is"
	}
	var b strings.Builder
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(currentTime)
	if tz := strings.TrimSpace(p.Timezone); tz != "" {
		b.WriteString(" (timezone: ")
		b.WriteString(tz)
		b.WriteString(")")
	}
	if p.CurrentTimeToolAvailable && strings.TrimSpace(p.CurrentTimeToolName) != "" {
		b.WriteString("\n\n")
		b.WriteString("For exact current time or timezone-specific time, call the built-in ")
		b.WriteString(p.CurrentTimeToolName)
		b.WriteString(" tool. Treat its result as valid only for the current request; ")
		b.WriteString("do not reuse previous time tool results as current time.")
	}
	return b.String()
}

func (p *TimeRequestProcessor) effectiveTimeFormat() string {
	if p.TimeFormat == "" {
		return defaultCurrentDateFormat
	}
	return p.TimeFormat
}

// addTimeToSystemMessage adds time information to the system message.
func (p *TimeRequestProcessor) addTimeToSystemMessage(req *model.Request, timeContent string) {
	// Find existing system message or create new one.
	systemMsgIndex := findLastSystemMessageIndex(req.Messages)

	if systemMsgIndex >= 0 {
		// There's already a system message, check if it contains time info.
		if !containsTimeInfo(req.Messages[systemMsgIndex].Content, timeContent) {
			// Append time info to existing system message.
			if req.Messages[systemMsgIndex].Content == "" {
				req.Messages[systemMsgIndex].Content = timeContent
			} else {
				req.Messages[systemMsgIndex].Content += "\n\n" +
					timeContent
			}
		}
	} else {
		// No existing system message, create new one.
		timeMsg := model.NewSystemMessage(timeContent)
		req.Messages = append([]model.Message{timeMsg}, req.Messages...)
	}
}

// addTimeToUserMessage adds clock context to the latest user turn.
func (p *TimeRequestProcessor) addTimeToUserMessage(req *model.Request, timeContent string) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := &req.Messages[i]
		if msg.Role != model.RoleUser {
			continue
		}
		if messageContainsTimeInfo(msg, timeContent) {
			return
		}
		// Providers serialize either the content parts or the scalar content of
		// a message, never both, so the clock block has to use the same
		// representation as the user input it is attached to.
		if len(msg.ContentParts) > 0 {
			text := timeContent
			msg.ContentParts = append(msg.ContentParts, model.ContentPart{
				Type: model.ContentTypeText,
				Text: &text,
			})
			return
		}
		msg.Content += "\n\n" + timeContent
		return
	}
	req.Messages = append(req.Messages, model.NewUserMessage(timeContent))
}

// messageContainsTimeInfo reports whether the message already carries this
// request's clock block in its scalar content or in any text content part.
func messageContainsTimeInfo(msg *model.Message, timeInfo string) bool {
	if containsTimeInfo(msg.Content, timeInfo) {
		return true
	}
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if containsTimeInfo(*part.Text, timeInfo) {
			return true
		}
	}
	return false
}

// containsTimeInfo reports whether content already has this request's clock
// block. A raw timestamp in user text is not enough; skip only when the
// official current-time or current-date label is present.
func containsTimeInfo(content, timeInfo string) bool {
	if !strings.Contains(content, "The current time is:") &&
		!strings.Contains(content, "The current date is:") {
		return false
	}
	return strings.Contains(content, timeInfo)
}
