//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// package
// Package epochtime provides EpochTime type to be compatible with ADK Web.
package epochtime

import (
	"encoding/json"
	"time"
)

const (
	// zeroEpochLiteral is the literal for zero epoch.
	zeroEpochLiteral = "0"
	// nanosecondsPerSecond is the number of nanoseconds per second.
	nanosecondsPerSecond = float64(time.Second)
)

// EpochTime wraps time.Time to (un)marshal as unix seconds (float) like ADK.
type EpochTime struct{ time.Time }

// MarshalJSON implements json.Marshaler to encode time as unix seconds (float).
func (t EpochTime) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte(zeroEpochLiteral), nil
	}
	unixSeconds := float64(t.Time.UnixNano()) / nanosecondsPerSecond
	return json.Marshal(unixSeconds)
}

// UnmarshalJSON implements json.Unmarshaler to decode unix seconds (float).
func (t *EpochTime) UnmarshalJSON(b []byte) error {
	var unixSeconds float64
	if err := json.Unmarshal(b, &unixSeconds); err != nil {
		return err
	}
	t.Time = time.Unix(0, int64(unixSeconds*nanosecondsPerSecond)).UTC()
	return nil
}
