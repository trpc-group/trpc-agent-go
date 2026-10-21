//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telemetry

import (
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/trace"
)

type chatObserver struct {
	observe func(trace.Span, string, []byte)
}

var chatObservers atomic.Pointer[[]*chatObserver]
var chatObserversMu sync.Mutex

// RegisterChatAttributeObserver registers a synchronous chat payload observer.
// See the public telemetry/trace function for the observer contract.
func RegisterChatAttributeObserver(observe func(trace.Span, string, []byte)) func() {
	if observe == nil {
		return func() {}
	}
	entry := &chatObserver{observe: observe}
	chatObserversMu.Lock()
	var next []*chatObserver
	if current := chatObservers.Load(); current != nil {
		next = append(next, (*current)...)
	}
	next = append(next, entry)
	chatObservers.Store(&next)
	chatObserversMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			chatObserversMu.Lock()
			defer chatObserversMu.Unlock()
			var next []*chatObserver
			for _, candidate := range *chatObservers.Load() {
				if candidate != entry {
					next = append(next, candidate)
				}
			}
			chatObservers.Store(&next)
		})
	}
}

func chatAttributeObserver(span trace.Span) func(string, []byte) {
	current := chatObservers.Load()
	if current == nil || len(*current) == 0 {
		return nil
	}
	return func(key string, value []byte) {
		for _, entry := range *current {
			entry.observe(span, key, value)
		}
	}
}

func notifyAbsentChatAttribute(observers []func(string, []byte), key string) {
	if len(observers) > 0 && observers[0] != nil {
		observers[0](key, nil)
	}
}
