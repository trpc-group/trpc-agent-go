//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"trpc.group/trpc-go/trpc-agent-go/internal/state/partsuserinput"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// retainTypedUserMessage reports whether last should be sent unchanged.
//
// It is true only when userInput is nonempty, last still carries content
// parts, and userInput equals the saved ContentParts-only projection.
// Annotations, interleaved media, and merged history on last stay in place.
// A missing projection, or any other selected input, returns false so the
// caller uses the explicit string override.
func retainTypedUserMessage(
	state State,
	last model.Message,
	userInput string,
) bool {
	if userInput == "" || len(last.ContentParts) == 0 {
		return false
	}
	original, _ := state[partsuserinput.Key].(string)
	return original != "" && userInput == original
}
