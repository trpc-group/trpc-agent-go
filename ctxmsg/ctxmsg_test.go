//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package ctxmsg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageMetadataAndSetMetadata(t *testing.T) {
	msg := New()

	md := msg.Metadata()
	assert.NotNil(t, md)

	md["k"] = []byte("v")
	assert.Contains(t, msg.Metadata(), "k")
	assert.Equal(t, []byte("v"), msg.Metadata()["k"])

	msg.SetMetadata(MetaData{"k": []byte("v2")})
	assert.Equal(t, []byte("v2"), msg.Metadata()["k"])

	repl := MetaData{"k": []byte("v3")}
	msg.SetMetadata(repl)
	repl["k"] = []byte("v4")
	assert.Equal(t, []byte("v4"), msg.Metadata()["k"])

	msg.SetMetadata(nil)
	assert.NotContains(t, msg.Metadata(), "k")

	PutBackMessage(msg)
}

func TestWithNewMessageAttachesMessage(t *testing.T) {
	base := context.Background()
	ctx, m := withNewMessage(base)
	assert.NotNil(t, ctx.Value(ContextKeyMessage))

	got := Message(ctx)
	assert.Same(t, m.(*msg), got.(*msg))
	PutBackMessage(m)
}

func TestWithCloneMessageCreatesMessageWhenMissing(t *testing.T) {
	base := context.Background()
	ctx, m := WithCloneMessage(base)
	assert.NotNil(t, ctx.Value(ContextKeyMessage))
	assert.Same(t, m.(*msg), Message(ctx).(*msg))
	PutBackMessage(m)
}

func TestWithCloneMessageClonesMetadataWhenPresent(t *testing.T) {
	base := context.Background()
	ctx, old := withNewMessage(base)
	old.Metadata()["a"] = []byte("value")

	ctx2, cloned := WithCloneMessage(ctx)
	assert.NotNil(t, ctx2.Value(ContextKeyMessage))
	assert.NotSame(t, old, cloned)

	assert.Equal(t, []byte("value"), cloned.Metadata()["a"])

	old.Metadata()["a"][0] = 'V'
	assert.Equal(t, old.Metadata()["a"], cloned.Metadata()["a"])

	cloned.Metadata()["new"] = []byte("x")
	_, ok := old.Metadata()["new"]
	assert.False(t, ok)

	old.Metadata()["old"] = []byte("y")
	_, ok = cloned.Metadata()["old"]
	assert.False(t, ok)

	PutBackMessage(old)
	PutBackMessage(cloned)
}

func TestMetaDataClone(t *testing.T) {
	orig := MetaData{
		"a": []byte("value"),
	}
	clone := orig.Clone()
	assert.Equal(t, orig["a"], clone["a"])

	orig["a"][0] = 'V'
	assert.Equal(t, orig["a"], clone["a"])

	clone["b"] = []byte("x")
	_, ok := orig["b"]
	assert.False(t, ok)
}

func TestMetaDataCloneNil(t *testing.T) {
	var orig MetaData
	assert.Nil(t, orig.Clone())
}

func TestMessagePoolClearsMetadata(t *testing.T) {
	msg := New()
	msg.Metadata()["k"] = []byte("v")
	PutBackMessage(msg)

	msg2 := New()
	_, ok := msg2.Metadata()["k"]
	assert.False(t, ok)
	PutBackMessage(msg2)
}

func TestMessageReturnsExistingMessage(t *testing.T) {
	base := context.Background()
	ctx, m := withNewMessage(base)
	got := Message(ctx)
	assert.Same(t, m.(*msg), got.(*msg))
	PutBackMessage(m)
}

func TestMessageReturnsStandaloneMessageWhenMissing(t *testing.T) {
	base := context.Background()
	msg := Message(base)
	msg.Metadata()["k"] = []byte("v")
	assert.Equal(t, []byte("v"), msg.Metadata()["k"])
}

func TestMessageKeepsContextWhenMissing(t *testing.T) {
	base := context.Background()
	m := Message(base).(*msg)
	assert.Equal(t, base, m.context)
}

func TestEnsureMessageCreatesAndAttachesMessage(t *testing.T) {
	base := context.Background()
	ctx, m := EnsureMessage(base)
	assert.NotNil(t, m)
	assert.NotNil(t, ctx.Value(ContextKeyMessage))
	assert.Same(t, m.(*msg), Message(ctx).(*msg))
	PutBackMessage(m)
}

func TestEnsureMessageReturnsExistingMessage(t *testing.T) {
	base := context.Background()
	ctx, m := withNewMessage(base)
	ctx2, got := EnsureMessage(ctx)
	assert.Same(t, ctx, ctx2)
	assert.Same(t, m.(*msg), got.(*msg))
	PutBackMessage(m)
}

type stubMsg struct{}

func (stubMsg) Metadata() MetaData {
	return nil
}

func (stubMsg) SetMetadata(MetaData) {}

func TestPutBackMessageIgnoresNonPoolMessages(t *testing.T) {
	assert.NotPanics(t, func() { PutBackMessage(stubMsg{}) })
	assert.NotPanics(t, func() { PutBackMessage(nil) })
}
