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
	metadata map[string]any
}

func (p testProvider) Metadata() map[string]any {
	return p.metadata
}

func TestFrom(t *testing.T) {
	assert.Equal(t, Value("preserve_history"), From(testProvider{
		metadata: map[string]any{
			MetadataKey: Value("preserve_history"),
		},
	}))
	assert.Empty(t, From(testProvider{metadata: map[string]any{
		MetadataKey: "preserve_history",
	}}))
	assert.Empty(t, From(testProvider{}))
	assert.Empty(t, From(struct{}{}))
	assert.Empty(t, From(nil))
}
