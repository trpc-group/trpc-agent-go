//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package flow

import "trpc.group/trpc-go/trpc-agent-go/model"

// InsertFewShotMessages inserts few-shot examples after the leading system message block.
func InsertFewShotMessages(
	messages []model.Message,
	examples [][]model.Message,
) []model.Message {
	fewShot := flattenFewShotMessages(examples)
	if len(fewShot) == 0 {
		return messages
	}
	insertAt := 0
	for insertAt < len(messages) && messages[insertAt].Role == model.RoleSystem {
		insertAt++
	}
	out := make([]model.Message, 0, len(messages)+len(fewShot))
	out = append(out, messages[:insertAt]...)
	out = append(out, fewShot...)
	out = append(out, messages[insertAt:]...)
	return out
}

func flattenFewShotMessages(examples [][]model.Message) []model.Message {
	if len(examples) == 0 {
		return nil
	}
	total := 0
	for _, group := range examples {
		total += len(group)
	}
	if total == 0 {
		return nil
	}
	out := make([]model.Message, 0, total)
	for _, group := range examples {
		out = append(out, group...)
	}
	return out
}
