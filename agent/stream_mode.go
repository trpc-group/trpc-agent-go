//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

// StreamMode controls which categories of events are forwarded to callers.
// Graph-related modes are only meaningful when the underlying agent emits
// graph events.
type StreamMode string

// StreamMode constants for supported stream categories.
const (
	// StreamModeMessages forwards model message events.
	StreamModeMessages StreamMode = "messages"
	// StreamModeProgress forwards events carrying a valid event.Progress
	// extension, regardless of their object type. It may overlap other modes;
	// selecting multiple modes forwards each event once. It does not enable
	// production; use WithProgressEvents for built-in progress producers.
	StreamModeProgress StreamMode = "progress"
	// StreamModeUpdates forwards graph/state update events.
	StreamModeUpdates StreamMode = "updates"
	// StreamModeCheckpoints forwards checkpoint lifecycle events.
	StreamModeCheckpoints StreamMode = "checkpoints"
	// StreamModeTasks forwards task lifecycle events.
	StreamModeTasks StreamMode = "tasks"
	// StreamModeDebug forwards tasks and checkpoints (debug-focused view).
	StreamModeDebug StreamMode = "debug"
	// StreamModeCustom forwards node-emitted custom events.
	StreamModeCustom StreamMode = "custom"
)
