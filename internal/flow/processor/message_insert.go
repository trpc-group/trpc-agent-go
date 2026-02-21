//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import "trpc.group/trpc-go/trpc-agent-go/model"

// insertSystemMessageAfterLastSystem inserts msg after the last system message.
// If there is no system message, it prepends the msg.
func insertSystemMessageAfterLastSystem(req *model.Request, msg *model.Message) {
	if req == nil || msg == nil {
		return
	}
	idx := findLastSystemMessageIndex(req.Messages)
	if idx >= 0 {
		req.Messages = append(req.Messages[:idx+1],
			append([]model.Message{*msg}, req.Messages[idx+1:]...)...)
		return
	}
	req.Messages = append([]model.Message{*msg}, req.Messages...)
}
