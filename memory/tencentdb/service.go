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
		opts:   options,
		client: client,
		queue:  make(chan ingestJob, options.IngestQueueSize),
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

	scan := scanTranscript(sess, readBestEffortLastCaptureAt(sess))
	if len(scan.Messages) == 0 {
		return nil
	}
	userContent, assistantContent := lastUserAssistantPair(scan.Messages)
	if strings.TrimSpace(userContent) == "" || strings.TrimSpace(assistantContent) == "" {
		return nil
	}

	req := captureRequest{
		UserContent:      userContent,
		AssistantContent: assistantContent,
		SessionKey:       s.sessionKey(sess),
		SessionID:        sess.ID,
		UserID:           sess.UserID,
		Messages:         normalizeGatewayMessageTimestamps(scan.Messages, time.Now()),
	}
	job := ingestJob{
		req:    req,
		sess:   sess,
		cursor: scan.Latest,
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return errors.New("tencentdb memory: service is closed")
	}
	select {
	case s.queue <- job:
		s.mu.RUnlock()
		writeBestEffortLastCaptureAt(sess, scan.Latest)
		return nil
	default:
		s.mu.RUnlock()
		if err := s.capture(ctx, job); err != nil {
			return err
		}
		return nil
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
		return fmt.Errorf("tencentdb memory: capture failed: %w", err)
	}
	writeBestEffortLastCaptureAt(job.sess, job.cursor)
	return nil
}
