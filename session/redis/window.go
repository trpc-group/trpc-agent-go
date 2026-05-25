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

	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
)

var _ session.WindowService = (*Service)(nil)

// GetEventWindow loads a small ordered event window around one anchor event.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	sess, err := s.GetSession(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return sessionwindow.EventWindowFromOrderedEvents(req.Key, nil, req)
	}
	return sessionwindow.EventWindowFromOrderedEvents(req.Key, sess.Events, req)
}
