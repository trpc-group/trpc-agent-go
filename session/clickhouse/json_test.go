//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestUnmarshalStoredJSONNormalizesClickHouseNumericFields(t *testing.T) {
	data := `{"author":"agent","choices":[{"index":"0","message":{"role":"assistant","content":"ok"}}],"created":"1","id":"event-id","invocationId":"invocation-id","timestamp":"2026-07-28T00:00:00Z","version":"1"}`

	var got event.Event
	require.NoError(t, unmarshalStoredJSON(data, &got))
	require.NotNil(t, got.Response)
	require.Equal(t, int64(1), got.Created)
	require.Equal(t, 0, got.Choices[0].Index)
	require.Equal(t, 1, got.Version)
}

func TestUnmarshalStoredJSONAcceptsQuotedDocument(t *testing.T) {
	data := `"{\"id\":\"event-id\",\"invocationId\":\"invocation-id\",\"timestamp\":\"2026-07-28T00:00:00Z\",\"version\":1}"`

	var got event.Event
	require.NoError(t, unmarshalStoredJSON(data, &got))
	require.Equal(t, "event-id", got.ID)
	require.Equal(t, 1, got.Version)
}
