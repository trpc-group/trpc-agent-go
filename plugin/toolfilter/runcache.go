//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"container/list"
	"context"
	"sync"

	"golang.org/x/sync/singleflight"
)

// runCache is a fixed-size LRU cache keyed by string. It is safe for
// concurrent use.
type runCache[T any] struct {
	mu     sync.Mutex
	max    int
	order  *list.List               // front = most recent
	items  map[string]*list.Element // element.Value = *entry[T]
	flight singleflight.Group
}

// getOrCompute shares in-flight work by key. A canceled caller stops waiting;
// the context captured by compute controls the shared work itself.
func (c *runCache[T]) getOrCompute(ctx context.Context, key string, compute func() (T, bool)) (T, bool) {
	if value, ok := c.get(key); ok {
		return value, true
	}
	result := c.flight.DoChan(key, func() (any, error) {
		if value, ok := c.get(key); ok {
			return entry[T]{value: value}, nil
		}
		value, cacheable := compute()
		if cacheable {
			c.set(key, value)
		}
		return entry[T]{value: value}, nil
	})
	select {
	case <-ctx.Done():
		var zero T
		return zero, false
	case result := <-result:
		return result.Val.(entry[T]).value, true
	}
}

type entry[T any] struct {
	key   string
	value T
}

func newRunCache[T any](max int) *runCache[T] {
	if max < 1 {
		max = 1
	}
	return &runCache[T]{
		max:   max,
		order: list.New(),
		items: make(map[string]*list.Element, max),
	}
}

// get returns the cached value for key when present, marking it recent.
func (c *runCache[T]) get(key string) (T, bool) {
	var zero T
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry[T]).value, true
}

// set stores the value for key, evicting the least recently used entry when
// the cache is full.
func (c *runCache[T]) set(key string, value T) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*entry[T]).value = value
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&entry[T]{key: key, value: value})
	c.items[key] = el
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry[T]).key)
	}
}

// len reports the number of cached entries, for tests.
func (c *runCache[T]) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
