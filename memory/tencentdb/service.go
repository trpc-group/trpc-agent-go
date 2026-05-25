//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var _ session.Ingestor = (*Service)(nil)

// Service is a TencentDB Agent Memory gateway adapter.
//
// The current TencentDB SDK gateway is best treated as a trusted sidecar. The
// adapter forwards app/user/session identifiers, but hard multi-tenant isolation
// depends on the gateway and SDK honoring those fields end-to-end.
type Service struct {
	opts   Options
	client *gatewayClient

	queue  chan ingestJob
	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
	once   sync.Once

	cursorMu sync.Mutex
	inFlight map[string]time.Time

	tools []tool.Tool
}

// NewService creates a TencentDB Agent Memory service.
func NewService(opts ...Option) (*Service, error) {
	options := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	client, err := newGatewayClient(options)
	if err != nil {
		return nil, err
	}
	s := &Service{
		opts:     options,
		client:   client,
		queue:    make(chan ingestJob, options.IngestQueueSize),
		inFlight: make(map[string]time.Time),
	}
	s.tools = s.buildTools()
	s.startWorkers()
	return s, nil
}

// IngestSession captures the latest user/assistant exchange and transcript
// messages into the TencentDB Agent Memory gateway.
func (s *Service) IngestSession(
	ctx context.Context,
	sess *session.Session,
	opts ...session.IngestOption,
) error {
	if s == nil {
		return errors.New("tencentdb memory: nil service")
	}
	if sess == nil {
		return errors.New("tencentdb memory: session is required")
	}
	if err := validateSessionScope(sess); err != nil {
		return err
	}

	if ctx == nil {
		ctx = context.Background()
	}
	sessionKey := s.sessionKey(sess)
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("tencentdb memory: service is closed")
	}
	job, ok := s.reserveIngestJob(sess, sessionKey)
	if !ok {
		s.mu.RUnlock()
		return nil
	}
	select {
	case s.queue <- job:
		s.mu.RUnlock()
		return nil
	case <-ctx.Done():
		s.clearInFlight(sessionKey, job.cursor)
		s.mu.RUnlock()
		return ctx.Err()
	}
}

// EndSession flushes short-term session state managed by the sidecar, if
// supported by the gateway.
func (s *Service) EndSession(ctx context.Context, sess *session.Session) error {
	if s == nil {
		return errors.New("tencentdb memory: nil service")
	}
	if sess == nil {
		return errors.New("tencentdb memory: session is required")
	}
	if err := validateSessionScope(sess); err != nil {
		return err
	}
	_, err := s.client.endSession(ctx, endSessionRequest{
		SessionKey: s.sessionKey(sess),
		UserID:     sess.UserID,
	})
	return err
}

// Health checks gateway readiness.
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
	if s == nil {
		return nil, errors.New("tencentdb memory: nil service")
	}
	return s.client.health(ctx)
}

// Tools returns TencentDB-native memory tools.
func (s *Service) Tools() []tool.Tool {
	if s == nil || len(s.tools) == 0 {
		return nil
	}
	out := make([]tool.Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// Close stops capture workers after draining queued work.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.queue)
		s.mu.Unlock()
	})
	s.wg.Wait()
	return nil
}

func (s *Service) sessionKey(sess *session.Session) string {
	if s.opts.SessionKeyFunc != nil {
		return s.opts.SessionKeyFunc(sess)
	}
	return defaultSessionKey(sess)
}

func defaultSessionKey(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(sess.AppName),
		strings.TrimSpace(sess.UserID),
		strings.TrimSpace(sess.ID),
	}
	return strings.Join(parts, ":")
}

func validateSessionScope(sess *session.Session) error {
	if strings.TrimSpace(sess.AppName) == "" {
		return errors.New("tencentdb memory: session app name is required")
	}
	if strings.TrimSpace(sess.UserID) == "" {
		return errors.New("tencentdb memory: session user id is required")
	}
	if strings.TrimSpace(sess.ID) == "" {
		return errors.New("tencentdb memory: session id is required")
	}
	return nil
}

func (s *Service) capture(ctx context.Context, job ingestJob) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.client.capture(ctx, job.req)
	if err != nil {
		s.clearInFlight(job.req.SessionKey, job.cursor)
		return fmt.Errorf("tencentdb memory: capture failed: %w", err)
	}
	writeBestEffortLastCaptureAt(job.sess, job.cursor)
	s.clearInFlight(job.req.SessionKey, job.cursor)
	return nil
}

func (s *Service) reserveIngestJob(sess *session.Session, sessionKey string) (ingestJob, bool) {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()

	since := readBestEffortLastCaptureAt(sess)
	if inFlight, ok := s.inFlight[sessionKey]; ok && inFlight.After(since) {
		since = inFlight
	}
	scan := scanTranscript(sess, since)
	if len(scan.Messages) == 0 {
		return ingestJob{}, false
	}
	userContent, assistantContent := lastUserAssistantPair(scan.Messages)
	if strings.TrimSpace(userContent) == "" || strings.TrimSpace(assistantContent) == "" {
		return ingestJob{}, false
	}
	if current, ok := s.inFlight[sessionKey]; !ok || scan.Latest.After(current) {
		s.inFlight[sessionKey] = scan.Latest
	}
	return ingestJob{
		req: captureRequest{
			UserContent:      userContent,
			AssistantContent: assistantContent,
			SessionKey:       sessionKey,
			SessionID:        sess.ID,
			UserID:           sess.UserID,
			Messages:         normalizeGatewayMessageTimestamps(scan.Messages, time.Now()),
		},
		sess:   sess,
		cursor: scan.Latest,
	}, true
}

func (s *Service) clearInFlight(sessionKey string, cursor time.Time) {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	if current, ok := s.inFlight[sessionKey]; ok && !current.After(cursor) {
		delete(s.inFlight, sessionKey)
	}
}
