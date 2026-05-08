//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package templateresolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSamplesAggregator(t *testing.T) {
	aggregator, err := ResolveSamplesAggregator("")
	require.NoError(t, err)
	assert.NotNil(t, aggregator)

	aggregator, err = ResolveSamplesAggregator(SampleAggregatorMajorityVoteName)
	require.NoError(t, err)
	assert.NotNil(t, aggregator)
}

func TestResolveSamplesAggregatorRejectsUnknownName(t *testing.T) {
	_, err := ResolveSamplesAggregator("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported samples aggregator "missing"`)
}

func TestResolveInvocationsAggregator(t *testing.T) {
	aggregator, err := ResolveInvocationsAggregator("")
	require.NoError(t, err)
	assert.NotNil(t, aggregator)

	aggregator, err = ResolveInvocationsAggregator(InvocationAggregatorAverageName)
	require.NoError(t, err)
	assert.NotNil(t, aggregator)
}

func TestResolveInvocationsAggregatorRejectsUnknownName(t *testing.T) {
	_, err := ResolveInvocationsAggregator("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unsupported invocations aggregator "missing"`)
}
