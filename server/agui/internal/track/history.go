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
	"errors"
	"fmt"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const historyRunOrderKey = session.StateTempPrefix + "agui:history_run_order"

type historyRunKey struct{}

// HistoryRunOrder orders admitted runs within a session generation.
type HistoryRunOrder struct {
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
}

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
func historyPayload(payload json.RawMessage, runID string, order json.RawMessage) json.RawMessage {
	raw := bytes.TrimSpace(payload)
	if runID == "" || len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return bytes.Clone(payload)
	}
	id, _ := json.Marshal(runID)
	const orderField = `,"trpcRunOrder":`
	size := len(raw) + len(id) + len(`,"trpcRunId":`)
	if len(order) != 0 {
		size += len(orderField) + len(order)
	}
	result := make(json.RawMessage, 0, size)
	result = append(result, raw[:len(raw)-1]...)
	if len(bytes.TrimSpace(raw[1:len(raw)-1])) != 0 {
		result = append(result, ',')
	}
	result = append(result, `"trpcRunId":`...)
	result = append(result, id...)
	if len(order) != 0 {
		result = append(result, orderField...)
		result = append(result, order...)
	}
	return append(result, '}')
}

// HistoryRunID returns the internal run identity from an AG-UI track event.
func HistoryRunID(payload json.RawMessage) string {
	id, _ := HistoryRun(payload)
	return id
}

// HistoryRun returns the identity and optional shared admission order of a stored event.
func HistoryRun(payload json.RawMessage) (string, HistoryRunOrder) {
	if !bytes.Contains(payload, []byte(`"trpcRunId"`)) {
		return "", HistoryRunOrder{}
	}
	var metadata struct {
		RunID string          `json:"trpcRunId"`
		Order HistoryRunOrder `json:"trpcRunOrder"`
	}
	if json.Unmarshal(payload, &metadata) != nil {
		return "", HistoryRunOrder{}
	}
	return metadata.RunID, metadata.Order
}

// AdmitHistoryRun reserves an order before startup callbacks can delay the
// first events. Services without coordinated state initialization keep the
// history readable using run identities and visible boundaries.
func (t *tracker) AdmitHistoryRun(ctx context.Context, key session.Key) error {
	service, ok := t.sessionService.(session.StateInitializationService)
	if !ok {
		return nil
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	state := t.getSessionState(ctx, key)
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	if _, err := t.ensureSessionExists(ctx, key, state); err != nil {
		return err
	}
	var current, encoded []byte
	_, initialized, err := service.LoadOrInitializeSessionState(ctx, key, historyRunOrderKey,
		func(value []byte) bool {
			if len(encoded) != 0 {
				return bytes.Equal(value, encoded)
			}
			// The service rechecks the stored value under its initialization
			// gate. Use the raw value, which session read hooks cannot modify.
			current = bytes.Clone(value)
			return false
		},
		func(context.Context) ([]byte, error) {
			order := HistoryRunOrder{Epoch: state.runID}
			if len(current) != 0 {
				if err := json.Unmarshal(current, &order); err != nil {
					return nil, fmt.Errorf("decode history run order: %w", err)
				}
			}
			if order.Epoch == "" || order.Sequence == math.MaxUint64 {
				return nil, errors.New("invalid history run order")
			}
			order.Sequence++
			var err error
			encoded, err = json.Marshal(order)
			return encoded, err
		})
	if err != nil {
		return fmt.Errorf("reserve history run order: %w", err)
	}
	if !initialized {
		return errors.New("history run order was not reserved")
	}
	state.order = encoded
	return nil
}
