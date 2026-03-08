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

// OutboundFile describes one local file to send back through a channel.
type OutboundFile struct {
	// Path is a host path or other channel-specific local reference.
	Path string
	// Name optionally overrides the uploaded filename.
	Name string
	// AsVoice asks channels that support it to deliver compatible
	// audio as a voice note bubble instead of a generic audio file.
	AsVoice bool
}

// OutboundMessage is a generic outbound payload for chat channels.
type OutboundMessage struct {
	// Text is optional plain text to send before or with media.
	Text string
	// Files contains optional local files to deliver.
	Files []OutboundFile
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

// MessageSender is an optional outbound capability implemented by
// channels that can deliver text together with local media/files.
type MessageSender interface {
	Channel

	// SendMessage delivers a structured outbound payload to the
	// provided channel-specific target.
	SendMessage(
		ctx context.Context,
		target string,
		msg OutboundMessage,
	) error
}
