//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package channel defines the minimal interface for chat channels.
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

// TextSender is an optional outbound capability implemented by channels
// that can deliver plain text messages to a channel-specific target.
//
// Target encoding is channel-specific. For example, Telegram may use:
//   - "<chatID>" for direct messages
//   - "<chatID>:topic:<topicID>" for forum topics
type TextSender interface {
	Channel

	// SendText delivers text to the provided channel-specific target.
	SendText(ctx context.Context, target string, text string) error
}
