//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWithTag_Single verifies that a single tag is applied without a leading delimiter.
func TestWithTag_Single(t *testing.T) {
	e := New("inv-id", "author", WithTag("alpha"))
	require.Equal(t, "alpha", e.Tag)
}

// TestWithTag_Multiple verifies that subsequent tags are appended using TagDelimiter.
func TestWithTag_Multiple(t *testing.T) {
	e := New("inv-id", "author", WithTag("alpha"))
	// Append additional tags.
	WithTag("beta")(e)
	WithTag("gamma")(e)

	require.Equal(t, "alpha"+TagDelimiter+"beta"+TagDelimiter+"gamma", e.Tag)
}
