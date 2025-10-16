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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Cache-related string constants.
const (
	// CacheNamespacePrefix is the prefix for per-node cache namespaces.
	CacheNamespacePrefix = "__writes__"
)

// Cache is a minimal interface for storing and retrieving node results.
// Implementations must be concurrency-safe.
type Cache interface {
	// Get returns the cached value for the given namespace and key.
	// ok is true when a non-expired entry was found.
	Get(ns, key string) (val any, ok bool)
	// Set stores a value for the given namespace and key with the provided TTL.
	// ttl<=0 means no expiration.
	Set(ns, key string, val any, ttl time.Duration)
	// Clear removes all entries under the namespace.
	Clear(ns string)
}

// CachePolicy configures how cache keys are derived and how long entries live.
type CachePolicy struct {
	// KeyFunc derives a stable key bytes from the task input.
	// The implementation should ensure deterministic output for equivalent inputs.
	KeyFunc func(input any) ([]byte, error)
	// TTL controls entry lifetime. TTL<=0 means no expiration.
	TTL time.Duration
}

// DefaultCachePolicy returns a best-effort default policy using canonical JSON
// to produce a stable hash of the sanitized input.
func DefaultCachePolicy() *CachePolicy {
	return &CachePolicy{
		KeyFunc: func(input any) ([]byte, error) {
			canon, err := toCanonicalValue(input)
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(canon)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(b)
			out := make([]byte, hex.EncodedLen(len(sum)))
			hex.Encode(out, sum[:])
			return out, nil
		},
		TTL: 0,
	}
}

// InMemoryCache is a simple TTL cache backed by a nested map.
// It is intended for single-process use and testing.
type InMemoryCache struct {
	mu   sync.RWMutex
	data map[string]map[string]cacheEntry // ns -> key -> entry
}

type cacheEntry struct {
	v   any
	exp time.Time // zero means no expiration
}

// NewInMemoryCache creates a new in-memory cache instance.
func NewInMemoryCache() *InMemoryCache {
	return &InMemoryCache{data: make(map[string]map[string]cacheEntry)}
}

// Get implements Cache.
func (c *InMemoryCache) Get(ns, key string) (any, bool) {
	now := time.Now()
	c.mu.RLock()
	m := c.data[ns]
	var ent cacheEntry
	var ok bool
	if m != nil {
		ent, ok = m[key]
	}
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !ent.exp.IsZero() && now.After(ent.exp) {
		// lazy expire
		c.mu.Lock()
		if mm := c.data[ns]; mm != nil {
			delete(mm, key)
		}
		c.mu.Unlock()
		return nil, false
	}
	// Return a deep copy to avoid shared references.
	return deepCopyAny(ent.v), true
}

// Set implements Cache.
func (c *InMemoryCache) Set(ns, key string, val any, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	if _, ok := c.data[ns]; !ok {
		c.data[ns] = make(map[string]cacheEntry)
	}
	// Store a deep copy to isolate cache storage from caller mutations.
	c.data[ns][key] = cacheEntry{v: deepCopyAny(val), exp: exp}
	c.mu.Unlock()
}

// Clear implements Cache.
func (c *InMemoryCache) Clear(ns string) {
	c.mu.Lock()
	delete(c.data, ns)
	c.mu.Unlock()
}

// buildCacheNamespace builds a per-node namespace for cache entries.
// We scope by node ID only to align with the executor's node semantics.
func buildCacheNamespace(nodeID string) string {
	return fmt.Sprintf("%s:%s", CacheNamespacePrefix, nodeID)
}
