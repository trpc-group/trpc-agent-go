//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2a

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeStateDeltaMetadata_PublicWrapper(t *testing.T) {
	original := map[string][]byte{
		"foo": []byte(`{"k":"v"}`),
		"bar": nil,
	}

	encoded := EncodeStateDeltaMetadata(original)
	decoded := DecodeStateDeltaMetadata(encoded)

	require.Equal(t, original, decoded)
}
