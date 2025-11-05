//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package epochtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEpochTimeMarshalJSONZero(t *testing.T) {
	timestamp := EpochTime{}
	data, err := timestamp.MarshalJSON()
	assert.NoError(t, err)
	assert.Equal(t, []byte(zeroEpochLiteral), data)
}

func TestEpochTimeMarshalJSONNonZero(t *testing.T) {
	now := time.Unix(123, 456789000).UTC()
	timestamp := EpochTime{Time: now}
	data, err := timestamp.MarshalJSON()
	assert.NoError(t, err)

	var encoded float64
	err = json.Unmarshal(data, &encoded)
	assert.NoError(t, err)
	assert.InDelta(t, float64(now.UnixNano())/nanosecondsPerSecond, encoded, 1e-6)
}

func TestEpochTimeUnmarshalJSON(t *testing.T) {
	input := 321.654
	payload, err := json.Marshal(input)
	assert.NoError(t, err)

	var timestamp EpochTime
	err = timestamp.UnmarshalJSON(payload)
	assert.NoError(t, err)
	expected := time.Unix(0, int64(input*nanosecondsPerSecond)).UTC()
	assert.WithinDuration(t, expected, timestamp.Time, time.Microsecond)
}
