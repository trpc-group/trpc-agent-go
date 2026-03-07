//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type turnToPersist struct {
	appName         string
	evalSetID       string
	evalCaseID      string
	evalMode        evalset.EvalMode
	sessionIn       *evalset.SessionInput
	contextMessages []*model.Message
	invocation      *evalset.Invocation
}

func (t *turnToPersist) lockKey() string {
	return t.appName + ":" + t.evalSetID + ":" + t.evalCaseID
}

func (r *Recorder) persistTurn(ctx context.Context, turn *turnToPersist) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := turn.lockKey()
	r.locker.lock(key)
	defer r.locker.unlock(key)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.ensureEvalSet(ctx, turn.appName, turn.evalSetID); err != nil {
		return err
	}
	return r.appendInvocation(ctx, turn)
}

func (r *Recorder) ensureEvalSet(ctx context.Context, appName, evalSetID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := r.manager.Get(ctx, appName, evalSetID); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("get eval set %s.%s: %w", appName, evalSetID, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := r.manager.Create(ctx, appName, evalSetID); err != nil {
		if _, getErr := r.manager.Get(ctx, appName, evalSetID); getErr == nil {
			return nil
		}
		return fmt.Errorf("create eval set %s.%s: %w", appName, evalSetID, err)
	}
	return nil
}

func (r *Recorder) appendInvocation(ctx context.Context, turn *turnToPersist) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	existing, err := r.manager.GetCase(ctx, turn.appName, turn.evalSetID, turn.evalCaseID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := ctx.Err(); err != nil {
				return err
			}
			newCase := &evalset.EvalCase{
				EvalID:          turn.evalCaseID,
				EvalMode:        turn.evalMode,
				SessionInput:    turn.sessionIn,
				ContextMessages: turn.contextMessages,
			}
			appendConversationByMode(newCase, turn.evalMode, turn.invocation)
			if err := r.manager.AddCase(ctx, turn.appName, turn.evalSetID, newCase); err != nil {
				return fmt.Errorf("add eval case %s.%s.%s: %w", turn.appName, turn.evalSetID, turn.evalCaseID, err)
			}
			return nil
		}
		return fmt.Errorf("get eval case %s.%s.%s: %w", turn.appName, turn.evalSetID, turn.evalCaseID, err)
	}
	if existing.EvalMode != turn.evalMode {
		return fmt.Errorf(
			"eval case %s.%s.%s mode mismatch: existing=%q, incoming=%q",
			turn.appName,
			turn.evalSetID,
			turn.evalCaseID,
			existing.EvalMode,
			turn.evalMode,
		)
	}
	conversation := conversationByMode(existing, turn.evalMode)
	if hasInvocation(conversation, turn.invocation.InvocationID) {
		return nil
	}
	if existing.SessionInput == nil {
		existing.SessionInput = turn.sessionIn
	}
	if len(existing.ContextMessages) == 0 && len(turn.contextMessages) > 0 {
		existing.ContextMessages = turn.contextMessages
	}
	appendConversationByMode(existing, turn.evalMode, turn.invocation)
	sortInvocations(conversationByMode(existing, turn.evalMode))
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.manager.UpdateCase(ctx, turn.appName, turn.evalSetID, existing); err != nil {
		return fmt.Errorf("update eval case %s.%s.%s: %w", turn.appName, turn.evalSetID, turn.evalCaseID, err)
	}
	return nil
}

func sortInvocations(invocations []*evalset.Invocation) {
	sort.SliceStable(invocations, func(i, j int) bool {
		ti := invocationTime(invocations[i])
		tj := invocationTime(invocations[j])
		if ti.Equal(tj) {
			ii := ""
			jj := ""
			if invocations[i] != nil {
				ii = invocations[i].InvocationID
			}
			if invocations[j] != nil {
				jj = invocations[j].InvocationID
			}
			return ii < jj
		}
		return ti.Before(tj)
	})
}

func invocationTime(invocation *evalset.Invocation) time.Time {
	if invocation == nil || invocation.CreationTimestamp == nil {
		return time.Time{}
	}
	return invocation.CreationTimestamp.Time
}

func hasInvocation(invocations []*evalset.Invocation, invocationID string) bool {
	if invocationID == "" {
		return false
	}
	for _, inv := range invocations {
		if inv == nil {
			continue
		}
		if inv.InvocationID == invocationID {
			return true
		}
	}
	return false
}

func conversationByMode(evalCase *evalset.EvalCase, mode evalset.EvalMode) []*evalset.Invocation {
	if evalCase == nil {
		return nil
	}
	if mode == evalset.EvalModeTrace {
		return evalCase.ActualConversation
	}
	return evalCase.Conversation
}

func appendConversationByMode(evalCase *evalset.EvalCase, mode evalset.EvalMode, invocation *evalset.Invocation) {
	if evalCase == nil {
		return
	}
	if mode == evalset.EvalModeTrace {
		evalCase.ActualConversation = append(evalCase.ActualConversation, invocation)
		return
	}
	evalCase.Conversation = append(evalCase.Conversation, invocation)
}
