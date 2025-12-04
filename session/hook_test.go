//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestRunAppendEventHooks(t *testing.T) {
	t.Run("empty hooks calls final", func(t *testing.T) {
		called := false
		ctx := &AppendEventContext{Context: context.Background()}
		err := RunAppendEventHooks(nil, ctx, func() error {
			called = true
			return nil
		})
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("hooks execute in order", func(t *testing.T) {
		order := []string{}
		hooks := []AppendEventHook{
			func(ctx *AppendEventContext, next func() error) error {
				order = append(order, "before1")
				err := next()
				order = append(order, "after1")
				return err
			},
			func(ctx *AppendEventContext, next func() error) error {
				order = append(order, "before2")
				err := next()
				order = append(order, "after2")
				return err
			},
		}

		ctx := &AppendEventContext{Context: context.Background()}
		err := RunAppendEventHooks(hooks, ctx, func() error {
			order = append(order, "final")
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, []string{"before1", "before2", "final", "after2", "after1"}, order)
	})

	t.Run("hook can abort by not calling next", func(t *testing.T) {
		finalCalled := false
		hooks := []AppendEventHook{
			func(ctx *AppendEventContext, next func() error) error {
				return nil // skip next()
			},
		}

		ctx := &AppendEventContext{Context: context.Background()}
		err := RunAppendEventHooks(hooks, ctx, func() error {
			finalCalled = true
			return nil
		})

		assert.NoError(t, err)
		assert.False(t, finalCalled)
	})

	t.Run("hook can return error", func(t *testing.T) {
		testErr := errors.New("test error")
		hooks := []AppendEventHook{
			func(ctx *AppendEventContext, next func() error) error {
				return testErr
			},
		}

		ctx := &AppendEventContext{Context: context.Background()}
		err := RunAppendEventHooks(hooks, ctx, func() error {
			return nil
		})

		assert.Equal(t, testErr, err)
	})

	t.Run("hook can modify event", func(t *testing.T) {
		hooks := []AppendEventHook{
			func(ctx *AppendEventContext, next func() error) error {
				ctx.Event.Author = "modified"
				return next()
			},
		}

		e := event.New("inv1", "original")
		ctx := &AppendEventContext{
			Context: context.Background(),
			Event:   e,
		}
		err := RunAppendEventHooks(hooks, ctx, func() error {
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, "modified", ctx.Event.Author)
	})
}

func TestRunGetSessionHooks(t *testing.T) {
	t.Run("empty hooks calls final", func(t *testing.T) {
		called := false
		ctx := &GetSessionContext{Context: context.Background()}
		sess, err := RunGetSessionHooks(nil, ctx, func() (*Session, error) {
			called = true
			return NewSession("app", "user", "sess"), nil
		})
		assert.NoError(t, err)
		assert.True(t, called)
		assert.NotNil(t, sess)
	})

	t.Run("hooks execute in order", func(t *testing.T) {
		order := []string{}
		hooks := []GetSessionHook{
			func(ctx *GetSessionContext, next func() (*Session, error)) (*Session, error) {
				order = append(order, "before1")
				sess, err := next()
				order = append(order, "after1")
				return sess, err
			},
			func(ctx *GetSessionContext, next func() (*Session, error)) (*Session, error) {
				order = append(order, "before2")
				sess, err := next()
				order = append(order, "after2")
				return sess, err
			},
		}

		ctx := &GetSessionContext{Context: context.Background()}
		_, err := RunGetSessionHooks(hooks, ctx, func() (*Session, error) {
			order = append(order, "final")
			return NewSession("app", "user", "sess"), nil
		})

		assert.NoError(t, err)
		assert.Equal(t, []string{"before1", "before2", "final", "after2", "after1"}, order)
	})

	t.Run("hook can modify session after next", func(t *testing.T) {
		hooks := []GetSessionHook{
			func(ctx *GetSessionContext, next func() (*Session, error)) (*Session, error) {
				sess, err := next()
				if err != nil {
					return nil, err
				}
				if sess != nil {
					sess.State["modified"] = []byte("true")
				}
				return sess, nil
			},
		}

		ctx := &GetSessionContext{Context: context.Background()}
		sess, err := RunGetSessionHooks(hooks, ctx, func() (*Session, error) {
			return NewSession("app", "user", "sess"), nil
		})

		assert.NoError(t, err)
		assert.Equal(t, []byte("true"), sess.State["modified"])
	})
}
