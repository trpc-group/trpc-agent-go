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
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

func TestNew(t *testing.T) {
	r := New(nil)
	assert.NotNil(t, r)
	runner, ok := r.(*runner)
	assert.True(t, ok)

	trans := runner.translatorFactory(&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NotNil(t, trans)
	assert.IsType(t, translator.New("", ""), trans)

	userID, err := runner.userIDResolver(context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)
	assert.Equal(t, "user", userID)
}

func TestRunValidatesInput(t *testing.T) {
	r := &runner{}
	ch, err := r.Run(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)

	r.runner = &fakeRunner{}
	ch, err = r.Run(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestRunNoMessages(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) translator.Translator { return fakeTrans },
		userIDResolver:    NewOptions().UserIDResolver,
	}

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunUserIDResolverError(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) translator.Translator { return fakeTrans },
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "", errors.New("boom")
		},
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunLastMessageNotUser(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) translator.Translator { return fakeTrans },
		userIDResolver:    NewOptions().UserIDResolver,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleAssistant, Content: "bot"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunUnderlyingRunnerError(t *testing.T) {
	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context, userID, sessionID string, message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		return nil, errors.New("fail")
	}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) translator.Translator { return fakeTrans },
		userIDResolver:    NewOptions().UserIDResolver,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 1, underlying.calls)
}

func TestRunTranslateError(t *testing.T) {
	fakeTrans := &fakeTranslator{err: errors.New("bad event")}
	eventsCh := make(chan *agentevent.Event, 1)
	eventsCh <- &agentevent.Event{}
	close(eventsCh)

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		return eventsCh, nil
	}

	r := &runner{
		runner: underlying,
		translatorFactory: func(*adapter.RunAgentInput) translator.Translator {
			return fakeTrans
		},
		userIDResolver: NewOptions().UserIDResolver,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	aguiCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, aguiCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
}

func TestRunNormal(t *testing.T) {
	fakeTrans := &fakeTranslator{events: [][]aguievents.Event{
		{aguievents.NewTextMessageStartEvent("msg-1")},
		{aguievents.NewTextMessageEndEvent("msg-1"), aguievents.NewRunFinishedEvent("thread", "run")},
	}}

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		assert.Equal(t, "user-123", userID)
		assert.Equal(t, "thread", sessionID)
		ch := make(chan *agentevent.Event, 2)
		ch <- &agentevent.Event{}
		ch <- &agentevent.Event{}
		close(ch)
		return ch, nil
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: func(*adapter.RunAgentInput) translator.Translator { return fakeTrans },
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "user-123", nil
		},
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}

	aguiCh, err := r.Run(context.Background(), input)
	if !assert.NoError(t, err) {
		return
	}
	evts := collectEvents(t, aguiCh)
	assert.Len(t, evts, 4)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), evts[1])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), evts[2])
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), evts[3])
	assert.Equal(t, 1, underlying.calls)
}

func TestRunnerHandleBeforeWithCallback(t *testing.T) {
	t.Run("without callback", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		r := &runner{}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("with callback", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		replacement := agentevent.New("inv-replacement", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return replacement, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, replacement, got)
	})
	t.Run("return err", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, errors.New("fail")
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("both nil", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("multiple callbacks", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event1 := agentevent.New("inv-1", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event1, nil
				}).
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event1, got)
	})
	t.Run("multiple callbacks return nil", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, nil
				}).
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event2, got)
	})
	t.Run("multiple callbacks return err", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, errors.New("fail")
				}).
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestRunnerHandleAfterWithCallback(t *testing.T) {
	t.Run("without callback", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("with callback", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		replacement := aguievents.NewRunErrorEvent("callback override")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return replacement, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, replacement, got)
	})
	t.Run("return err", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, errors.New("fail")
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("both nil", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("multiple callbacks", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event1 := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event1, nil
				}).
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event1, got)
	})
	t.Run("multiple callbacks return nil", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, nil
				}).
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event2, got)
	})
	t.Run("multiple callbacks return err", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, errors.New("fail")
				}).
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestRunnerBeforeTranslateCallbackOverridesInput(t *testing.T) {
	original := agentevent.NewResponseEvent("inv", "assistant",
		&model.Response{
			ID:      "id",
			Object:  model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "original"}}}})
	replacement := agentevent.NewResponseEvent("inv", "assistant",
		&model.Response{
			ID:      "id",
			Object:  model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "replacement"}}}})

	callbacks := translator.NewCallbacks().
		RegisterBeforeTranslate(func(ctx context.Context, evt *agentevent.Event) (*agentevent.Event, error) {
			return replacement, nil
		})

	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event, 1)
			ch <- original
			close(ch)
			return ch, nil
		}}

	r := New(underlying, WithTranslateCallbacks(callbacks))

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	ch, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	out := collectEvents(t, ch)

	assert.Len(t, out, 4)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), out[0])
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), out[1])
	assert.IsType(t, (*aguievents.TextMessageContentEvent)(nil), out[2])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), out[3])

	contentEvent, ok := out[2].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "replacement", contentEvent.Delta)
}

func TestRunnerAfterTranslateCallbackOverridesEmission(t *testing.T) {
	replacement := aguievents.NewRunErrorEvent("override")
	fakeTrans := &fakeTranslator{events: [][]aguievents.Event{{aguievents.NewRunFinishedEvent("thread", "run")}}}
	callbacks := translator.NewCallbacks().
		RegisterAfterTranslate(func(ctx context.Context, evt aguievents.Event) (aguievents.Event, error) {
			return replacement, nil
		})

	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event, 1)
			ch <- agentevent.New("inv", "assistant")
			close(ch)
			return ch, nil
		}}

	r := &runner{
		runner:             underlying,
		translatorFactory:  func(*adapter.RunAgentInput) translator.Translator { return fakeTrans },
		userIDResolver:     NewOptions().UserIDResolver,
		translateCallbacks: callbacks,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	ch, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	out := collectEvents(t, ch)
	assert.Len(t, out, 2)
	assert.IsType(t, (*aguievents.RunErrorEvent)(nil), out[1])
}

type fakeTranslator struct {
	events [][]aguievents.Event
	err    error
}

func (f *fakeTranslator) Translate(evt *agentevent.Event) ([]aguievents.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.events) == 0 {
		return nil, nil
	}
	out := f.events[0]
	f.events = f.events[1:]
	return out, nil
}

type fakeRunner struct {
	run func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		opts ...agent.RunOption) (<-chan *agentevent.Event, error)
	calls int
}

func (f *fakeRunner) Run(ctx context.Context,
	userID, sessionID string,
	message model.Message,
	opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
	f.calls++
	if f.run != nil {
		return f.run(ctx, userID, sessionID, message, opts...)
	}
	return nil, nil
}

func collectEvents(t *testing.T, ch <-chan aguievents.Event) []aguievents.Event {
	t.Helper()
	var out []aguievents.Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-time.After(time.Second):
			t.Fatalf("timeout collecting events")
		}
	}
}
