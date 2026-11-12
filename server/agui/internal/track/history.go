//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package track

import (
	"bytes"
	"context"
	"encoding/json"
)

type historyRunKey struct{}

// WithHistoryRunID associates the buffered track events with one invocation of the runner.
func WithHistoryRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, historyRunKey{}, runID)
}

func historyRunID(ctx context.Context) string {
	id, _ := ctx.Value(historyRunKey{}).(string)
	return id
}

// historyPayload adds storage metadata after aggregation so even regenerated
// text, reasoning, and tool argument events retain their originating run.
func historyPayload(payload json.RawMessage, runID string) json.RawMessage {
	raw := bytes.TrimSpace(payload)
	if runID == "" || len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return bytes.Clone(payload)
	}
	id, _ := json.Marshal(runID)
	result := make(json.RawMessage, 0, len(raw)+len(id)+16)
	result = append(result, raw[:len(raw)-1]...)
	if len(bytes.TrimSpace(raw[1:len(raw)-1])) != 0 {
		result = append(result, ',')
	}
	result = append(result, `"trpcRunId":`...)
	result = append(result, id...)
	return append(result, '}')
}

// HistoryRunID returns the internal run identity from an AG-UI track event.
func HistoryRunID(payload json.RawMessage) string {
	if !bytes.Contains(payload, []byte(`"trpcRunId"`)) {
		return ""
	}
	var metadata struct {
		RunID string `json:"trpcRunId"`
	}
	if json.Unmarshal(payload, &metadata) != nil {
		return ""
	}
	return metadata.RunID
}
