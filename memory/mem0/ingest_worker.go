//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type ingestJob struct {
	Ctx      context.Context
	UserKey  memory.UserKey
	Session  *session.Session
	LatestTs time.Time
	Messages []model.Message
	Options  session.IngestOptions
}

type ingestWorker struct {
	c *client

	asyncMode bool
	version   string

	jobChans []chan *ingestJob
	timeout  time.Duration

	orgID     string
	projectID string

	mu      sync.RWMutex
	wg      sync.WaitGroup
	started bool
}

const (
	ingestEventStatusPending   = "PENDING"
	ingestEventStatusRunning   = "RUNNING"
	ingestEventStatusSucceeded = "SUCCEEDED"
	ingestEventStatusFailed    = "FAILED"
	ingestEventPollInterval    = 2 * time.Second
)

func newIngestWorker(c *client, opts serviceOpts) *ingestWorker {
	num := opts.asyncMemoryNum
	if num <= 0 {
		num = defaultAsyncMemoryNum
	}
	queueSize := opts.memoryQueueSize
	if queueSize <= 0 {
		queueSize = defaultMemoryQueueSize
	}
	w := &ingestWorker{
		c:         c,
		asyncMode: opts.asyncMode,
		version:   opts.version,
		timeout:   opts.memoryJobTimeout,
		orgID:     opts.orgID,
		projectID: opts.projectID,
		jobChans:  make([]chan *ingestJob, num),
	}
	for i := 0; i < num; i++ {
		w.jobChans[i] = make(chan *ingestJob, queueSize)
	}
	w.start()
	return w
}

func (w *ingestWorker) start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	w.wg.Add(len(w.jobChans))
	for _, ch := range w.jobChans {
		go func(ch chan *ingestJob) {
			defer w.wg.Done()
			for job := range ch {
				w.process(job)
			}
		}(ch)
	}
	w.started = true
}

func (w *ingestWorker) Stop() {
	w.mu.Lock()
	if !w.started || len(w.jobChans) == 0 {
		w.mu.Unlock()
		return
	}
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.started = false
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *ingestWorker) tryEnqueue(ctx context.Context, job *ingestJob) bool {
	if job == nil {
		return true
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false
		}
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}
	idx := 0
	if job.Session != nil {
		idx = job.Session.Hash
	}
	if idx == 0 {
		idx = hashUserKey(job.UserKey)
	}
	if idx < 0 {
		idx = -idx
	}
	idx = idx % len(w.jobChans)
	select {
	case w.jobChans[idx] <- job:
		return true
	default:
		return false
	}
}

func (w *ingestWorker) process(job *ingestJob) {
	if job == nil {
		return
	}
	ctx := job.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if w.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.timeout)
		defer cancel()
	}
	if err := w.ingest(ctx, job.UserKey, job.Session, job.Messages, job.Options); err != nil {
		log.WarnfContext(ctx, "mem0: ingest failed for user %s/%s: %v", job.UserKey.AppName, job.UserKey.UserID, err)
	}
}

func (w *ingestWorker) ingest(
	ctx context.Context,
	userKey memory.UserKey,
	_ *session.Session,
	messages []model.Message,
	reqOpts session.IngestOptions,
) error {
	apiMsgs := make([]apiMessage, 0, len(messages))
	for _, m := range messages {
		content := messageText(m)
		if content == "" {
			continue
		}
		apiMsgs = append(apiMsgs, apiMessage{Role: m.Role.String(), Content: content})
	}
	if len(apiMsgs) == 0 {
		return nil
	}
	req := createMemoryRequest{
		Messages:  apiMsgs,
		UserID:    userKey.UserID,
		AppID:     userKey.AppName,
		AgentID:   reqOpts.AgentID,
		RunID:     reqOpts.RunID,
		Metadata:  cloneMetadata(reqOpts.Metadata),
		Infer:     true,
		Async:     w.asyncMode,
		Version:   w.version,
		OrgID:     w.orgID,
		ProjectID: w.projectID,
	}
	var events createMemoryEvents
	if err := w.c.doJSON(ctx, httpMethodPost, pathV1Memories, nil, req, &events); err != nil {
		return err
	}
	return w.awaitQueuedEvents(ctx, events)
}

func (w *ingestWorker) awaitQueuedEvents(ctx context.Context, events createMemoryEvents) error {
	for _, evt := range events {
		if eventID := strings.TrimSpace(evt.EventID); eventID != "" {
			if _, err := w.awaitIngestEvent(ctx, eventID); err != nil {
				return err
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(evt.Status), ingestEventStatusFailed) {
			return fmt.Errorf("mem0: ingest failed: %s", strings.TrimSpace(evt.Message))
		}
	}
	return nil
}

func (w *ingestWorker) awaitIngestEvent(ctx context.Context, eventID string) (*eventStatusResponse, error) {
	path := "/v1/event/" + eventID + "/"
	for {
		var out eventStatusResponse
		if err := w.c.doJSON(ctx, httpMethodGet, path, nil, nil, &out); err != nil {
			return nil, err
		}
		switch strings.ToUpper(strings.TrimSpace(out.Status)) {
		case ingestEventStatusSucceeded:
			return &out, nil
		case ingestEventStatusFailed:
			return nil, fmt.Errorf("mem0: ingest event %s failed", eventID)
		case "", ingestEventStatusPending, ingestEventStatusRunning:
		default:
			if len(out.Results) > 0 {
				return &out, nil
			}
		}
		timer := time.NewTimer(ingestEventPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func hashUserKey(userKey memory.UserKey) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userKey.AppName))
	_, _ = h.Write([]byte(userKey.UserID))
	return int(h.Sum32())
}
