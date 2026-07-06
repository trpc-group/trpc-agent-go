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
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Service provides an ingest-first integration with mem0.
type Service struct {
	opts serviceOpts
	c    *client

	ingestWorker *ingestWorker

	precomputedTools []tool.Tool
}

// NewService creates a new mem0 service.
func NewService(options ...ServiceOpt) (*Service, error) {
	opts := defaultOptions.clone()
	for _, opt := range options {
		opt(&opts)
	}
	if opts.apiMode == apiModeSelfHostedOSS {
		if isCloudDefaultHost(opts.host) {
			return nil, errors.New("mem0: self-hosted OSS cannot use the hosted platform default host")
		}
		if opts.orgID != "" || opts.projectID != "" {
			return nil, errors.New("mem0: org/project identifiers are not supported by self-hosted OSS")
		}
	}
	c, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	svc := &Service{opts: opts, c: c}
	svc.ingestWorker = newIngestWorker(c, opts)
	svc.precomputedTools = buildReadOnlyTools(svc)
	return svc, nil
}

func isCloudDefaultHost(host string) bool {
	return strings.TrimRight(host, "/") == strings.TrimRight(defaultHost, "/")
}

// Tools returns the mem0 read-only tools exposed to the agent.
func (s *Service) Tools() []tool.Tool {
	return append([]tool.Tool(nil), s.precomputedTools...)
}

// IngestSession enqueues session transcript ingestion into mem0.
//
// Per-request settings are configured via session.IngestOption helpers
// (e.g. session.WithIngestMetadata, session.WithIngestAgentID,
// session.WithIngestRunID). The resolved snapshot is forwarded to mem0's
// POST /v1/memories/ payload as metadata, agent_id and run_id respectively.
//
// An invalid session scope (empty AppName / UserID) is surfaced as an error
// rather than silently dropped, so caller misconfiguration is distinguishable
// from a successful no-op, matching ReadMemories / SearchMemories behaviour.
func (s *Service) IngestSession(
	ctx context.Context,
	sess *session.Session,
	opts ...session.IngestOption,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.ingestWorker == nil || sess == nil {
		return nil
	}
	userKey := memory.UserKey{AppName: sess.AppName, UserID: sess.UserID}
	if err := userKey.CheckUserKey(); err != nil {
		return err
	}
	since := readLastExtractAt(sess)
	latestTs, messages := scanDeltaSince(sess, since)
	if len(messages) == 0 {
		return nil
	}
	writeLastExtractAt(sess, latestTs)
	var reqOpts session.IngestOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&reqOpts)
	}
	job := &ingestJob{
		Ctx:      context.WithoutCancel(ctx),
		UserKey:  userKey,
		Session:  sess,
		LatestTs: latestTs,
		Messages: messages,
		Options:  reqOpts,
	}
	if s.ingestWorker.tryEnqueue(ctx, job) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	log.DebugfContext(ctx, "mem0: ingest queue full, processing synchronously for user %s/%s", userKey.AppName, userKey.UserID)
	timeout := s.opts.memoryJobTimeout
	if timeout <= 0 {
		timeout = defaultMemoryJobTimeout
	}
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return s.ingestWorker.ingest(syncCtx, userKey, sess, messages, reqOpts)
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if s.opts.apiMode == apiModeSelfHostedOSS {
		return s.readOSSMemories(ctx, userKey, limit)
	}
	pageSize := defaultListPageSize
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	var entries []*memory.Entry
	page := 1
	for {
		q := url.Values{}
		q.Set(queryKeyUserID, userKey.UserID)
		q.Set(queryKeyAppID, userKey.AppName)
		q.Set(queryKeyPage, itoa(page))
		q.Set(queryKeyPageSize, itoa(pageSize))
		addOrgProjectQuery(q, s.opts)

		var batch listMemoriesResponse
		if err := s.c.doJSON(ctx, httpMethodGet, pathV1Memories, q, nil, &batch); err != nil {
			if isInvalidPageError(err) {
				break
			}
			return nil, err
		}
		if len(batch.Results) == 0 {
			break
		}
		for i := range batch.Results {
			if entry := toEntry(userKey.AppName, userKey.UserID, &batch.Results[i]); entry != nil {
				entries = append(entries, entry)
			}
		}
		page++
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *Service) readOSSMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if limit <= 0 {
		return nil, errors.New("mem0: self-hosted OSS ReadMemories requires a positive limit")
	}
	if limit > maxOSSListTopK {
		return nil, fmt.Errorf("mem0: self-hosted OSS ReadMemories limit %d exceeds maximum %d", limit, maxOSSListTopK)
	}
	q := url.Values{}
	q.Set(queryKeyUserID, userKey.UserID)
	q.Set(queryKeyTopK, itoa(maxOSSListTopK))

	var batch listMemoriesResponse
	if err := s.c.doJSON(ctx, httpMethodGet, pathOSSMemories, q, nil, &batch); err != nil {
		return nil, err
	}
	entries := make([]*memory.Entry, 0, len(batch.Results))
	for i := range batch.Results {
		rec := &batch.Results[i]
		if !recordMatchesTRPCApp(rec, userKey.AppName, s.opts.includeUnscopedSelfHostedOSSMemories) {
			continue
		}
		if entry := toEntry(userKey.AppName, userKey.UserID, rec); entry != nil {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey, query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	searchOpts := memory.ResolveSearchOptions(query, opts)
	searchOpts.Query = strings.TrimSpace(searchOpts.Query)
	if searchOpts.Query == "" {
		return []*memory.Entry{}, nil
	}
	maxResults := defaultSearchTopK
	if searchOpts.MaxResults > 0 {
		maxResults = searchOpts.MaxResults
	}
	path := pathV2Search
	filters := cloudSearchFilters(userKey, s.opts)
	if s.opts.apiMode == apiModeSelfHostedOSS {
		path = pathOSSSearch
		filters = ossSearchFilters(userKey, s.opts.includeUnscopedSelfHostedOSSMemories)
	}
	req := searchV2Request{
		Query:   searchOpts.Query,
		Filters: filters,
		TopK:    searchCandidateLimit(searchOpts, maxResults),
	}
	var resp searchV2Response
	if err := s.c.doJSON(ctx, httpMethodPost, path, nil, req, &resp); err != nil {
		return nil, err
	}
	results := make([]*memory.Entry, 0, len(resp.Memories))
	for _, m := range resp.Memories {
		rec := memoryRecord{
			ID:        m.ID,
			Memory:    m.Memory,
			Metadata:  m.Metadata,
			UserID:    m.UserID,
			AppID:     m.AppID,
			CreatedAt: m.CreatedAt,
		}
		if m.UpdatedAt != nil {
			rec.UpdatedAt = *m.UpdatedAt
		}
		if s.opts.apiMode == apiModeSelfHostedOSS &&
			!recordMatchesTRPCApp(&rec, userKey.AppName, s.opts.includeUnscopedSelfHostedOSSMemories) {
			continue
		}
		entry := toEntry(userKey.AppName, userKey.UserID, &rec)
		if entry == nil {
			continue
		}
		entry.Score = m.Score
		if !matchesSearchFilters(entry, searchOpts) {
			continue
		}
		results = append(results, entry)
	}
	sortSearchResults(results, searchOpts)
	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
}

// Close stops background workers and releases resources.
func (s *Service) Close() error {
	if s.ingestWorker != nil {
		s.ingestWorker.Stop()
	}
	return nil
}
