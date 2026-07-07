//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const sessionContextSourceStateKey = "__trpc_agent_session_context_sources__"

type sessionContextSourceLedger map[string]sessionContextSourceRecord

type sessionContextSourceRecord struct {
	Version             string    `json:"version,omitempty"`
	State               []byte    `json:"state,omitempty"`
	MaterializedEventID string    `json:"materialized_event_id,omitempty"`
	MaterializedAt      time.Time `json:"materialized_at,omitempty"`
}

type sessionContextSourceCommit struct {
	name         string
	record       sessionContextSourceRecord
	messageCount int
}

func (r *runner) resolveSessionContextMessages(
	ctx context.Context,
	appName string,
	userID string,
	sessionID string,
	message model.Message,
	ro agent.RunOptions,
) ([]model.Message, error) {
	fns := ro.SessionContextMessageFuncsInOrder()
	if len(fns) == 0 {
		return nil, nil
	}
	var messages []model.Message
	for _, fn := range fns {
		if fn == nil {
			continue
		}
		built, err := fn(ctx, &agent.SessionContextMessagesArgs{
			AppName:         appName,
			UserID:          userID,
			SessionID:       sessionID,
			RequestID:       ro.RequestID,
			OriginalMessage: message,
		})
		if err != nil {
			return nil, fmt.Errorf("runner: build session context messages: %w", err)
		}
		messages = append(messages, built...)
	}
	normalizeSessionContextMessages(messages)
	return filterPayloadMessages(messages), nil
}

func (r *runner) resolveSessionContextSourceMessages(
	ctx context.Context,
	appName string,
	userID string,
	sessionID string,
	sess *session.Session,
	message model.Message,
	ro agent.RunOptions,
) ([]model.Message, []sessionContextSourceCommit, error) {
	if len(ro.SessionContextSources) == 0 {
		return nil, nil, nil
	}
	if sess == nil {
		return nil, nil, fmt.Errorf("runner: nil session for session context source")
	}
	ledger := loadSessionContextSourceLedger(sess)
	seen := make(map[string]struct{})
	var messages []model.Message
	var commits []sessionContextSourceCommit
	for _, entry := range ro.SessionContextSources {
		if entry.Func == nil {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			return nil, nil, fmt.Errorf("runner: session context source name is empty")
		}
		if _, ok := seen[name]; ok {
			return nil, nil, fmt.Errorf("runner: duplicate session context source %q", name)
		}
		seen[name] = struct{}{}

		previous, hasPrevious := ledger[name]
		update, err := entry.Func(ctx, &agent.SessionContextSourceArgs{
			AppName:          appName,
			UserID:           userID,
			SessionID:        sessionID,
			RequestID:        ro.RequestID,
			OriginalMessage:  message,
			Name:             name,
			PreviousVersion:  previous.Version,
			PreviousState:    copySessionContextSourceState(previous.State),
			SnapshotRequired: !hasPrevious || !sessionContextSourceMaterialized(sess, previous),
		})
		if err != nil {
			return nil, nil, fmt.Errorf(
				"runner: build session context source %q: %w",
				name,
				err,
			)
		}
		if update == nil {
			continue
		}
		built, commit, err := resolveSessionContextSourceUpdate(
			name,
			previous,
			hasPrevious,
			update,
		)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, built...)
		if commit != nil {
			commits = append(commits, *commit)
		}
	}
	return messages, commits, nil
}

func resolveSessionContextSourceUpdate(
	name string,
	previous sessionContextSourceRecord,
	hasPrevious bool,
	update *agent.SessionContextSourceResult,
) ([]model.Message, *sessionContextSourceCommit, error) {
	messages := append([]model.Message(nil), update.Messages...)
	normalizeSessionContextMessages(messages)
	messages = filterPayloadMessages(messages)

	version := strings.TrimSpace(update.Version)
	if version == "" {
		var err error
		version, err = deriveSessionContextSourceVersion(
			update.State,
			messages,
			previous.Version,
		)
		if err != nil {
			return nil, nil, err
		}
	}

	state := copySessionContextSourceState(update.State)
	if update.State == nil && hasPrevious {
		state = copySessionContextSourceState(previous.State)
	}
	if version == "" && state == nil && len(messages) == 0 {
		return nil, nil, nil
	}

	record := sessionContextSourceRecord{
		Version:             version,
		State:               state,
		MaterializedEventID: previous.MaterializedEventID,
		MaterializedAt:      previous.MaterializedAt,
	}
	if hasPrevious &&
		previous.Version == record.Version &&
		bytes.Equal(previous.State, record.State) &&
		len(messages) == 0 {
		return messages, nil, nil
	}
	return messages, &sessionContextSourceCommit{
		name:         name,
		record:       record,
		messageCount: len(messages),
	}, nil
}

func sessionContextSourceMaterialized(
	sess *session.Session,
	record sessionContextSourceRecord,
) bool {
	if sess == nil || strings.TrimSpace(record.MaterializedEventID) == "" {
		return false
	}
	events := sess.GetEvents()
	anchorIndex := -1
	var anchor event.Event
	for i, evt := range events {
		if evt.ID == record.MaterializedEventID {
			anchorIndex = i
			anchor = evt
			break
		}
	}
	if anchorIndex < 0 {
		return false
	}
	return !sessionContextSourceCoveredBySummary(
		sess,
		events,
		anchorIndex,
		anchor,
		record,
	)
}

func sessionContextSourceCoveredBySummary(
	sess *session.Session,
	events []event.Event,
	anchorIndex int,
	anchor event.Event,
	record sessionContextSourceRecord,
) bool {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	for _, sum := range sess.Summaries {
		if sum == nil {
			continue
		}
		if sessionContextSourceCoveredByBoundary(
			events,
			anchorIndex,
			anchor,
			record,
			sum.CutoffBoundary(),
		) {
			return true
		}
	}
	return false
}

func sessionContextSourceCoveredByBoundary(
	events []event.Event,
	anchorIndex int,
	anchor event.Event,
	record sessionContextSourceRecord,
	boundary *session.SummaryBoundary,
) bool {
	if boundary == nil {
		return false
	}
	if boundary.LastEventID != "" {
		for i, evt := range events {
			if evt.ID == boundary.LastEventID {
				return anchorIndex <= i
			}
		}
	}
	cutoff := boundary.CutoffTime()
	if cutoff.IsZero() {
		return false
	}
	anchorTime := anchor.Timestamp
	if anchorTime.IsZero() {
		anchorTime = record.MaterializedAt
	}
	if anchorTime.IsZero() {
		return true
	}
	return !anchorTime.UTC().After(cutoff)
}

func loadSessionContextSourceLedger(sess *session.Session) sessionContextSourceLedger {
	ledger := make(sessionContextSourceLedger)
	raw, ok := sess.GetState(sessionContextSourceStateKey)
	if !ok || len(raw) == 0 {
		return ledger
	}
	if err := json.Unmarshal(raw, &ledger); err != nil {
		log.Warnf("runner: ignore invalid session context source state: %v", err)
		return make(sessionContextSourceLedger)
	}
	if ledger == nil {
		return make(sessionContextSourceLedger)
	}
	return ledger
}

func deriveSessionContextSourceVersion(
	state []byte,
	messages []model.Message,
	previousVersion string,
) (string, error) {
	if state != nil {
		sum := sha256.Sum256(state)
		return fmt.Sprintf("sha256:state:%x", sum[:]), nil
	}
	if len(messages) == 0 {
		return previousVersion, nil
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("runner: hash session context source messages: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:messages:%x", sum[:]), nil
}

func copySessionContextSourceState(state []byte) []byte {
	if state == nil {
		return nil
	}
	copied := make([]byte, len(state))
	copy(copied, state)
	return copied
}

func (r *runner) commitSessionContextSourceLedger(
	ctx context.Context,
	sess *session.Session,
	commits []sessionContextSourceCommit,
	sourceEvents []event.Event,
) {
	if len(commits) == 0 || sess == nil {
		return
	}
	ledger := loadSessionContextSourceLedger(sess)
	eventOffset := 0
	for _, commit := range commits {
		if commit.messageCount > 0 {
			eventOffset += commit.messageCount
			if eventOffset <= len(sourceEvents) {
				anchor := sourceEvents[eventOffset-1]
				commit.record.MaterializedEventID = anchor.ID
				commit.record.MaterializedAt = anchor.Timestamp.UTC()
			} else {
				log.WarnfContext(
					ctx,
					"runner: missing materialized event anchor for session context source %q",
					commit.name,
				)
			}
		}
		ledger[commit.name] = commit.record
	}
	raw, err := json.Marshal(ledger)
	if err != nil {
		log.WarnfContext(ctx, "runner: marshal session context source state failed: %v", err)
		return
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := r.sessionService.UpdateSessionState(
		ctx,
		key,
		session.StateMap{sessionContextSourceStateKey: raw},
	); err != nil {
		log.WarnfContext(ctx, "runner: update session context source state failed: %v", err)
		return
	}
	sess.SetState(sessionContextSourceStateKey, raw)
}

func sourceContextMessageCount(commits []sessionContextSourceCommit) int {
	var count int
	for _, commit := range commits {
		count += commit.messageCount
	}
	return count
}

func sourceContextEventsForCurrentTurn(
	appendedEvents []event.Event,
	currentTurnStart int,
	sourceMessageCount int,
) []event.Event {
	if sourceMessageCount <= 0 || len(appendedEvents) == 0 {
		return nil
	}
	if currentTurnStart < 0 || currentTurnStart > len(appendedEvents) {
		return nil
	}
	end := currentTurnStart + sourceMessageCount
	if end > len(appendedEvents) {
		return nil
	}
	return append([]event.Event(nil), appendedEvents[currentTurnStart:end]...)
}

func normalizeSessionContextMessages(messages []model.Message) {
	for i := range messages {
		if model.HasPayload(messages[i]) {
			messages[i].Role = model.RoleUser
		}
	}
}
