//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package channel

import "context"

// Channel represents one external surface (Telegram, Slack, etc.) that
// can receive inbound messages and deliver replies.
type Channel interface {
	// ID returns a stable channel identifier.
	ID() string

	// Run blocks until ctx is done or an unrecoverable error happens.
	Run(ctx context.Context) error
}
