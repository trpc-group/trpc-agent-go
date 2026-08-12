//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package updatepolicy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testProvider struct {
	policy Value
}

func (p testProvider) ConfiguredUpdatePolicy() Value {
	return p.policy
}

func TestFrom(t *testing.T) {
	assert.Equal(t, Value("preserve_history"), From(testProvider{
		policy: Value("preserve_history"),
	}))
	assert.Empty(t, From(testProvider{}))
	assert.Empty(t, From(struct{}{}))
	assert.Empty(t, From(nil))
}

func TestWorkerConfiguration(t *testing.T) {
	_, ok := WorkerConfiguration(context.Background())
	assert.False(t, ok)

	ctx := WithWorkerConfiguration(context.Background(), Value("append_only"))
	policy, ok := WorkerConfiguration(ctx)
	assert.True(t, ok)
	assert.Equal(t, Value("append_only"), policy)
}
