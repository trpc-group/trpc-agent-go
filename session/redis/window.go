//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
)

var _ session.WindowService = (*Service)(nil)

const eventWindowScanCap = 10000

// GetEventWindow loads a small ordered event window around one anchor event.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}
	anchorEventID := strings.TrimSpace(req.AnchorEventID)
	if anchorEventID == "" {
		return nil, fmt.Errorf("anchor event id is required")
	}
	req.AnchorEventID = anchorEventID
	if req.Before < 0 || req.After < 0 {
		return nil, fmt.Errorf("event window requires before >= 0 and after >= 0")
	}
	windowSize := uint64(req.Before) + uint64(req.After) + 1
	if windowSize > uint64(eventWindowScanCap) {
		return nil, fmt.Errorf("redis event window exceeds scan cap: %d", eventWindowScanCap)
	}

	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	sess, _, err := s.getSessionInternal(
		ctx,
		req.Key,
		&session.Options{EventNum: eventWindowScanCap},
		zsetExists,
		hashidxExists,
	)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return sessionwindow.EventWindowFromOrderedEvents(req.Key, nil, req)
	}
	window, err := sessionwindow.EventWindowFromOrderedEvents(req.Key, sess.Events, req)
	if err != nil && len(sess.Events) >= eventWindowScanCap {
		return nil, fmt.Errorf(
			"redis event window scan cap exceeded while locating anchor event %q: %w",
			anchorEventID,
			err,
		)
	}
	return window, err
}
