//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	"context"
	"errors"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
)

func TestBeforeTranslateCallback(t *testing.T) {
	t.Run("with callback", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		replacement := agentevent.New("inv-replacement", "assistant")
		cb := NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return replacement, nil
			})
		got, err := cb.RunBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, replacement, got)
	})
	t.Run("return err", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		cb := NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return nil, errors.New("fail")
			})
		got, err := cb.RunBeforeTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("both nil", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		cb := NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return nil, nil
			})
		got, err := cb.RunBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("multiple callbacks", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event1 := agentevent.New("inv-1", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		cb := NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return event1, nil
			}).
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return event2, nil
			})
		got, err := cb.RunBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event1, got)
	})
	t.Run("multiple callbacks return nil", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		cb := NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return nil, nil
			}).
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return event2, nil
			})
		got, err := cb.RunBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event2, got)
	})
	t.Run("multiple callbacks return err", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		cb := NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return nil, errors.New("fail")
			}).
			RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
				return event2, nil
			})
		got, err := cb.RunBeforeTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestAfterTranslateCallback(t *testing.T) {
	t.Run("with callback", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		replacement := aguievents.NewRunErrorEvent("callback override")
		cb := NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return replacement, nil
			})
		got, err := cb.RunAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, replacement, got)
	})
	t.Run("return err", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		cb := NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return nil, errors.New("fail")
			})
		got, err := cb.RunAfterTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("both nil", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		cb := NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return nil, nil
			})
		got, err := cb.RunAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Nil(t, got)
	})
	t.Run("multiple callbacks", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event1 := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		cb := NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return event1, nil
			}).
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return event2, nil
			})
		got, err := cb.RunAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event1, got)
	})
	t.Run("multiple callbacks return nil", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		cb := NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return nil, nil
			}).
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return event2, nil
			})
		got, err := cb.RunAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event2, got)
	})
	t.Run("multiple callbacks return err", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		cb := NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return nil, errors.New("fail")
			}).
			RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
				return event2, nil
			})
		got, err := cb.RunAfterTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}
