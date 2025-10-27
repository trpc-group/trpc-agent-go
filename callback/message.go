//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package callback provides shared utilities for callback implementations.
package callback

// Message defines the interface for storing and retrieving callback data.
// This interface is shared across agent, model, and tool callbacks.
type Message interface {
	// Set stores a value with the given key.
	Set(key string, value any)
	// Get retrieves a value by key.
	// Returns (value, true) if found, otherwise (nil, false).
	Get(key string) (any, bool)
	// Delete removes a value by key.
	Delete(key string)
	// Clear removes all stored values.
	Clear()
}

// message is the implementation of Message interface.
type message struct {
	store map[string]any
}

// NewMessage creates a new Message instance.
func NewMessage() Message {
	return &message{
		store: make(map[string]any),
	}
}

// Set stores a value with the given key.
func (m *message) Set(key string, value any) {
	m.store[key] = value
}

// Get retrieves a value by key.
func (m *message) Get(key string) (any, bool) {
	v, ok := m.store[key]
	return v, ok
}

// Delete removes a value by key.
func (m *message) Delete(key string) {
	delete(m.store, key)
}

// Clear removes all stored values.
func (m *message) Clear() {
	clear(m.store)
}
