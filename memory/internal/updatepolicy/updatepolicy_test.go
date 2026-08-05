//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package updatepolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type testProvider struct {
	value Value
}

func (p testProvider) AutoMemoryUpdatePolicy() Value {
	return p.value
}

func TestFrom(t *testing.T) {
	assert.Equal(t, Value("preserve_history"), From(testProvider{
		value: "preserve_history",
	}))
	assert.Empty(t, From(struct{}{}))
	assert.Empty(t, From(nil))
}
