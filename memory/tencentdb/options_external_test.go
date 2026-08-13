//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
)

func TestOptionsKeyedLiteralAndCustomOptionRemainSupported(t *testing.T) {
	options := memorytencentdb.Options{
		GatewayURL: "http://127.0.0.1:8420",
		Timeout:    time.Second,
	}
	customOption := memorytencentdb.Option(func(got *memorytencentdb.Options) {
		got.GatewayURL = options.GatewayURL
		got.Timeout = options.Timeout
	})
	svc, err := memorytencentdb.NewService(customOption)
	require.NoError(t, err)
	require.NoError(t, svc.Close())
}
