//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cases

import (
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// SingleTurn is case 1: one user message plus one assistant reply.
// It guards basic author/role/content fidelity and detects lost or
// duplicated events.
func SingleTurn() replaytest.Case {
	return replaytest.Case{
		Name:        "basic/single_turn",
		Description: "single user+assistant turn; event fidelity, no loss, no duplication",
		NeedCaps:    replaytest.Capability{Session: true},
		Steps: []replaytest.Step{
			createSession("basic-1"),
			userMsg("basic-1", "inv-b1-1", "介绍一下 Go 语言"),
			assistantMsg("basic-1", "inv-b1-1", "Go 是一门静态类型、编译型的编程语言。"),
		},
	}
}

// MultiTurnOrder is case 2: six alternating turns with numbered content.
// It guards read-back order equals write order.
func MultiTurnOrder() replaytest.Case {
	return replaytest.Case{
		Name:        "basic/multi_turn_order",
		Description: "six alternating turns; read-back order must equal write order",
		NeedCaps:    replaytest.Capability{Session: true},
		Steps: append(
			[]replaytest.Step{createSession("basic-2")},
			seqEvents("basic-2", 6)...,
		),
	}
}
