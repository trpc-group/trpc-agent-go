//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolCron = "cron"

	actionStatus = "status"
	actionList   = "list"
	actionAdd    = "add"
	actionUpdate = "update"
	actionRemove = "remove"
	actionDelete = "delete"
	actionRun    = "run"
	actionClear  = "clear"
)

// Tool exposes the scheduler to the model.
type Tool struct {
	svc *Service
}

// NewTool creates a cron tool for the scheduler service.
func NewTool(svc *Service) *Tool {
	return &Tool{svc: svc}
}

// SetService binds the scheduler service after tool construction.
func (t *Tool) SetService(svc *Service) {
	if t == nil {
		return
	}
	t.svc = svc
}

func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolCron,
		Description: "Manage scheduled jobs. Use add for reminders, " +
			"future work, or recurring reports. The message " +
			"becomes a future agent turn. If channel/target are " +
			"omitted when adding a job from chat, the final " +
			"result is delivered back to the current chat when " +
			"possible. Listing and mutating jobs is scoped to " +
			"the current user.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"action"},
			Properties: map[string]*tool.Schema{
				"action": {
					Type: "string",
					Description: "status, list, add, update, " +
						"remove, clear, or run",
				},
				"job_id": {
					Type: "string",
					Description: "Job id for update, remove, " +
						"or run.",
				},
				"jobId": {
					Type:        "string",
					Description: "Alias for job_id.",
				},
				"name": {
					Type: "string",
					Description: "Optional human-readable job " +
						"name.",
				},
				"message": {
					Type: "string",
					Description: "Agent task prompt for the " +
						"scheduled run.",
				},
				"prompt": {
					Type:        "string",
					Description: "Alias for message.",
				},
				"task": {
					Type:        "string",
					Description: "Alias for message.",
				},
				"enabled": {
					Type:        "boolean",
					Description: "Whether the job is enabled.",
				},
				"schedule_kind": {
					Type:        "string",
					Description: "at, every, or cron.",
				},
				"at": {
					Type: "string",
					Description: "RFC3339 timestamp for at " +
						"schedules.",
				},
				"run_at": {
					Type:        "string",
					Description: "Alias for at.",
				},
				"runAt": {
					Type:        "string",
					Description: "Alias for at.",
				},
				"every": {
					Type: "string",
					Description: "Duration like 1m, 2h, or " +
						"24h for recurring jobs.",
				},
				"interval": {
					Type:        "string",
					Description: "Alias for every.",
				},
				"duration": {
					Type:        "string",
					Description: "Alias for every.",
				},
				"every_ms": {
					Type: "number",
					Description: "Alias duration in " +
						"milliseconds.",
				},
				"everyMs": {
					Type:        "number",
					Description: "Alias for every_ms.",
				},
				"cron_expr": {
					Type: "string",
					Description: "Cron expression with 5 or " +
						"6 fields.",
				},
				"cronExpr": {
					Type:        "string",
					Description: "Alias for cron_expr.",
				},
				"timezone": {
					Type: "string",
					Description: "Optional IANA timezone for " +
						"cron schedules.",
				},
				"timeout_sec": {
					Type: "number",
					Description: "Optional maximum runtime " +
						"per job execution.",
				},
				"timeoutSec": {
					Type:        "number",
					Description: "Alias for timeout_sec.",
				},
				"channel": {
					Type: "string",
					Description: "Optional delivery " +
						"channel. Defaults to current chat " +
						"channel when resolvable.",
				},
				"target": {
					Type: "string",
					Description: "Optional delivery target. " +
						"Defaults to current chat target " +
						"when resolvable.",
				},
			},
		},
	}
}

type toolInput struct {
	Action       string `json:"action"`
	JobID        string `json:"job_id,omitempty"`
	JobIDOld     string `json:"jobId,omitempty"`
	Name         string `json:"name,omitempty"`
	Message      string `json:"message,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	Task         string `json:"task,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
	ScheduleKind string `json:"schedule_kind,omitempty"`
	At           string `json:"at,omitempty"`
	RunAt        string `json:"run_at,omitempty"`
	RunAtOld     string `json:"runAt,omitempty"`
	Every        string `json:"every,omitempty"`
	Interval     string `json:"interval,omitempty"`
	Duration     string `json:"duration,omitempty"`
	EveryMS      *int64 `json:"every_ms,omitempty"`
	EveryMSOld   *int64 `json:"everyMs,omitempty"`
	CronExpr     string `json:"cron_expr,omitempty"`
	CronExprOld  string `json:"cronExpr,omitempty"`
	Timezone     string `json:"timezone,omitempty"`
	TimeoutSec   *int   `json:"timeout_sec,omitempty"`
	TimeoutOld   *int   `json:"timeoutSec,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Target       string `json:"target,omitempty"`
}

func (t *Tool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("cron tool is not configured")
	}

	var in toolInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	if isScheduledRunMutation(ctx, action) {
		return nil, fmt.Errorf(
			"cron: scheduled runs cannot %s jobs",
			action,
		)
	}

	switch action {
	case actionStatus:
		return t.svc.Status(), nil
	case actionList:
		return t.list(ctx, in)
	case actionAdd:
		return t.add(ctx, in)
	case actionUpdate:
		return t.update(ctx, in)
	case actionRemove, actionDelete:
		return t.remove(ctx, in)
	case actionClear:
		return t.clear(ctx, in)
	case actionRun:
		return t.runNow(ctx, in)
	default:
		return nil, fmt.Errorf("unsupported cron action: %s", in.Action)
	}
}

func (t *Tool) list(
	ctx context.Context,
	in toolInput,
) (any, error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}

	delivery, err := optionalScopeDelivery(ctx, in.Channel, in.Target)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"jobs": t.svc.ListForUser(userID, delivery),
	}, nil
}

func (t *Tool) add(
	ctx context.Context,
	in toolInput,
) (any, error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}

	job := &Job{
		Name:       in.Name,
		Message:    resolveMessage(in),
		Enabled:    true,
		Schedule:   scheduleFromInput(in),
		UserID:     userID,
		TimeoutSec: firstIntValue(in.TimeoutSec, in.TimeoutOld),
	}
	if in.Enabled != nil {
		job.Enabled = *in.Enabled
	}
	delivery, err := optionalDelivery(ctx, in.Channel, in.Target)
	if err != nil {
		return nil, err
	}
	job.Delivery = delivery

	created, err := t.svc.Add(job)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (t *Tool) update(
	ctx context.Context,
	in toolInput,
) (any, error) {
	jobID := resolveJobID(in)
	if jobID == "" {
		return nil, fmt.Errorf("job_id is required")
	}
	if _, err := currentOwnedJob(ctx, t.svc, jobID); err != nil {
		return nil, err
	}

	patch := Patch{}
	if in.Name != "" {
		name := in.Name
		patch.Name = &name
	}
	if message := resolveMessage(in); message != "" {
		patch.Message = &message
	}
	if in.Enabled != nil {
		patch.Enabled = in.Enabled
	}
	if hasScheduleInput(in) {
		schedule := scheduleFromInput(in)
		patch.Schedule = &schedule
	}
	if in.TimeoutSec != nil || in.TimeoutOld != nil {
		value := firstIntValue(in.TimeoutSec, in.TimeoutOld)
		patch.TimeoutSec = &value
	}
	if in.Channel != "" || in.Target != "" {
		delivery, err := optionalDelivery(
			ctx,
			in.Channel,
			in.Target,
		)
		if err != nil {
			return nil, err
		}
		channelID := delivery.Channel
		target := delivery.Target
		patch.Channel = &channelID
		patch.Target = &target
	}

	updated, err := t.svc.Update(jobID, patch)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (t *Tool) remove(
	ctx context.Context,
	in toolInput,
) (any, error) {
	jobID := resolveJobID(in)
	if jobID == "" {
		return nil, fmt.Errorf("job_id is required")
	}
	if _, err := currentOwnedJob(ctx, t.svc, jobID); err != nil {
		return nil, err
	}
	if err := t.svc.Remove(jobID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "job_id": jobID}, nil
}

func (t *Tool) clear(
	ctx context.Context,
	in toolInput,
) (any, error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}

	delivery, err := optionalScopeDelivery(ctx, in.Channel, in.Target)
	if err != nil {
		return nil, err
	}
	removed, err := t.svc.RemoveForUser(userID, delivery)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "removed": removed}, nil
}

func (t *Tool) runNow(
	ctx context.Context,
	in toolInput,
) (any, error) {
	jobID := resolveJobID(in)
	if jobID == "" {
		return nil, fmt.Errorf("job_id is required")
	}
	if _, err := currentOwnedJob(ctx, t.svc, jobID); err != nil {
		return nil, err
	}
	job, err := t.svc.RunNow(jobID)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func scheduleFromInput(in toolInput) Schedule {
	at := resolveAt(in)
	every := resolveEvery(in)
	everyMS := firstInt64Value(in.EveryMS, in.EveryMSOld)
	cronExpr := firstString(in.CronExpr, in.CronExprOld)
	return Schedule{
		Kind: resolveScheduleKind(
			in.ScheduleKind,
			at,
			every,
			everyMS,
			cronExpr,
		),
		At:       at,
		Every:    every,
		EveryMS:  everyMS,
		CronExpr: cronExpr,
		Timezone: strings.TrimSpace(in.Timezone),
	}
}

func resolveScheduleKind(
	kind string,
	at string,
	every string,
	everyMS int64,
	cronExpr string,
) string {
	kind = strings.TrimSpace(kind)
	if kind != "" {
		return kind
	}
	switch {
	case strings.TrimSpace(cronExpr) != "":
		return ScheduleKindCron
	case strings.TrimSpace(at) != "":
		return ScheduleKindAt
	case strings.TrimSpace(every) != "", everyMS > 0:
		return ScheduleKindEvery
	default:
		return ""
	}
}

func resolveMessage(in toolInput) string {
	return firstString(in.Message, in.Prompt, in.Task)
}

func resolveAt(in toolInput) string {
	return firstString(in.At, in.RunAt, in.RunAtOld)
}

func resolveEvery(in toolInput) string {
	return firstString(in.Every, in.Interval, in.Duration)
}

func resolveJobID(in toolInput) string {
	if jobID := strings.TrimSpace(in.JobID); jobID != "" {
		return jobID
	}
	return strings.TrimSpace(in.JobIDOld)
}

func currentUserID(ctx context.Context) (string, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return "", fmt.Errorf("cron: current user context is unavailable")
	}
	userID := strings.TrimSpace(inv.Session.UserID)
	if userID == "" {
		return "", fmt.Errorf("cron: current user id is unavailable")
	}
	return userID, nil
}

func currentOwnedJob(
	ctx context.Context,
	svc *Service,
	jobID string,
) (*Job, error) {
	userID, err := currentUserID(ctx)
	if err != nil {
		return nil, err
	}

	job := svc.Get(jobID)
	if job == nil {
		return nil, fmt.Errorf("cron: unknown job: %s", jobID)
	}
	if strings.TrimSpace(job.UserID) != userID {
		return nil, fmt.Errorf("cron: unknown job: %s", jobID)
	}
	return job, nil
}

func optionalDelivery(
	ctx context.Context,
	channelID string,
	target string,
) (outbound.DeliveryTarget, error) {
	explicit := outbound.DeliveryTarget{
		Channel: channelID,
		Target:  target,
	}
	if strings.TrimSpace(channelID) == "" &&
		strings.TrimSpace(target) == "" {
		resolved, err := outbound.ResolveTarget(ctx, explicit)
		if err != nil {
			return outbound.DeliveryTarget{}, nil
		}
		return resolved, nil
	}
	return outbound.ResolveTarget(ctx, explicit)
}

func optionalScopeDelivery(
	ctx context.Context,
	channelID string,
	target string,
) (outbound.DeliveryTarget, error) {
	if strings.TrimSpace(channelID) == "" &&
		strings.TrimSpace(target) == "" {
		return outbound.DeliveryTarget{}, nil
	}
	return outbound.ResolveTarget(
		ctx,
		outbound.DeliveryTarget{
			Channel: channelID,
			Target:  target,
		},
	)
}

func isScheduledRunMutation(ctx context.Context, action string) bool {
	if action == actionStatus || action == actionList {
		return false
	}
	scheduled, ok := agent.GetRuntimeStateValueFromContext[bool](
		ctx,
		runtimeStateScheduledRun,
	)
	return ok && scheduled
}

func hasScheduleInput(in toolInput) bool {
	return strings.TrimSpace(in.ScheduleKind) != "" ||
		resolveAt(in) != "" ||
		resolveEvery(in) != "" ||
		in.EveryMS != nil ||
		in.EveryMSOld != nil ||
		strings.TrimSpace(in.CronExpr) != "" ||
		strings.TrimSpace(in.CronExprOld) != "" ||
		strings.TrimSpace(in.Timezone) != ""
}

func firstIntValue(values ...*int) int {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

func firstInt64Value(values ...*int64) int64 {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var _ tool.CallableTool = (*Tool)(nil)
