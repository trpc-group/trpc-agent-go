//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import "trpc.group/trpc-go/trpc-agent-go/model"

const historyToolGuidanceContent = "" +
	"A session summary is present above. The summary is a compressed " +
	"version of earlier turns, so specific details (exact names, " +
	"lists, numbers, recommendations) may be missing. " +
	"When answering a question that depends on such details, call " +
	"search_history to find the relevant eventIds, then call " +
	"get_history_events to retrieve the full content. " +
	"After retrieving history, incorporate the recovered details " +
	"into your response naturally—give a thorough, complete answer " +
	"as if you had the full conversation context. " +
	"Do not use history tools when the summary already contains " +
	"enough information to answer accurately. " +
	"History tools have a limited budget; use the minimum scope " +
	"needed."

func historyToolGuidanceMessage() *model.Message {
	return &model.Message{Role: model.RoleSystem, Content: historyToolGuidanceContent}
}
