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
	"encoding/base64"
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
	opts          Options
	client        *gatewayClient
	offloadClient *gatewayClient

	queue  chan ingestJob
	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
	once   sync.Once

	cursorMu    sync.Mutex
	inFlight    map[string]time.Time
	lastCapture map[string]time.Time
	serialTail  map[string]*captureSerialState

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
	offloadClient := client
	if options.ContextOffload.Enabled &&
		(options.ContextOffload.GatewayURL != "" || options.ContextOffload.APIKey != "") {
		offloadOptions := options
		if options.ContextOffload.GatewayURL != "" {
			offloadOptions.GatewayURL = options.ContextOffload.GatewayURL
		}
		if options.ContextOffload.APIKey != "" {
			offloadOptions.APIKey = options.ContextOffload.APIKey
		}
		offloadClient, err = newGatewayClient(offloadOptions)
		if err != nil {
			return nil, err
		}
	}
	s := &Service{
		opts:          options,
		client:        client,
		offloadClient: offloadClient,
		queue:         make(chan ingestJob, options.IngestQueueSize),
		inFlight:      make(map[string]time.Time),
		lastCapture:   make(map[string]time.Time),
		serialTail:    make(map[string]*captureSerialState),
	}
	s.tools = s.buildTools()
	s.startWorkers()
	return s, nil
}

func (s *Service) contextOffloadClient() *gatewayClient {
	if s == nil {
		return nil
	}
	if s.offloadClient != nil {
		return s.offloadClient
	}
	return s.client
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
		s.finishCaptureJob(job, ctx.Err())
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
	if ctx == nil {
		ctx = context.Background()
	}
	sessionKey := s.sessionKey(sess)
	barrier := s.reserveSerialBarrier(sessionKey)
	if err := s.waitForPreviousCapture(ctx, barrier); err != nil {
		s.finishSerialBarrier(sessionKey, barrier, err)
		return err
	}
	_, err := s.client.endSession(ctx, endSessionRequest{
		SessionKey: sessionKey,
		UserID:     sess.UserID,
	})
	if err == nil {
		clearBestEffortSyntheticTimestamp(sess)
		// Intentionally retain the service-level capture checkpoint until the
		// service shuts down. It is the only reliable cursor for reloaded or
		// cloned sessions, so dropping it here would let a framework session
		// that keeps running after EndSession re-scan and resend transcript
		// that was already captured. Keeping it is safe because new events
		// always carry timestamps after the cursor.
	}
	s.finishSerialBarrier(sessionKey, barrier, nil)
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
	for i, part := range parts {
		parts[i] = base64.RawURLEncoding.EncodeToString([]byte(part))
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
	if err := s.waitForPreviousCapture(ctx, job.serial); err != nil {
		s.finishCaptureJob(job, err)
		return err
	}
	_, err := s.client.capture(ctx, job.req)
	if err != nil {
		err = fmt.Errorf("tencentdb memory: capture failed: %w", err)
		s.finishCaptureJob(job, err)
		return err
	}
	writeBestEffortLastCaptureAt(job.sess, job.cursor)
	s.finishCaptureJob(job, nil)
	return nil
}

func (s *Service) reserveIngestJob(sess *session.Session, sessionKey string) (ingestJob, bool) {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()

	since := readBestEffortLastCaptureAt(sess)
	if last, ok := s.lastCapture[sessionKey]; ok && last.After(since) {
		since = last
	}
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
	messages, latestSynthetic := normalizeGatewayMessageTimestampsAfter(
		scan.Messages,
		time.Now(),
		readBestEffortSyntheticTimestamp(sess),
	)
	writeBestEffortSyntheticTimestamp(sess, latestSynthetic)
	previous := s.serialTail[sessionKey]
	serial := &captureSerialState{
		sessionKey: sessionKey,
		previous:   previous,
		done:       make(chan struct{}),
	}
	s.serialTail[sessionKey] = serial
	return ingestJob{
		req: captureRequest{
			UserContent:      userContent,
			AssistantContent: assistantContent,
			SessionKey:       sessionKey,
			SessionID:        sess.ID,
			UserID:           sess.UserID,
			Messages:         messages,
		},
		sess:   sess,
		cursor: scan.Latest,
		serial: serial,
	}, true
}

func (s *Service) finishCaptureJob(job ingestJob, err error) {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	if err == nil && !job.cursor.IsZero() {
		if last, ok := s.lastCapture[job.req.SessionKey]; !ok || job.cursor.After(last) {
			s.lastCapture[job.req.SessionKey] = job.cursor
		}
	}
	if current, ok := s.inFlight[job.req.SessionKey]; ok && !current.After(job.cursor) {
		delete(s.inFlight, job.req.SessionKey)
	}
	if job.serial != nil {
		job.serial.err = err
		if s.serialTail[job.req.SessionKey] == job.serial {
			delete(s.serialTail, job.req.SessionKey)
		}
		close(job.serial.done)
	}
}

func (s *Service) reserveSerialBarrier(sessionKey string) *captureSerialState {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	barrier := &captureSerialState{
		sessionKey: sessionKey,
		previous:   s.serialTail[sessionKey],
		done:       make(chan struct{}),
	}
	s.serialTail[sessionKey] = barrier
	return barrier
}

func (s *Service) waitForPreviousCapture(ctx context.Context, serial *captureSerialState) error {
	if serial == nil || serial.previous == nil {
		return nil
	}
	select {
	case <-serial.previous.done:
		if serial.previous.err != nil {
			return fmt.Errorf(
				"tencentdb memory: previous capture failed for session_key %q: %w",
				serial.sessionKey,
				serial.previous.err,
			)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) finishSerialBarrier(sessionKey string, barrier *captureSerialState, err error) {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	barrier.err = err
	if s.serialTail[sessionKey] == barrier {
		delete(s.serialTail, sessionKey)
	}
	close(barrier.done)
}
