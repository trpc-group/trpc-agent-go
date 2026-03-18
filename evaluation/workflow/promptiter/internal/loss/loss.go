//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package loss provides internal helpers for PromptIter loss semantics.
package loss

import "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"

// SeverityRank maps one loss severity to a stable sort rank.
func SeverityRank(severity promptiter.LossSeverity) int {
	switch severity {
	case promptiter.LossSeverityP0:
		return 0
	case promptiter.LossSeverityP1:
		return 1
	case promptiter.LossSeverityP2:
		return 2
	case promptiter.LossSeverityP3:
		return 3
	default:
		return 4
	}
}
