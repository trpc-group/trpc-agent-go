//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package zset

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stateInitializationGeneration struct {
	Storage  string `json:"storage"`
	Revision string `json:"revision,omitempty"`
}

func encodeStateInitializationGeneration(storage, revision string) (string, error) {
	raw, err := json.Marshal(stateInitializationGeneration{
		Storage: storage, Revision: revision,
	})
	return string(raw), err
}

func decodeStateInitializationGeneration(raw string) stateInitializationGeneration {
	var generation stateInitializationGeneration
	if json.Unmarshal([]byte(raw), &generation) == nil && generation.Storage != "" {
		return generation
	}
	return stateInitializationGeneration{Storage: raw}
}

// StateInitializationLeaseKey returns a lease key in the session's Redis
// Cluster hash slot. stateKeyDigest must not contain the raw state key.
func (c *Client) StateInitializationLeaseKey(
	key session.Key,
	stateKeyDigest string,
) string {
	return c.prefixedKey(fmt.Sprintf(
		"stateinit:{%s}:%s:%s:%s",
		key.AppName,
		key.UserID,
		key.SessionID,
		stateKeyDigest,
	))
}

// LoadSessionStateValue loads one raw session-scoped state value.
func (c *Client) LoadSessionStateValue(
	ctx context.Context,
	key session.Key,
	stateKey string,
) ([]byte, bool, string, bool, error) {
	loaded, err := c.runScript(
		ctx,
		luaLoadStateInitializationValue,
		[]string{c.sessionStateKey(key), c.revisionKey(key)},
		key.SessionID,
		uuid.NewString(),
	).StringSlice()
	if err == redis.Nil {
		return nil, false, "", false, nil
	}
	if err != nil {
		return nil, false, "", false, fmt.Errorf("load session state value: %w", err)
	}
	if len(loaded) != 2 {
		return nil, false, "", false, fmt.Errorf(
			"load session state value: unexpected script result length %d",
			len(loaded),
		)
	}
	var stored SessionState
	if err := json.Unmarshal([]byte(loaded[0]), &stored); err != nil {
		return nil, false, "", true, fmt.Errorf("load session state value: unmarshal session state: %w", err)
	}
	if stored.Generation == "" {
		return nil, false, "", true, fmt.Errorf("load session state value: session generation is missing")
	}
	generation, err := encodeStateInitializationGeneration(stored.Generation, loaded[1])
	if err != nil {
		return nil, false, "", true, fmt.Errorf("load session state value: encode generation: %w", err)
	}
	value, present := stored.State[stateKey]
	return cloneStateInitializationValue(value), present, generation, true, nil
}

// CommitStateInitialization atomically persists a primary value and its
// projections, then releases the owner-token-checked lease. It returns 1 on
// success, 0 when ownership was lost, -1 when the session disappeared, and -2
// when the session generation changed. It returns -3 when private session
// revision metadata changed after the value was loaded.
func (c *Client) CommitStateInitialization(
	ctx context.Context,
	key session.Key,
	stateKey string,
	value []byte,
	generation string,
	leaseKey string,
	ownerToken string,
	projections ...session.StateMap,
) (int, error) {
	decodedGeneration := decodeStateInitializationGeneration(generation)
	revisionJSON := decodedGeneration.Revision
	write := sessionrevision.NewWrite(ctx, nil)
	record := &sessionrevision.PersistedRecord{}
	if revisionJSON != "" {
		if err := json.Unmarshal([]byte(revisionJSON), record); err != nil {
			return 0, fmt.Errorf("commit state initialization: unmarshal revision: %w", err)
		}
	}
	if err := sessionrevision.CheckWrite(record, write); err != nil {
		return 0, err
	}
	write.ExpectedGeneration = record.Generation
	write.HasExpectedGeneration = true
	sessionrevision.ApplyWrite(record, write)
	updatedRevision, err := json.Marshal(record)
	if err != nil {
		return 0, fmt.Errorf("commit state initialization: marshal revision: %w", err)
	}
	ttlMillis := c.cfg.SessionTTL.Milliseconds()
	if c.cfg.SessionTTL > 0 && ttlMillis == 0 {
		ttlMillis = 1
	}
	nilSentinel := "__TRPC_AGENT_GO_STATE_INITIALIZATION_NULL_" +
		strings.ReplaceAll(uuid.NewString(), "-", "") + "__"
	state := map[string]string{stateKey: encodeStateInitializationValue(value, nilSentinel)}
	for _, projection := range projections {
		for projectedKey, projectedValue := range projection {
			if _, exists := state[projectedKey]; exists {
				return 0, fmt.Errorf(
					"commit state initialization: duplicate state key %q",
					projectedKey,
				)
			}
			state[projectedKey] = encodeStateInitializationValue(projectedValue, nilSentinel)
		}
	}
	encodedState, err := json.Marshal(state)
	if err != nil {
		return 0, fmt.Errorf("commit state initialization: marshal state: %w", err)
	}
	result, err := c.runScript(
		ctx,
		luaCommitStateInitialization,
		[]string{leaseKey, c.sessionStateKey(key), c.revisionKey(key)},
		ownerToken,
		key.SessionID,
		string(encodedState),
		nilSentinel,
		decodedGeneration.Storage,
		time.Now().UTC().Format(time.RFC3339Nano),
		ttlMillis,
		revisionJSON,
		string(updatedRevision),
	).Int()
	if err != nil {
		return 0, fmt.Errorf("commit state initialization: %w", err)
	}
	return result, nil
}

func encodeStateInitializationValue(value []byte, nilSentinel string) string {
	if value == nil {
		return nilSentinel
	}
	return base64.StdEncoding.EncodeToString(value)
}

func cloneStateInitializationValue(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
