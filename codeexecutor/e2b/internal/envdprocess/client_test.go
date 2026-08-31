//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdprocess

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientRejectsInvalidBaseURL(t *testing.T) {
	_, err := NewClient("not-a-url", nil, nil)
	require.Error(t, err)
}

func TestNewClientUsesDefaultHTTPClient(t *testing.T) {
	client, err := NewClient("https://envd.example", nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, client.processClient)
}
