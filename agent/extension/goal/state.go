//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	stateKeyRetryCount      = "goal:retry_count"
	stateKeyReminderPending = "goal:reminder_pending"
)

// GoalStatus is the persisted status of a session goal.
type GoalStatus string

const (
	// GoalStatusActive means the goal still needs work.
	GoalStatusActive GoalStatus = "active"
	// GoalStatusBlocked means progress depends on external input.
	GoalStatusBlocked GoalStatus = "blocked"
	// GoalStatusComplete means the objective has been achieved.
	GoalStatusComplete GoalStatus = "complete"
)

// Goal is a session-scoped objective.
type Goal struct {
	ID             string     `json:"id"`
	Objective      string     `json:"objective"`
	Status         GoalStatus `json:"status"`
	CreatedAtUnix  int64      `json:"created_at_unix"`
	UpdatedAtUnix  int64      `json:"updated_at_unix"`
	TerminalAtUnix *int64     `json:"terminal_at_unix,omitempty"`
}

// NewActiveGoal constructs an active goal.
func NewActiveGoal(objective string) (*Goal, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, errors.New("goal objective is required")
	}
	now := time.Now().UTC().Unix()
	return &Goal{
		ID:            uuid.NewString(),
		Objective:     objective,
		Status:        GoalStatusActive,
		CreatedAtUnix: now,
		UpdatedAtUnix: now,
	}, nil
}

// GetGoal reads the default goal state from sess.
func GetGoal(sess *session.Session) (*Goal, bool, error) {
	return GetGoalWithStateKey(sess, DefaultStateKey)
}

// GetGoalWithStateKey reads goal state from sess using stateKey.
func GetGoalWithStateKey(sess *session.Session, stateKey string) (*Goal, bool, error) {
	if sess == nil {
		return nil, false, nil
	}
	if stateKey == "" {
		stateKey = DefaultStateKey
	}
	raw, ok := sess.GetState(stateKey)
	if !ok || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	var g Goal
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, false, fmt.Errorf("goal: decode state: %w", err)
	}
	if g.ID == "" || g.Status == "" {
		return nil, false, nil
	}
	return &g, true, nil
}

// Start creates or replaces the goal in session state for an existing session.
// Applications can use this from their own command layer, for example after
// parsing "/goal ...".
func Start(
	ctx context.Context,
	service session.Service,
	key session.Key,
	objective string,
	options ...StartOption,
) (*Goal, error) {
	if service == nil {
		return nil, errors.New("goal: session service is required")
	}
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	cfg := startOptions{stateKey: DefaultStateKey}
	for _, opt := range options {
		if opt != nil {
			opt(&cfg)
		}
	}
	g, err := NewActiveGoal(objective)
	if err != nil {
		return nil, err
	}
	raw, err := encodeGoal(g)
	if err != nil {
		return nil, err
	}
	state := session.StateMap{cfg.stateKey: raw}
	sess, getErr := service.GetSession(ctx, key)
	if getErr != nil {
		return nil, getErr
	}
	if sess != nil {
		sess.SetState(cfg.stateKey, raw)
		return g, service.UpdateSessionState(ctx, key, state)
	}
	if _, err := service.CreateSession(ctx, key, state); err != nil {
		return nil, err
	}
	return g, nil
}

type startOptions struct {
	stateKey string
}

// StartOption configures Start.
type StartOption func(*startOptions)

// WithStartStateKey sets the state key used by Start.
func WithStartStateKey(key string) StartOption {
	return func(o *startOptions) {
		if key != "" {
			o.stateKey = key
		}
	}
}

func encodeGoal(g *Goal) ([]byte, error) {
	if g == nil {
		return []byte("null"), nil
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("goal: encode state: %w", err)
	}
	return raw, nil
}

func writeGoalToSession(sess *session.Session, stateKey string, g *Goal) error {
	if sess == nil {
		return errors.New("goal: invocation session is required")
	}
	raw, err := encodeGoal(g)
	if err != nil {
		return err
	}
	sess.SetState(stateKey, raw)
	return nil
}

func retryCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	v, _ := agent.GetStateValue[int](inv, stateKeyRetryCount)
	return v
}

func incRetryCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	n := retryCount(inv) + 1
	inv.SetState(stateKeyRetryCount, n)
	return n
}

func resetRetryCount(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(stateKeyRetryCount)
}

func reminderPending(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	v, _ := agent.GetStateValue[bool](inv, stateKeyReminderPending)
	return v
}

func setReminderPending(inv *agent.Invocation, pending bool) {
	if inv == nil {
		return
	}
	if pending {
		inv.SetState(stateKeyReminderPending, true)
		return
	}
	inv.DeleteState(stateKeyReminderPending)
}
