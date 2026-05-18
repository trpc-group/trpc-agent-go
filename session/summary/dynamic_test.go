//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type dynamicTestContextKey string

func TestNewDynamicSummarizer_NilResolver(t *testing.T) {
	assert.Nil(t, NewDynamicSummarizer(nil))
}

func TestDynamicSummarizer_DelegatesToResolvedSummarizer(t *testing.T) {
	sess := &session.Session{ID: "sid"}
	resolved := &dynamicFakeSummarizer{
		allow:   true,
		summary: "dynamic-summary",
	}
	ctx := context.WithValue(context.Background(), dynamicTestContextKey("route"), "tenant-a")
	var capturedCtx context.Context
	var capturedSess *session.Session

	s := NewDynamicSummarizer(func(
		ctx context.Context,
		sess *session.Session,
	) (SessionSummarizer, error) {
		capturedCtx = ctx
		capturedSess = sess
		return resolved, nil
	})

	contextual, ok := s.(ContextAwareSummarizer)
	require.True(t, ok)
	require.True(t, contextual.ShouldSummarizeWithContext(ctx, sess))
	assert.Equal(t, "tenant-a", capturedCtx.Value(dynamicTestContextKey("route")))
	assert.Same(t, sess, capturedSess)

	text, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	assert.Equal(t, "dynamic-summary", text)
}

func TestDynamicSummarizer_LegacyMethodsAndEmptyResolver(t *testing.T) {
	sess := &session.Session{ID: "sid"}
	s := NewDynamicSummarizer(func(
		context.Context,
		*session.Session,
	) (SessionSummarizer, error) {
		return &dynamicFakeSummarizer{allow: true}, nil
	})

	assert.True(t, s.ShouldSummarize(sess))
	s.SetPrompt("ignored")
	s.SetModel(nil)

	var nilDynamic *dynamicSummarizer
	assert.False(t, nilDynamic.ShouldSummarizeWithContext(context.Background(), sess))

	emptyDynamic := &dynamicSummarizer{}
	assert.False(t, emptyDynamic.ShouldSummarizeWithContext(context.Background(), sess))
}

func TestDynamicSummarizer_NilResolvedSummarizer(t *testing.T) {
	s := NewDynamicSummarizer(func(
		context.Context,
		*session.Session,
	) (SessionSummarizer, error) {
		return nil, nil
	})

	contextual, ok := s.(ContextAwareSummarizer)
	require.True(t, ok)
	assert.False(t, contextual.ShouldSummarizeWithContext(context.Background(), &session.Session{}))

	_, err := s.Summarize(context.Background(), &session.Session{})
	require.ErrorIs(t, err, errNoDynamicSummarizerResolved)
}

func TestDynamicSummarizer_ResolverError(t *testing.T) {
	wantErr := errors.New("resolve failed")
	s := NewDynamicSummarizer(func(
		context.Context,
		*session.Session,
	) (SessionSummarizer, error) {
		return nil, wantErr
	})

	contextual, ok := s.(ContextAwareSummarizer)
	require.True(t, ok)
	assert.False(t, contextual.ShouldSummarizeWithContext(context.Background(), &session.Session{}))

	_, err := s.Summarize(context.Background(), &session.Session{})
	require.ErrorIs(t, err, wantErr)
}

func TestDynamicSummarizer_PrefersContextAwareGate(t *testing.T) {
	resolved := &dynamicContextAwareSummarizer{}
	s := NewDynamicSummarizer(func(
		context.Context,
		*session.Session,
	) (SessionSummarizer, error) {
		return resolved, nil
	})

	contextual, ok := s.(ContextAwareSummarizer)
	require.True(t, ok)
	ctx := context.WithValue(context.Background(), dynamicTestContextKey("allow"), true)

	assert.True(t, contextual.ShouldSummarizeWithContext(ctx, &session.Session{}))
	assert.True(t, resolved.contextGateCalled)
	assert.False(t, resolved.legacyGateCalled)
}

func TestDynamicSummarizer_Metadata(t *testing.T) {
	s := NewDynamicSummarizer(func(
		context.Context,
		*session.Session,
	) (SessionSummarizer, error) {
		return &dynamicFakeSummarizer{}, nil
	})

	assert.Equal(t, metadataDynamicSummarizerType, s.Metadata()["type"])
}

type dynamicFakeSummarizer struct {
	allow   bool
	summary string
}

func (f *dynamicFakeSummarizer) ShouldSummarize(*session.Session) bool {
	return f.allow
}

func (f *dynamicFakeSummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	return f.summary, nil
}

func (f *dynamicFakeSummarizer) SetPrompt(string) {}

func (f *dynamicFakeSummarizer) SetModel(model.Model) {}

func (f *dynamicFakeSummarizer) Metadata() map[string]any {
	return map[string]any{}
}

type dynamicContextAwareSummarizer struct {
	legacyGateCalled  bool
	contextGateCalled bool
}

func (f *dynamicContextAwareSummarizer) ShouldSummarize(*session.Session) bool {
	f.legacyGateCalled = true
	return false
}

func (f *dynamicContextAwareSummarizer) ShouldSummarizeWithContext(
	ctx context.Context,
	_ *session.Session,
) bool {
	f.contextGateCalled = true
	allow, _ := ctx.Value(dynamicTestContextKey("allow")).(bool)
	return allow
}

func (f *dynamicContextAwareSummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	return "context-aware-summary", nil
}

func (f *dynamicContextAwareSummarizer) SetPrompt(string) {}

func (f *dynamicContextAwareSummarizer) SetModel(model.Model) {}

func (f *dynamicContextAwareSummarizer) Metadata() map[string]any {
	return map[string]any{}
}
