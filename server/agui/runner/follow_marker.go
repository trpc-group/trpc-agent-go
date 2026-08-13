//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const messagesSnapshotFollowRunMarkerKey = session.StateTempPrefix + "agui:messages_snapshot_follow:run"

// messagesSnapshotFollowRunMarker identifies the latest run announced for a
// session. The fixed state key is intentionally retained after completion: a
// matching persisted terminal event makes the marker inactive, while the next
// run overwrites it. This avoids clearing the marker before an asynchronously
// persisted terminal event becomes visible to another instance.
type messagesSnapshotFollowRunMarker struct {
	RunID     string    `json:"runId"`
	StartedAt time.Time `json:"startedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Finished  bool      `json:"finished,omitempty"`
}

func writeMessagesSnapshotFollowRunMarker(
	ctx context.Context,
	service session.Service,
	key session.Key,
	runID string,
	lease time.Duration,
) error {
	if service == nil {
		return fmt.Errorf("session service is nil")
	}
	marker := messagesSnapshotFollowRunMarker{
		RunID:     runID,
		StartedAt: time.Now().UTC(),
	}
	if lease > 0 {
		marker.ExpiresAt = marker.StartedAt.Add(lease)
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	state := session.StateMap{messagesSnapshotFollowRunMarkerKey: raw}
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		if _, createErr := service.CreateSession(ctx, key, state); createErr == nil {
			return nil
		} else {
			sess, err = service.GetSession(ctx, key)
			if err != nil {
				return fmt.Errorf("create session: %v; get session: %w", createErr, err)
			}
			if sess == nil {
				return fmt.Errorf("create session: %w", createErr)
			}
		}
	}
	if err := service.UpdateSessionState(ctx, key, state); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	return nil
}

func finishMessagesSnapshotFollowRunMarker(
	ctx context.Context,
	service session.Service,
	key session.Key,
	runID string,
) error {
	marker, err := readMessagesSnapshotFollowRunMarker(ctx, service, key)
	if err != nil {
		return err
	}
	if marker == nil || marker.RunID != runID {
		return nil
	}
	marker.Finished = true
	raw, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	if err := service.UpdateSessionState(ctx, key, session.StateMap{
		messagesSnapshotFollowRunMarkerKey: raw,
	}); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	return nil
}

func readMessagesSnapshotFollowRunMarker(
	ctx context.Context,
	service session.Service,
	key session.Key,
) (*messagesSnapshotFollowRunMarker, error) {
	if service == nil {
		return nil, nil
	}
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return nil, nil
	}
	raw, ok := sess.GetState(messagesSnapshotFollowRunMarkerKey)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var marker messagesSnapshotFollowRunMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, fmt.Errorf("decode marker: %w", err)
	}
	if marker.StartedAt.IsZero() {
		return nil, fmt.Errorf("decode marker: startedAt is empty")
	}
	if !marker.ExpiresAt.IsZero() && !time.Now().Before(marker.ExpiresAt) {
		return nil, nil
	}
	return &marker, nil
}

func trackContainsTerminalForFollowRun(
	trackEvents *session.TrackEvents,
	marker *messagesSnapshotFollowRunMarker,
) bool {
	if trackEvents == nil || marker == nil {
		return false
	}
	if marker.Finished {
		return true
	}
	for _, trackEvent := range trackEvents.Events {
		if trackEvent.Timestamp.Before(marker.StartedAt) || len(trackEvent.Payload) == 0 {
			continue
		}
		evt, err := aguievents.EventFromJSON(trackEvent.Payload)
		if err != nil {
			continue
		}
		terminal, _ := terminalRunSignal(evt)
		if terminal && evt.RunID() == marker.RunID {
			return true
		}
	}
	return false
}
