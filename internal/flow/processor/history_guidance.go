//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import "trpc.group/trpc-go/trpc-agent-go/model"

const historyToolGuidanceContent = "When a session summary is present, earlier details may not be included in the prompt. " +
	"If you need older context, do not guess: first call search_history to locate relevant eventIds, then call get_history_events to fetch the bounded content you need. " +
	"Use the minimum necessary scope because history tools have a budget."

func historyToolGuidanceMessage() *model.Message {
	return &model.Message{Role: model.RoleSystem, Content: historyToolGuidanceContent}
}
