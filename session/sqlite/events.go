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
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
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
	write := sessionrevision.NewWrite(ctx, sess)
	if s.opts.enableAsyncPersist && write.Start != nil {
		if err := s.flushRevisionPersistence(ctx, key); err != nil {
			return fmt.Errorf("flush persistence before runner turn: %w", err)
		}
	} else if s.opts.enableAsyncPersist && e != nil && e.IsRunnerCompletion() {
		if err := s.flushTrackPersistence(ctx, key); err != nil {
			return fmt.Errorf("flush track persistence before runner completion: %w", err)
		}
	}
	sess.UpdateUserSession(e, opts...)

	if s.opts.enableAsyncPersist {
		if write.Start != nil {
			if err := s.addEventWithRevision(ctx, key, e, write); err != nil {
				return fmt.Errorf("persist runner turn boundary: %w", err)
			}
			return nil
		}
		return s.enqueueEventPersistWithRevision(ctx, sess, key, e, write)
	}

	if err := s.addEventWithRevision(ctx, key, e, write); err != nil {
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
	return s.enqueueEventPersistWithRevision(ctx, sess, key, e, sessionrevision.Write{})
}

func (s *Service) enqueueEventPersistWithRevision(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *event.Event,
	write sessionrevision.Write,
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
	case s.eventPairChans[index] <- &sessionEventPair{key: key, event: e, write: write}:
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
	write := sessionrevision.NewWrite(ctx, sess)

	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}

	if s.opts.enableAsyncPersist {
		return s.enqueueTrackPersistWithRevision(ctx, sess, key, trackEvent, write)
	}

	if err := s.addTrackEventWithRevision(ctx, key, trackEvent, write); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	return nil
}

// GetTrackEvents returns persisted track events for the given session track.
func (s *Service) GetTrackEvents(
	ctx context.Context,
	key session.Key,
	track session.Track,
	opts ...session.Option,
) (*session.TrackEvents, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, err
	}
	opt := applyOptions(opts...)
	trackEvents, err := s.getTrackEventsByTrackLists(
		ctx,
		[]session.Key{key},
		[][]session.Track{{track}},
		opt.EventNum,
		opt.EventTime,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite session service get track events failed: %w", err)
	}
	return &session.TrackEvents{Track: track, Events: trackEvents[0][track]}, nil
}

func (s *Service) enqueueTrackPersist(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *session.TrackEvent,
) (err error) {
	return s.enqueueTrackPersistWithRevision(ctx, sess, key, e, sessionrevision.Write{})
}

func (s *Service) enqueueTrackPersistWithRevision(
	ctx context.Context,
	sess *session.Session,
	key session.Key,
	e *session.TrackEvent,
	write sessionrevision.Write,
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
	case s.trackEventChans[index] <- &trackEventPair{key: key, event: e, write: write}:
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
			var pendingErrors sessionrevision.PendingErrors
			for pair := range ch {
				if pair.done != nil {
					pair.done <- pendingErrors.Take(pair.key)
					continue
				}
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addEventWithRevision(ctx, pair.key, pair.event, pair.write); err != nil {
					pendingErrors.Add(pair.key, err)
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
			var pendingErrors sessionrevision.PendingErrors
			for pair := range ch {
				if pair.done != nil {
					pair.done <- pendingErrors.Take(pair.key)
					continue
				}
				ctx := context.Background()
				ctx, cancel := context.WithTimeout(
					ctx,
					defaultAsyncPersistTimeout,
				)
				if err := s.addTrackEventWithRevision(
					ctx,
					pair.key,
					pair.event,
					pair.write,
				); err != nil {
					pendingErrors.Add(pair.key, err)
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
