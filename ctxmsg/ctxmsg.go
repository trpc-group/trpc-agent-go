//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package ctxmsg provides a context-attached message container for passing structured data through an execution flow.
// It currently supports metadata and may be extended with additional fields.
package ctxmsg

import (
	"context"
	"sync"
)

// ContextKey is the key type used to store message related values in a context.
type ContextKey string

// ContextKeyMessage is the key used to store the message in a context.
const ContextKeyMessage = ContextKey("TRPC_AGENT_MESSAGE")

// Msg represents a context-attached container that can carry runtime fields.
type Msg interface {
	// Metadata returns the current metadata map.
	Metadata() MetaData
	// SetMetadata replaces the message metadata with the provided map.
	SetMetadata(MetaData)
}

// EnsureMessage ensures ctx contains a message and returns it.
func EnsureMessage(ctx context.Context) (context.Context, Msg) {
	if m, ok := ctx.Value(ContextKeyMessage).(*msg); ok {
		return ctx, m
	}
	return withNewMessage(ctx)
}

func withNewMessage(ctx context.Context) (context.Context, Msg) {
	m := msgPool.Get().(*msg)
	ctx = context.WithValue(ctx, ContextKeyMessage, m)
	m.context = ctx
	return ctx, m
}

// Message returns the message stored in ctx.
func Message(ctx context.Context) Msg {
	if m, ok := ctx.Value(ContextKeyMessage).(*msg); ok {
		return m
	}
	return &msg{context: ctx}
}

// WithCloneMessage attaches a new message to ctx and returns the updated context and message.
// If ctx already contains a message, its metadata is shallow-cloned into the new message.
func WithCloneMessage(ctx context.Context) (context.Context, Msg) {
	newMsg := msgPool.Get().(*msg)
	if oldMsg, ok := ctx.Value(ContextKeyMessage).(*msg); ok {
		newMsg.metadata = oldMsg.metadata.Clone()
	}
	ctx = context.WithValue(ctx, ContextKeyMessage, newMsg)
	newMsg.context = ctx
	return ctx, newMsg
}

type msg struct {
	metadata MetaData
	context  context.Context
}

var msgPool = sync.Pool{
	New: func() any {
		return &msg{}
	},
}

// New returns a message from the pool.
func New() Msg {
	m := msgPool.Get().(*msg)
	m.resetDefault()
	m.metadata = make(MetaData)
	return m
}

func (m *msg) resetDefault() {
	m.metadata = nil
	m.context = nil
}

// PutBackMessage returns a message back to the pool.
func PutBackMessage(sourceMsg Msg) {
	m, ok := sourceMsg.(*msg)
	if !ok || m == nil {
		return
	}
	m.resetDefault()
	msgPool.Put(m)
}

// MetaData stores message metadata.
type MetaData map[string][]byte

// Clone returns a shallow copy of the metadata map.
func (m MetaData) Clone() MetaData {
	if m == nil {
		return nil
	}
	md := make(MetaData, len(m))
	for k, v := range m {
		md[k] = v
	}
	return md
}

// Metadata returns the current metadata map.
func (m *msg) Metadata() MetaData {
	if m.metadata == nil {
		m.metadata = make(MetaData)
	}
	return m.metadata
}

// SetMetadata replaces the message metadata with the provided map.
func (m *msg) SetMetadata(md MetaData) {
	m.metadata = md
}
