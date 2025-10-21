//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"reflect"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Test that enabling cache results in node function being executed only once
// for identical inputs, and the second run hits the cache path.
func TestNodeCache_HitSkipsExecution(t *testing.T) {
	// Schema with a single integer field
	schema := NewStateSchema().
		AddField("n", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer}).
		AddField("out", StateField{Type: reflect.TypeOf((*any)(nil)).Elem(), Reducer: DefaultReducer})

	// Counter to track actual executions of the node function
	var callCount int
	worker := func(ctx context.Context, st State) (any, error) {
		callCount++
		n := st["n"].(int)
		return State{"out": n + 1}, nil
	}

	// Build graph with cache enabled at graph level
	sg := NewStateGraph(schema).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("work", worker).
		SetEntryPoint("work").
		SetFinishPoint("work")

	g, err := sg.Compile()
	require.NoError(t, err)

	ex, err := NewExecutor(g)
	require.NoError(t, err)

	// First run: should execute the node
	inv1 := &agent.Invocation{InvocationID: "cache-run-1"}
	ch1, err := ex.Execute(context.Background(), State{"n": 41}, inv1)
	require.NoError(t, err)

	// Drain events and capture final state to ensure correctness
	var final1 State
	for e := range ch1 {
		if e.Done && e.StateDelta != nil {
			final1 = decodeStateDelta(e.StateDelta)
		}
	}
	require.Equal(t, 1, callCount, "first run should call node once")
	require.Equal(t, 42, asInt(final1["out"]))

	// Second run with the same input: should hit cache, not execute node
	inv2 := &agent.Invocation{InvocationID: "cache-run-2"}
	ch2, err := ex.Execute(context.Background(), State{"n": 41}, inv2)
	require.NoError(t, err)

	// Observe node.complete events and ensure _cache_hit marker exists once
	var final2 State
	var sawCacheHit bool
	for e := range ch2 {
		if e.Response != nil && e.Response.Object == ObjectTypeGraphNodeComplete {
			if e.StateDelta != nil {
				if _, ok := e.StateDelta[MetadataKeyCacheHit]; ok {
					sawCacheHit = true
				}
			}
		}
		if e.Done && e.StateDelta != nil {
			final2 = decodeStateDelta(e.StateDelta)
		}
	}
	require.True(t, sawCacheHit, "expected _cache_hit marker on second run")
	require.Equal(t, 1, callCount, "second run should not call node function")
	require.Equal(t, 42, asInt(final2["out"]))
}

// Test TTL expiry: a short TTL should force re-computation after expiration.
func TestNodeCache_TTLExpires(t *testing.T) {
	schema := NewStateSchema().
		AddField("n", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer}).
		AddField("out", StateField{Type: reflect.TypeOf((*any)(nil)).Elem(), Reducer: DefaultReducer})

	var callCount int
	worker := func(ctx context.Context, st State) (any, error) {
		callCount++
		return State{"out": st["n"].(int) + 100}, nil
	}

	// Node-level TTL 20ms, graph-level cache backend
	pol := &CachePolicy{KeyFunc: DefaultCachePolicy().KeyFunc, TTL: 20 * time.Millisecond}
	sg := NewStateGraph(schema).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("work", worker, WithNodeCachePolicy(pol)).
		SetEntryPoint("work").
		SetFinishPoint("work")

	g, err := sg.Compile()
	require.NoError(t, err)
	ex, err := NewExecutor(g)
	require.NoError(t, err)

	// First run
	ch1, err := ex.Execute(context.Background(), State{"n": 1}, &agent.Invocation{InvocationID: "ttl-1"})
	require.NoError(t, err)
	drain(ch1)
	require.Equal(t, 1, callCount)

	// Second run within TTL: should use cache
	ch2, err := ex.Execute(context.Background(), State{"n": 1}, &agent.Invocation{InvocationID: "ttl-2"})
	require.NoError(t, err)
	drain(ch2)
	require.Equal(t, 1, callCount)

	// Wait for TTL to expire
	time.Sleep(50 * time.Millisecond)

	// Third run after TTL expiry: should recompute
	ch3, err := ex.Execute(context.Background(), State{"n": 1}, &agent.Invocation{InvocationID: "ttl-3"})
	require.NoError(t, err)
	drain(ch3)
	require.Equal(t, 2, callCount)
}

// Test clearing cache by node ID.
func TestNodeCache_ClearCacheByNode(t *testing.T) {
	schema := NewStateSchema().
		AddField("n", StateField{Type: reflect.TypeOf(0), Reducer: DefaultReducer}).
		AddField("out", StateField{Type: reflect.TypeOf((*any)(nil)).Elem(), Reducer: DefaultReducer})

	var callCount int
	worker := func(ctx context.Context, st State) (any, error) {
		callCount++
		return State{"out": st["n"].(int) * 2}, nil
	}

	sg := NewStateGraph(schema).
		WithCache(NewInMemoryCache()).
		WithCachePolicy(DefaultCachePolicy())
	sg.AddNode("work", worker).
		SetEntryPoint("work").
		SetFinishPoint("work")

	g, err := sg.Compile()
	require.NoError(t, err)
	ex, err := NewExecutor(g)
	require.NoError(t, err)

	// First run computes and caches
	ch1, err := ex.Execute(context.Background(), State{"n": 3}, &agent.Invocation{InvocationID: "clear-1"})
	require.NoError(t, err)
	drain(ch1)
	require.Equal(t, 1, callCount)

	// Second run hits cache
	ch2, err := ex.Execute(context.Background(), State{"n": 3}, &agent.Invocation{InvocationID: "clear-2"})
	require.NoError(t, err)
	drain(ch2)
	require.Equal(t, 1, callCount)

	// Clear cache for the node and run again: should recompute
	sg.ClearCache("work")
	ch3, err := ex.Execute(context.Background(), State{"n": 3}, &agent.Invocation{InvocationID: "clear-3"})
	require.NoError(t, err)
	drain(ch3)
	require.Equal(t, 2, callCount)
}

// Helpers
func decodeStateDelta(delta map[string][]byte) State {
	out := make(State)
	for k, v := range delta {
		switch k {
		case MetadataKeyNode, MetadataKeyPregel, MetadataKeyChannel, MetadataKeyState, MetadataKeyCompletion:
			continue
		}
		var anyv any
		_ = json.Unmarshal(v, &anyv)
		out[k] = anyv
	}
	return out
}

func drain(ch <-chan *event.Event) {
	for range ch {
		// discard
	}
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	default:
		return 0
	}
}
