//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hook

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRunAppendEventHooks(t *testing.T) {
	t.Run("empty hooks calls final", func(t *testing.T) {
		called := false
		ctx := &session.AppendEventContext{Context: context.Background()}
		final := func(c *session.AppendEventContext, next func() error) error {
			called = true
			return nil
		}
		err := RunAppendEventHooks(nil, ctx, final)
		assert.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("hooks execute in order", func(t *testing.T) {
		order := []string{}
		hooks := []session.AppendEventHook{
			func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "before1")
				err := next()
				order = append(order, "after1")
				return err
			},
			func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "before2")
				err := next()
				order = append(order, "after2")
				return err
			},
		}

		ctx := &session.AppendEventContext{Context: context.Background()}
		final := func(c *session.AppendEventContext, next func() error) error {
			order = append(order, "final")
			return nil
		}
		err := RunAppendEventHooks(hooks, ctx, final)

		assert.NoError(t, err)
		assert.Equal(t, []string{"before1", "before2", "final", "after2", "after1"}, order)
	})

	t.Run("hook can abort by not calling next", func(t *testing.T) {
		finalCalled := false
		hooks := []session.AppendEventHook{
			func(ctx *session.AppendEventContext, next func() error) error {
				return nil // skip next()
			},
		}

		ctx := &session.AppendEventContext{Context: context.Background()}
		final := func(c *session.AppendEventContext, next func() error) error {
			finalCalled = true
			return nil
		}
		err := RunAppendEventHooks(hooks, ctx, final)

		assert.NoError(t, err)
		assert.False(t, finalCalled)
	})

	t.Run("hook can return error", func(t *testing.T) {
		testErr := errors.New("test error")
		hooks := []session.AppendEventHook{
			func(ctx *session.AppendEventContext, next func() error) error {
				return testErr
			},
		}

		ctx := &session.AppendEventContext{Context: context.Background()}
		final := func(c *session.AppendEventContext, next func() error) error {
			return nil
		}
		err := RunAppendEventHooks(hooks, ctx, final)

		assert.Equal(t, testErr, err)
	})

	t.Run("hook can modify event", func(t *testing.T) {
		hooks := []session.AppendEventHook{
			func(ctx *session.AppendEventContext, next func() error) error {
				ctx.Event.Author = "modified"
				return next()
			},
		}

		e := event.New("inv1", "original")
		ctx := &session.AppendEventContext{
			Context: context.Background(),
			Event:   e,
		}
		final := func(c *session.AppendEventContext, next func() error) error {
			return nil
		}
		err := RunAppendEventHooks(hooks, ctx, final)

		assert.NoError(t, err)
		assert.Equal(t, "modified", ctx.Event.Author)
	})

	t.Run("nil final function with empty hooks", func(t *testing.T) {
		ctx := &session.AppendEventContext{Context: context.Background()}
		err := RunAppendEventHooks(nil, ctx, nil)
		assert.NoError(t, err)
	})

	t.Run("nil final function with hooks", func(t *testing.T) {
		hooks := []session.AppendEventHook{
			func(ctx *session.AppendEventContext, next func() error) error {
				return next()
			},
		}

		ctx := &session.AppendEventContext{Context: context.Background()}
		err := RunAppendEventHooks(hooks, ctx, nil)
		assert.NoError(t, err)
	})

	t.Run("error propagates from final", func(t *testing.T) {
		testErr := errors.New("final error")
		hooks := []session.AppendEventHook{
			func(ctx *session.AppendEventContext, next func() error) error {
				return next()
			},
		}

		ctx := &session.AppendEventContext{Context: context.Background()}
		final := func(c *session.AppendEventContext, next func() error) error {
			return testErr
		}
		err := RunAppendEventHooks(hooks, ctx, final)
		assert.Equal(t, testErr, err)
	})
}

func TestRunGetSessionHooks(t *testing.T) {
	t.Run("empty hooks calls final", func(t *testing.T) {
		called := false
		ctx := &session.GetSessionContext{Context: context.Background()}
		final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			called = true
			return session.NewSession("app", "user", "sess"), nil
		}
		sess, err := RunGetSessionHooks(nil, ctx, final)
		assert.NoError(t, err)
		assert.True(t, called)
		assert.NotNil(t, sess)
	})

	t.Run("hooks execute in order", func(t *testing.T) {
		order := []string{}
		hooks := []session.GetSessionHook{
			func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "before1")
				sess, err := next()
				order = append(order, "after1")
				return sess, err
			},
			func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "before2")
				sess, err := next()
				order = append(order, "after2")
				return sess, err
			},
		}

		ctx := &session.GetSessionContext{Context: context.Background()}
		final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			order = append(order, "final")
			return session.NewSession("app", "user", "sess"), nil
		}
		_, err := RunGetSessionHooks(hooks, ctx, final)

		assert.NoError(t, err)
		assert.Equal(t, []string{"before1", "before2", "final", "after2", "after1"}, order)
	})

	t.Run("hook can modify session after next", func(t *testing.T) {
		hooks := []session.GetSessionHook{
			func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
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

		ctx := &session.GetSessionContext{Context: context.Background()}
		final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			return session.NewSession("app", "user", "sess"), nil
		}
		sess, err := RunGetSessionHooks(hooks, ctx, final)

		assert.NoError(t, err)
		assert.Equal(t, []byte("true"), sess.State["modified"])
	})

	t.Run("hook can return error", func(t *testing.T) {
		testErr := errors.New("get session error")
		hooks := []session.GetSessionHook{
			func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				return nil, testErr
			},
		}

		ctx := &session.GetSessionContext{Context: context.Background()}
		final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			return session.NewSession("app", "user", "sess"), nil
		}
		sess, err := RunGetSessionHooks(hooks, ctx, final)

		assert.Nil(t, sess)
		assert.Equal(t, testErr, err)
	})

	t.Run("hook can skip next and return nil session", func(t *testing.T) {
		finalCalled := false
		hooks := []session.GetSessionHook{
			func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				return nil, nil // skip next()
			},
		}

		ctx := &session.GetSessionContext{Context: context.Background()}
		final := func(c *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			finalCalled = true
			return session.NewSession("app", "user", "sess"), nil
		}
		sess, err := RunGetSessionHooks(hooks, ctx, final)

		assert.NoError(t, err)
		assert.Nil(t, sess)
		assert.False(t, finalCalled)
	})

	t.Run("nil final function with empty hooks", func(t *testing.T) {
		ctx := &session.GetSessionContext{Context: context.Background()}
		sess, err := RunGetSessionHooks(nil, ctx, nil)

		assert.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("nil final function with hooks", func(t *testing.T) {
		hooks := []session.GetSessionHook{
			func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				return next()
			},
		}

		ctx := &session.GetSessionContext{Context: context.Background()}
		sess, err := RunGetSessionHooks(hooks, ctx, nil)

		assert.NoError(t, err)
		assert.Nil(t, sess)
	})
}
