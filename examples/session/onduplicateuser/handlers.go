//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// InsertPlaceholderHandler inserts a placeholder assistant message
// between consecutive user messages.
func InsertPlaceholderHandler() session.OnDuplicateUserMessageFunc {
	return func(sess *session.Session, prev, curr *event.Event) bool {
		finishReason := "error"
		placeholder := event.Event{
			Response: &model.Response{
				ID:        "",
				Object:    model.ObjectTypeChatCompletion,
				Created:   0,
				Done:      true,
				Timestamp: prev.Timestamp,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "[Connection interrupted]",
						},
						FinishReason: &finishReason,
					},
				},
			},
			RequestID:          prev.RequestID,
			InvocationID:       prev.InvocationID,
			ParentInvocationID: prev.ParentInvocationID,
			Author:             "system",
			ID:                 "",
			Timestamp:          time.Now(),
			Branch:             prev.Branch,
			FilterKey:          prev.FilterKey,
			Version:            event.CurrentVersion,
		}
		sess.Events = append(sess.Events, placeholder)
		return true
	}
}

// RemovePreviousHandler removes the first (older) user message when
// consecutive user messages are detected.
func RemovePreviousHandler() session.OnDuplicateUserMessageFunc {
	return func(sess *session.Session, prev, curr *event.Event) bool {
		if len(sess.Events) > 0 {
			sess.Events = sess.Events[:len(sess.Events)-1]
		}
		return true
	}
}

// SkipCurrentHandler skips the current (newer) user message when
// consecutive user messages are detected.
func SkipCurrentHandler() session.OnDuplicateUserMessageFunc {
	return func(sess *session.Session, prev, curr *event.Event) bool {
		return false
	}
}
