//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()

	assert.NotNil(t, opts.UserIDResolver)
	userID, err := opts.UserIDResolver(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	assert.Equal(t, "user", userID)
	assert.Nil(t, opts.TranslateCallbacks)

	assert.NotNil(t, opts.TranslatorFactory)
	input := &adapter.RunAgentInput{ThreadID: "thread-1", RunID: "run-1"}
	tr := opts.TranslatorFactory(input)
	assert.NotNil(t, tr)
	assert.IsType(t, translator.New("", ""), tr)

	assert.NotNil(t, opts.RunAgentInputHook)
	modified, err := opts.RunAgentInputHook(context.Background(), input)
	assert.NoError(t, err)
	assert.Same(t, input, modified)
}

func TestWithUserIDResolver(t *testing.T) {
	wantErr := errors.New("resolver error")
	called := false
	customResolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
		called = true
		return "custom", wantErr
	}

	opts := NewOptions(WithUserIDResolver(customResolver))

	userID, err := opts.UserIDResolver(context.Background(), &adapter.RunAgentInput{})
	assert.Equal(t, wantErr, err)
	assert.Equal(t, "custom", userID)
	assert.True(t, called)
}

func TestWithTranslatorFactory(t *testing.T) {
	customTranslator := translator.New("custom-thread", "custom-run")
	factoryCalled := false
	opts := NewOptions(WithTranslatorFactory(func(input *adapter.RunAgentInput) translator.Translator {
		factoryCalled = true
		return customTranslator
	}))

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	tr := opts.TranslatorFactory(input)

	assert.True(t, factoryCalled)
	assert.Equal(t, customTranslator, tr)
}

func TestWithTranslateCallbacks(t *testing.T) {
	cb := translator.NewCallbacks()
	opts := NewOptions(WithTranslateCallbacks(cb))
	assert.Same(t, cb, opts.TranslateCallbacks)
}

func TestWithRunAgentInputHook(t *testing.T) {
	called := false
	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	custom := &adapter.RunAgentInput{ThreadID: "other-thread", RunID: "other-run"}
	hook := func(ctx context.Context, in *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
		called = true
		assert.Same(t, input, in)
		return custom, nil
	}

	opts := NewOptions(WithRunAgentInputHook(hook))

	got, err := opts.RunAgentInputHook(context.Background(), input)
	assert.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, custom, got)
}

func TestWithAppName(t *testing.T) {
	opts := NewOptions(WithAppName("custom-app"))
	assert.Equal(t, "custom-app", opts.AppName)
}

func TestWithSessionService(t *testing.T) {
	opts := NewOptions(WithSessionService(inmemory.NewSessionService()))
	assert.NotNil(t, opts.SessionService)
}
