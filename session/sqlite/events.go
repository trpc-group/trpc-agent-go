//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AppendEvent appends an event to a session.
func (s *Service) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	hctx := &session.AppendEventContext{
		Context: ctx,
		Session: sess,
		Event:   e,
		Key:     key,
	}
	final := func(c *session.AppendEventContext, next func() error) error {
		return s.appendEventInternal(
			c.Context,
			c.Session,
			c.Event,
			c.Key,
			opts...,
		)
	}
	return hook.RunAppendEventHooks(s.opts.appendEventHooks, hctx, final)
}

func (s *Service) appendEventInternal(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	key session.Key,
	opts ...session.Option,
) error {
	sess.UpdateUserSession(e, opts...)

	if s.opts.enableAsyncPersist {
		return s.enqueueEventPersist(ctx, sess, key, e)
	}

	if err := s.addEvent(ctx, key, e); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (s *Service) enqueueEventPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *event.Event,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok &&
				e.Error() == "send on closed channel" {
				log.ErrorfContext(
					ctx,
					"async persist event: %v",
					r,
				)
				err = nil
				return
			}
			panic(r)
		}
	}()

	index := sess.Hash % len(s.eventPairChans)
	select {
	case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AppendTrackEvent appends a track event to a session.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return session.ErrNilSession
	}
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}

	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}

	if s.opts.enableAsyncPersist {
		return s.enqueueTrackPersist(ctx, sess, key, trackEvent)
	}

	if err := s.addTrackEvent(ctx, key, trackEvent); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	return nil
}

func (s *Service) enqueueTrackPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *session.TrackEvent,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok &&
				e.Error() == "send on closed channel" {
				log.ErrorfContext(
					ctx,
					"async persist track event: %v",
					r,
				)
				err = nil
				return
			}
			panic(r)
		}
	}()

	index := sess.Hash % len(s.trackEventChans)
	select {
	case s.trackEventChans[index] <- &trackEventPair{key: key, event: e}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) startAsyncPersistWorker() {
	persisterNum := s.opts.asyncPersisterNum
	s.eventPairChans = make([]chan *sessionEventPair, persisterNum)
	s.trackEventChans = make([]chan *trackEventPair, persisterNum)

	for i := 0; i < persisterNum; i++ {
		s.eventPairChans[i] = make(
			chan *sessionEventPair,
			defaultChanBufferSize,
		)
		s.trackEventChans[i] = make(
			chan *trackEventPair,
			defaultChanBufferSize,
		)
	}

	s.persistWg.Add(persisterNum * 2)

	for _, ch := range s.eventPairChans {
		go func(ch chan *sessionEventPair) {
			defer s.persistWg.Done()
			for pair := range ch {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addEvent(ctx, pair.key, pair.event); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist event: %v",
						err,
					)
				}
				cancel()
			}
		}(ch)
	}

	for _, ch := range s.trackEventChans {
		go func(ch chan *trackEventPair) {
			defer s.persistWg.Done()
			for pair := range ch {
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addTrackEvent(
					ctx,
					pair.key,
					pair.event,
				); err != nil {
					log.ErrorfContext(
						ctx,
						"async persist track event: %v",
						err,
					)
				}
				cancel()
			}
		}(ch)
	}
}
