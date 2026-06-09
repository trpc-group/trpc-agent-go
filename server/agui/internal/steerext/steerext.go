//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package steerext defines AG-UI-local queued steer extension metadata.
//
// Keep these wire-format values local to the standalone AG-UI module. Importing
// root internal steer symbols would force published consumers to depend on a
// root module version that already contains those newer symbols.
package steerext

const (
	// QueuedUserMessageExtensionKey marks events emitted when queued user
	// messages are consumed at a safe boundary.
	QueuedUserMessageExtensionKey = "trpc_agent.steer.queued_user_message"

	// QueuedUserMessageStatusConsumed is the status for consumed queued
	// user-message events.
	QueuedUserMessageStatusConsumed = "consumed"
)

// QueuedUserMessageMetadata describes the queued user-message event state.
type QueuedUserMessageMetadata struct {
	Status string `json:"status"`
}
