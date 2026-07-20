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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	_ session.Ingestor = (*Service)(nil)
	_ memory.Reader    = (*Service)(nil)
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
// Per-request settings are configured via session.IngestOption helpers.
// Call Ingest when a request needs Mem0-specific fields.
//
// An invalid session scope (empty AppName / UserID) is surfaced as an error
// rather than silently dropped, so caller misconfiguration is distinguishable
// from a successful no-op, matching ReadMemories / SearchMemories behaviour.
func (s *Service) IngestSession(
	ctx context.Context,
	sess *session.Session,
	opts ...session.IngestOption,
) error {
	return s.ingestSession(ctx, sess, resolveSessionIngestOptions(opts))
}

// Ingest enqueues a session transcript with Mem0-specific request options.
// It shares the same ingestion and watermark behavior as IngestSession while
// keeping provider fields out of the session package.
func (s *Service) Ingest(
	ctx context.Context,
	sess *session.Session,
	opts ...IngestOption,
) error {
	return s.ingestSession(ctx, sess, resolveIngestOptions(opts))
}

func (s *Service) ingestSession(
	ctx context.Context,
	sess *session.Session,
	reqOpts ingestOptions,
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
	if err := s.validateIngestOptions(reqOpts); err != nil {
		return err
	}
	writeLastExtractAt(sess, latestTs)
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

func resolveSessionIngestOptions(options []session.IngestOption) ingestOptions {
	var sessionOpts session.IngestOptions
	for _, option := range options {
		if option == nil {
			continue
		}
		option(&sessionOpts)
	}
	return ingestOptions{
		metadata: cloneMetadata(sessionOpts.Metadata),
		agentID:  sessionOpts.AgentID,
		runID:    sessionOpts.RunID,
	}
}

func resolveIngestOptions(options []IngestOption) ingestOptions {
	var opts ingestOptions
	for _, option := range options {
		if option == nil {
			continue
		}
		option(&opts)
	}
	opts.metadata = cloneMetadata(opts.metadata)
	if opts.infer != nil {
		infer := *opts.infer
		opts.infer = &infer
	}
	return opts
}

func (s *Service) validateIngestOptions(opts ingestOptions) error {
	if opts.expirationDate != "" {
		if _, err := time.Parse(time.DateOnly, opts.expirationDate); err != nil {
			return fmt.Errorf("mem0: invalid expiration date %q: %w", opts.expirationDate, err)
		}
	}
	if opts.memoryType != "" && opts.memoryType != MemoryTypeProcedural {
		return fmt.Errorf("mem0: unsupported memory type %q", opts.memoryType)
	}
	if opts.memoryType == MemoryTypeProcedural && strings.TrimSpace(opts.agentID) == "" {
		return errors.New("mem0: procedural memory requires an agent ID")
	}
	if s.opts.apiMode == apiModeSelfHostedOSS {
		return nil
	}
	if opts.prompt != "" || opts.expirationDate != "" || opts.memoryType != "" {
		return errors.New("mem0: prompt, expiration date, and memory type require self-hosted OSS mode")
	}
	return nil
}

// ReadMemories reads memories for a user.
func (s *Service) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	if s.opts.apiMode == apiModeSelfHostedOSS {
		entries, err := s.readOSSMemories(ctx, userKey, limit, OSSReadOptions{}, false)
		if err != nil {
			return nil, err
		}
		return entriesFromOSSMemories(entries), nil
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
		if limit > 0 && len(entries) >= limit {
			break
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

// ReadOSSMemories reads self-hosted OSS records with optional provider scopes.
// It returns an error when the service is not configured in OSS mode.
func (s *Service) ReadOSSMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
	opts OSSReadOptions,
) ([]*OSSMemory, error) {
	if s.opts.apiMode != apiModeSelfHostedOSS {
		return nil, errors.New("mem0: OSS read options require self-hosted OSS mode")
	}
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	return s.readOSSMemories(ctx, userKey, limit, opts, true)
}

func (s *Service) readOSSMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
	opts OSSReadOptions,
	includeProviderFields bool,
) ([]*OSSMemory, error) {
	if limit <= 0 {
		return nil, errors.New("mem0: self-hosted OSS ReadMemories requires a positive limit")
	}
	if limit > maxOSSListTopK {
		return nil, fmt.Errorf("mem0: self-hosted OSS ReadMemories limit %d exceeds maximum %d", limit, maxOSSListTopK)
	}
	q := url.Values{}
	q.Set(queryKeyUserID, userKey.UserID)
	if opts.AgentID != "" {
		q.Set(queryKeyAgentID, opts.AgentID)
	}
	if opts.RunID != "" {
		q.Set(queryKeyRunID, opts.RunID)
	}
	if opts.IncludeExpired {
		q.Set(queryKeyShowExpired, "true")
	}
	// The current OSS GET /memories API can only scope by user_id, run_id, or
	// agent_id, not by metadata.trpc_app_name. Fetch the server-side cap and
	// enforce app isolation locally as a best-effort view over those candidates.
	q.Set(queryKeyTopK, itoa(maxOSSListTopK))

	var batch listMemoriesResponse
	if err := s.c.doJSON(ctx, httpMethodGet, pathOSSMemories, q, nil, &batch); err != nil {
		return nil, err
	}
	entries := make([]*OSSMemory, 0, len(batch.Results))
	for i := range batch.Results {
		rec := &batch.Results[i]
		if !recordMatchesTRPCApp(rec, userKey.AppName, s.opts.includeUnscopedSelfHostedOSSMemories) {
			continue
		}
		var item *OSSMemory
		if includeProviderFields {
			item = toOSSMemory(userKey.AppName, userKey.UserID, rec)
		} else if entry := toEntry(userKey.AppName, userKey.UserID, rec); entry != nil {
			item = &OSSMemory{Entry: entry}
		}
		if item == nil {
			continue
		}
		entries = append(entries, item)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Entry.UpdatedAt.Equal(entries[j].Entry.UpdatedAt) {
			return entries[i].Entry.CreatedAt.After(entries[j].Entry.CreatedAt)
		}
		return entries[i].Entry.UpdatedAt.After(entries[j].Entry.UpdatedAt)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// SearchMemories searches memories for a user.
func (s *Service) SearchMemories(ctx context.Context, userKey memory.UserKey, query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	searchOpts := memory.ResolveSearchOptions(query, opts)
	results, err := s.searchMemories(ctx, userKey, searchOpts, OSSSearchOptions{}, false)
	if err != nil {
		return nil, err
	}
	return entriesFromOSSMemories(results), nil
}

// SearchOSSMemories searches self-hosted OSS records and preserves optional
// provider fields and ranking diagnostics. It returns an error when the service
// is not configured in OSS mode.
func (s *Service) SearchOSSMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts OSSSearchOptions,
) ([]*OSSMemory, error) {
	if s.opts.apiMode != apiModeSelfHostedOSS {
		return nil, errors.New("mem0: OSS search options require self-hosted OSS mode")
	}
	searchOpts := opts.SearchOptions
	if strings.TrimSpace(searchOpts.Query) == "" {
		searchOpts.Query = query
	}
	return s.searchMemories(ctx, userKey, searchOpts, opts, true)
}

func (s *Service) searchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	searchOpts memory.SearchOptions,
	ossOpts OSSSearchOptions,
	includeProviderFields bool,
) ([]*OSSMemory, error) {
	if err := userKey.CheckUserKey(); err != nil {
		return nil, err
	}
	searchOpts.Query = strings.TrimSpace(searchOpts.Query)
	if searchOpts.Query == "" {
		return []*OSSMemory{}, nil
	}
	maxResults := defaultSearchTopK
	if searchOpts.MaxResults > 0 {
		maxResults = searchOpts.MaxResults
	}
	path := pathV2Search
	filters := cloudSearchFilters(userKey, s.opts)
	if s.opts.apiMode == apiModeSelfHostedOSS {
		path = pathOSSSearch
		filters = ossSearchFilters(
			userKey,
			s.opts.includeUnscopedSelfHostedOSSMemories,
			ossOpts,
		)
	}
	req := searchV2Request{
		Query:   searchOpts.Query,
		Filters: filters,
		TopK:    searchCandidateLimit(searchOpts, maxResults),
	}
	if s.opts.apiMode == apiModeSelfHostedOSS {
		if searchOpts.SimilarityThreshold > 0 && searchOpts.SimilarityThreshold <= 1 {
			threshold := searchOpts.SimilarityThreshold
			req.Threshold = &threshold
		}
		req.Explain = ossOpts.Explain
		req.ShowExpired = ossOpts.IncludeExpired
	}
	var resp searchV2Response
	if err := s.c.doJSON(ctx, httpMethodPost, path, nil, req, &resp); err != nil {
		return nil, err
	}
	results := make([]*OSSMemory, 0, len(resp.Memories))
	for _, m := range resp.Memories {
		rec := m.toMemoryRecord()
		if s.opts.apiMode == apiModeSelfHostedOSS &&
			!recordMatchesTRPCApp(&rec, userKey.AppName, s.opts.includeUnscopedSelfHostedOSSMemories) {
			continue
		}
		var item *OSSMemory
		if includeProviderFields {
			item = toOSSMemory(userKey.AppName, userKey.UserID, &rec)
		} else if entry := toEntry(userKey.AppName, userKey.UserID, &rec); entry != nil {
			item = &OSSMemory{Entry: entry}
		}
		if item == nil {
			continue
		}
		item.Entry.Score = m.Score
		if !matchesSearchFilters(item.Entry, searchOpts) {
			continue
		}
		results = append(results, item)
	}
	sortOSSMemories(results, searchOpts)
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
