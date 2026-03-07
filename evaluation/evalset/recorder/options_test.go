//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
)

func TestRecorder_OptionValidation(t *testing.T) {
	_, err := New(inmemory.New(), WithName(""))
	require.ErrorContains(t, err, "plugin name is empty")
	_, err = New(inmemory.New(), WithWriteTimeout(-time.Second))
	require.ErrorContains(t, err, "write timeout is negative")
	rec, err := New(inmemory.New(), WithName("named-recorder"))
	require.NoError(t, err)
	assert.Equal(t, "named-recorder", rec.Name())
}
