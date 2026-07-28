//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Fault is a corruption injected into one backend.
//
// A consistency harness that is never shown a real inconsistency proves
// nothing: normalization can quietly grow until every case passes against
// every backend. Faults close that gap by corrupting a backend on purpose and
// requiring the comparator to notice, which turns "these cases would catch a
// regression" from a claim into a test.
type Fault struct {
	// Name identifies the fault in test output.
	Name string
	// Description states the real-world failure the fault stands in for.
	Description string
	// Wrap decorates the session service with the corrupting behavior.
	Wrap func(session.Service) session.Service
}

// faultyService decorates a session service. Methods it does not override are
// promoted from the embedded interface, so a fault only has to describe the
// behavior it changes.
type faultyService struct {
	session.Service

	appendEvent    func(inner session.Service, ctx context.Context, sess *session.Session, e *event.Event) error
	getSession     func(inner session.Service, ctx context.Context, key session.Key, sess *session.Session) (*session.Session, error)
	createSummary  func(inner session.Service, ctx context.Context, sess *session.Session, filterKey string, force bool) error
	appendTrackEvt func(inner session.Service, ctx context.Context, sess *session.Session, e *session.TrackEvent) error
}

func (f *faultyService) AppendEvent(
	ctx context.Context, sess *session.Session, e *event.Event, opts ...session.Option,
) error {
	if f.appendEvent != nil {
		return f.appendEvent(f.Service, ctx, sess, e)
	}
	return f.Service.AppendEvent(ctx, sess, e, opts...)
}

func (f *faultyService) GetSession(
	ctx context.Context, key session.Key, opts ...session.Option,
) (*session.Session, error) {
	sess, err := f.Service.GetSession(ctx, key, opts...)
	if err != nil || f.getSession == nil {
		return sess, err
	}
	return f.getSession(f.Service, ctx, key, sess)
}

func (f *faultyService) CreateSessionSummary(
	ctx context.Context, sess *session.Session, filterKey string, force bool,
) error {
	if f.createSummary != nil {
		return f.createSummary(f.Service, ctx, sess, filterKey, force)
	}
	return f.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

// AppendTrackEvent keeps the decorated service satisfying session.TrackService
// whenever the wrapped service does. Without it, wrapping a backend would
// silently turn track support off and a track fault would look like a missing
// capability instead of a lost write.
func (f *faultyService) AppendTrackEvent(
	ctx context.Context, sess *session.Session, e *session.TrackEvent, opts ...session.Option,
) error {
	if f.appendTrackEvt != nil {
		return f.appendTrackEvt(f.Service, ctx, sess, e)
	}
	tracker, ok := f.Service.(session.TrackService)
	if !ok {
		return errors.New("wrapped session service does not implement session.TrackService")
	}
	return tracker.AppendTrackEvent(ctx, sess, e, opts...)
}

// DropNthEvent loses the nth appended event while reporting success, standing
// in for a backend whose write silently fails.
func DropNthEvent(n int) Fault {
	return Fault{
		Name:        "drop-event",
		Description: "the nth appended event is silently discarded",
		Wrap: func(inner session.Service) session.Service {
			var count int64
			return &faultyService{
				Service: inner,
				appendEvent: func(
					s session.Service, ctx context.Context, sess *session.Session, e *event.Event,
				) error {
					if int(atomic.AddInt64(&count, 1)) == n {
						return nil
					}
					return s.AppendEvent(ctx, sess, e)
				},
			}
		},
	}
}

// DuplicateNthEvent writes the nth event twice, standing in for a retry that
// is not idempotent.
func DuplicateNthEvent(n int) Fault {
	return Fault{
		Name:        "duplicate-event",
		Description: "the nth appended event is written twice",
		Wrap: func(inner session.Service) session.Service {
			var count int64
			return &faultyService{
				Service: inner,
				appendEvent: func(
					s session.Service, ctx context.Context, sess *session.Session, e *event.Event,
				) error {
					if err := s.AppendEvent(ctx, sess, e); err != nil {
						return err
					}
					if int(atomic.AddInt64(&count, 1)) == n {
						return s.AppendEvent(ctx, sess, e)
					}
					return nil
				},
			}
		},
	}
}

// SwapEventsOnRead returns two adjacent events in the wrong order, standing in
// for a backend whose ordering key is not stable.
func SwapEventsOnRead(i, j int) Fault {
	return Fault{
		Name:        "reorder-events",
		Description: "two events are returned in the wrong order",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				getSession: func(
					s session.Service, ctx context.Context, key session.Key, sess *session.Session,
				) (*session.Session, error) {
					if sess == nil || i >= len(sess.Events) || j >= len(sess.Events) {
						return sess, nil
					}
					sess.Events[i], sess.Events[j] = sess.Events[j], sess.Events[i]
					return sess, nil
				},
			}
		},
	}
}

// DropSummary discards every summary write, standing in for summary loss.
func DropSummary() Fault {
	return Fault{
		Name:        "summary-lost",
		Description: "summary generation reports success but stores nothing",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				createSummary: func(
					s session.Service, ctx context.Context, sess *session.Session, filterKey string, force bool,
				) error {
					return nil
				},
			}
		},
	}
}

// StaleSummaryOnRegenerate lets the first summary through and silently drops
// every later regeneration for the same filter key.
//
// This is the overwrite failure: the summary is present, so nothing looks
// missing, but it describes a conversation that has since moved on. It is the
// hardest of the summary faults to notice by eye, which is why it is injected
// rather than trusted.
func StaleSummaryOnRegenerate() Fault {
	return Fault{
		Name:        "summary-not-overwritten",
		Description: "a regenerated summary is dropped, leaving the earlier text in place",
		Wrap: func(inner session.Service) session.Service {
			var mu sync.Mutex
			seen := make(map[string]bool)
			return &faultyService{
				Service: inner,
				createSummary: func(
					s session.Service, ctx context.Context, sess *session.Session, filterKey string, force bool,
				) error {
					mu.Lock()
					first := !seen[filterKey]
					seen[filterKey] = true
					mu.Unlock()
					if !first {
						return nil
					}
					return s.CreateSessionSummary(ctx, sess, filterKey, force)
				},
			}
		},
	}
}

// MisfileSummaryFilterKey stores a summary under a filter key other than the
// requested one, standing in for the branch-attribution bug that leaves one
// branch summarized with another branch's content.
func MisfileSummaryFilterKey(wrong string) Fault {
	return Fault{
		Name:        "summary-wrong-filter-key",
		Description: "a summary is filed under the wrong filter key",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				createSummary: func(
					s session.Service, ctx context.Context, sess *session.Session, filterKey string, force bool,
				) error {
					return s.CreateSessionSummary(ctx, sess, wrong, force)
				},
			}
		},
	}
}

// MisattributeSummary writes the summary onto a different session, standing in
// for the ownership bug where one conversation's summary surfaces in another.
func MisattributeSummary(target SessionRef) Fault {
	return Fault{
		Name:        "summary-wrong-session",
		Description: "a summary is stored against the wrong session",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				createSummary: func(
					s session.Service, ctx context.Context, sess *session.Session, filterKey string, force bool,
				) error {
					other, err := s.GetSession(ctx, target.Key())
					if err != nil {
						return err
					}
					if other == nil {
						return nil
					}
					other.Events = sess.Events
					return s.CreateSessionSummary(ctx, other, filterKey, force)
				},
			}
		},
	}
}

// HideStateKeyOnRead omits one state key from every read, standing in for
// state that is written but never returned.
func HideStateKeyOnRead(key string) Fault {
	return Fault{
		Name:        "state-key-lost",
		Description: "one state key is missing from reads",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				getSession: func(
					s session.Service, ctx context.Context, k session.Key, sess *session.Session,
				) (*session.Session, error) {
					if sess != nil {
						delete(sess.State, key)
					}
					return sess, nil
				},
			}
		},
	}
}

// InjectStateKeyOnRead adds a state key no operation ever wrote, standing in
// for state leaking across sessions.
func InjectStateKeyOnRead(key, value string) Fault {
	return Fault{
		Name:        "state-key-leaked",
		Description: "a state key appears that was never written",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				getSession: func(
					s session.Service, ctx context.Context, k session.Key, sess *session.Session,
				) (*session.Session, error) {
					if sess != nil {
						if sess.State == nil {
							sess.State = session.StateMap{}
						}
						sess.State[key] = []byte(value)
					}
					return sess, nil
				},
			}
		},
	}
}

// DropTrackEvents discards every track write, standing in for observability
// data that is accepted and then lost.
func DropTrackEvents() Fault {
	return Fault{
		Name:        "track-events-lost",
		Description: "track writes report success but store nothing",
		Wrap: func(inner session.Service) session.Service {
			return &faultyService{
				Service: inner,
				appendTrackEvt: func(
					s session.Service, ctx context.Context, sess *session.Session, e *session.TrackEvent,
				) error {
					return nil
				},
			}
		},
	}
}
