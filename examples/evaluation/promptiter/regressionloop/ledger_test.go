//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLedgerChargesRejectedRoundsAndRetries(t *testing.T) {
	ledger := newLedger()
	ledger.record(modelCall{Stage: "baseline", Role: "candidate", PromptTokens: 10, CompletionTokens: 5})
	ledger.record(modelCall{Stage: "round-1", Role: "worker", PromptTokens: 4, CompletionTokens: 2})
	ledger.record(modelCall{Stage: "round-2", Role: "worker", PromptTokens: 4, CompletionTokens: 2})

	assert.Equal(t, 3, ledger.snapshot().ModelCalls.Value)
	assert.Equal(t, 27, ledger.snapshot().Tokens.Value)
}

func TestLedgerReservationUsesCumulativeValuesAndExplicitZero(t *testing.T) {
	ledger := newLedger()
	ledger.record(modelCall{PromptTokens: 2, CompletionTokens: 1})
	limit := 3
	err := ledger.canReserve(usageSummary{Tokens: knownInt(1)}, gatePolicy{MaxTokens: &limit})
	require.ErrorContains(t, err, "tokens")

	zero := 0
	err = newLedger().canReserve(
		usageSummary{ModelCalls: knownInt(1)},
		gatePolicy{MaxModelCalls: &zero},
	)
	require.ErrorContains(t, err, "model calls")
}

func TestLedgerReservationRejectsUnknownMeasurement(t *testing.T) {
	limit := 1
	err := newLedger().canReserve(usageSummary{}, gatePolicy{MaxTokens: &limit})
	require.ErrorContains(t, err, "unknown")
}
