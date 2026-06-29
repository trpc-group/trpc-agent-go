//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package goal

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Extension provides session-goal tools and enforcement hooks for one
// LLMAgent.
type Extension struct {
	opts           Options
	getGoalTool    *goalTool
	createGoalTool *goalTool
	updateGoalTool *goalTool
}

var _ extension.Extension = (*Extension)(nil)

// New builds a Goal extension ready to install via llmagent.WithExtensions.
func New(opts ...Option) *Extension {
	o := Options{
		Name:               DefaultExtensionName,
		StateKey:           DefaultStateKey,
		GetGoalToolName:    DefaultGetGoalToolName,
		CreateGoalToolName: DefaultCreateGoalToolName,
		UpdateGoalToolName: DefaultUpdateGoalToolName,
		InjectGuidance:     true,
		MaxRetries:         DefaultMaxRetries,
		NudgeFormatter:     DefaultNudgeFormatter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.Name == "" {
		o.Name = DefaultExtensionName
	}
	if o.StateKey == "" {
		o.StateKey = DefaultStateKey
	}
	if o.GetGoalToolName == "" {
		o.GetGoalToolName = DefaultGetGoalToolName
	}
	if o.CreateGoalToolName == "" {
		o.CreateGoalToolName = DefaultCreateGoalToolName
	}
	if o.UpdateGoalToolName == "" {
		o.UpdateGoalToolName = DefaultUpdateGoalToolName
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = DefaultMaxRetries
	}
	if o.NudgeFormatter == nil {
		o.NudgeFormatter = DefaultNudgeFormatter
	}

	e := &Extension{opts: o}
	e.getGoalTool = newGoalTool(toolKindGet, o.GetGoalToolName, o.StateKey)
	e.createGoalTool = newGoalTool(toolKindCreate, o.CreateGoalToolName, o.StateKey)
	e.updateGoalTool = newGoalTool(toolKindUpdate, o.UpdateGoalToolName, o.StateKey)
	return e
}

// Name implements extension.Extension.
func (e *Extension) Name() string {
	if e == nil || e.opts.Name == "" {
		return DefaultExtensionName
	}
	return e.opts.Name
}

// Register implements extension.Extension.
func (e *Extension) Register(r *extension.Registry) {
	if r == nil {
		return
	}
	r.Tools(e.getGoalTool, e.createGoalTool, e.updateGoalTool)
	r.BeforeModel(e.beforeModel)
	r.AfterModel(e.afterModel)
}

func (e *Extension) beforeModel(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	if e.opts.InjectGuidance {
		insertGuidance(args.Request, e.guidance())
	}

	pendingReminder := reminderPending(inv)
	if pendingReminder {
		setReminderPending(inv, false)
	}

	g, ok, err := GetGoalWithStateKey(invocationSession(inv), e.opts.StateKey)
	if err != nil {
		log.WarnfContext(ctx, "goal: read goal failed: %v", err)
		return nil, nil
	}
	if !ok || g.Status != GoalStatusActive {
		return nil, nil
	}

	if !pendingReminder {
		return nil, nil
	}
	msg := e.opts.NudgeFormatter(NudgeContext{
		AgentName:          invocationAgentName(inv),
		Goal:               g,
		AttemptNumber:      retryCount(inv),
		MaxRetries:         e.opts.MaxRetries,
		UpdateGoalToolName: e.updateGoalToolName(),
	})
	if msg == "" {
		return nil, nil
	}
	args.Request.Messages = append(args.Request.Messages, model.NewUserMessage(msg))
	return nil, nil
}

func (e *Extension) afterModel(
	ctx context.Context,
	args *model.AfterModelArgs,
) (*model.AfterModelResult, error) {
	if args == nil || args.Response == nil {
		return nil, nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	if args.Error != nil || args.Response.Error != nil {
		return nil, nil
	}
	if !e.shouldConsiderResponse(args.Response) {
		return nil, nil
	}

	g, ok, err := GetGoalWithStateKey(invocationSession(inv), e.opts.StateKey)
	if err != nil {
		log.WarnfContext(ctx, "goal: read goal failed: %v", err)
		return nil, nil
	}
	if !ok || g.Status != GoalStatusActive {
		return nil, nil
	}

	if retryCount(inv) >= e.opts.MaxRetries {
		e.notify(EnforceEvent{
			Reason:        ReasonExhausted,
			AgentName:     invocationAgentName(inv),
			Goal:          g,
			AttemptNumber: retryCount(inv),
			MaxRetries:    e.opts.MaxRetries,
		})
		resetRetryCount(inv)
		return nil, nil
	}

	setReminderPending(inv, true)
	attempt := incRetryCount(inv)
	e.notify(EnforceEvent{
		Reason:        ReasonBlocked,
		AgentName:     invocationAgentName(inv),
		Goal:          g,
		AttemptNumber: attempt,
		MaxRetries:    e.opts.MaxRetries,
	})
	return &model.AfterModelResult{
		CustomResponse: blockedControlResponse(args.Response),
	}, nil
}

func blockedControlResponse(src *model.Response) *model.Response {
	if src == nil {
		return &model.Response{Done: false}
	}
	rsp := src.Clone()
	rsp.Done = false
	rsp.IsPartial = false
	rsp.Choices = nil
	rsp.Error = nil
	return rsp
}

func (e *Extension) shouldConsiderResponse(rsp *model.Response) bool {
	if rsp == nil {
		return false
	}
	if rsp.IsPartial || rsp.Error != nil {
		return false
	}
	if rsp.IsToolCallResponse() {
		return false
	}
	return rsp.IsFinalResponse()
}

func (e *Extension) guidance() string {
	if e == nil {
		return renderGuidance("", "", "")
	}
	return renderGuidance(
		e.getGoalToolName(),
		e.createGoalToolName(),
		e.updateGoalToolName(),
	)
}

func (e *Extension) getGoalToolName() string {
	if e == nil {
		return DefaultGetGoalToolName
	}
	if e.getGoalTool != nil && e.getGoalTool.name != "" {
		return e.getGoalTool.name
	}
	if e.opts.GetGoalToolName != "" {
		return e.opts.GetGoalToolName
	}
	return DefaultGetGoalToolName
}

func (e *Extension) createGoalToolName() string {
	if e == nil {
		return DefaultCreateGoalToolName
	}
	if e.createGoalTool != nil && e.createGoalTool.name != "" {
		return e.createGoalTool.name
	}
	if e.opts.CreateGoalToolName != "" {
		return e.opts.CreateGoalToolName
	}
	return DefaultCreateGoalToolName
}

func (e *Extension) updateGoalToolName() string {
	if e == nil {
		return DefaultUpdateGoalToolName
	}
	if e.updateGoalTool != nil && e.updateGoalTool.name != "" {
		return e.updateGoalTool.name
	}
	if e.opts.UpdateGoalToolName != "" {
		return e.opts.UpdateGoalToolName
	}
	return DefaultUpdateGoalToolName
}

func (e *Extension) notify(evt EnforceEvent) {
	if e == nil || e.opts.OnEnforce == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("goal: OnEnforce panic: %v", r)
		}
	}()
	e.opts.OnEnforce(evt)
}

func invocationSession(inv *agent.Invocation) *session.Session {
	if inv == nil {
		return nil
	}
	return inv.Session
}

func invocationAgentName(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	return inv.AgentName
}
