//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package conversation stores and renders speaker-aware conversation
// metadata for OpenClaw channels.
package conversation

const (
	// ExtensionKey stores serialized conversation metadata on requests
	// and persisted session events.
	ExtensionKey = "openclaw:conversation:v1"

	// RuntimeStateKey stores decoded conversation metadata in one run.
	RuntimeStateKey = "openclaw.conversation"

	// HistoryModeShared tells OpenClaw to project speaker-aware session
	// history into the current run.
	HistoryModeShared = "shared"

	authorUser      = "user"
	authorAssistant = "assistant"
	authorSystem    = "system"

	contextSpeakerPrefix = "Speaker"
	contextQuotePrefix   = "Quoted message"
	contextMessagePrefix = "Message"

	summarySpeakerAssistant = "Assistant"
	summarySpeakerSystem    = "Previous summary"

	summaryHeader = "Here is a brief summary of your previous " +
		"interactions:\n\n<summary_of_previous_interactions>\n%s\n" +
		"</summary_of_previous_interactions>\n\nYou should ALWAYS " +
		"prefer information from this conversation over the past " +
		"summary.\n"

	attachmentWordSingular = "attachment"
	attachmentWordPlural   = "attachments"
)
