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
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	metadataKeyTRPCTopics       = "trpc_topics"
	metadataKeyTRPCAppName      = "trpc_app_name"
	metadataKeyTRPCKind         = "trpc_kind"
	metadataKeyTRPCEventTime    = "trpc_event_time"
	metadataKeyTRPCParticipants = "trpc_participants"
	metadataKeyTRPCLocation     = "trpc_location"

	pathV1Memories = "/v1/memories/"
	pathV2Search   = "/v2/memories/search/"

	queryKeyUserID   = "user_id"
	queryKeyAppID    = "app_id"
	queryKeyPage     = "page"
	queryKeyPageSize = "page_size"

	memoryUserRole = "user"

	defaultListPageSize = 100
	defaultSearchTopK   = 20
)

func addOrgProjectQuery(q url.Values, opts serviceOpts) {
	if q == nil {
		return
	}
	if opts.orgID != "" {
		q.Set("org_id", opts.orgID)
	}
	if opts.projectID != "" {
		q.Set("project_id", opts.projectID)
	}
}

func addOrgProjectFilter(filters map[string]any, opts serviceOpts) {
	if filters == nil {
		return
	}
	andRaw, ok := filters["AND"]
	if !ok {
		return
	}
	andList, ok := andRaw.([]any)
	if !ok {
		return
	}
	if opts.orgID != "" {
		andList = append(andList, map[string]any{"org_id": opts.orgID})
	}
	if opts.projectID != "" {
		andList = append(andList, map[string]any{"project_id": opts.projectID})
	}
	filters["AND"] = andList
}

func parseMem0Times(rec *memoryRecord) parsedTimes {
	if rec == nil {
		return parsedTimes{}
	}
	var createdAt, updatedAt time.Time
	if t, ok := parseMem0Time(rec.CreatedAt); ok {
		createdAt = t
	}
	if t, ok := parseMem0Time(rec.UpdatedAt); ok {
		updatedAt = t
	}
	return parsedTimes{CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func parseMem0Time(s string) (time.Time, bool) {
	str := strings.TrimSpace(s)
	if str == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, str); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, str); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func toEntry(appName, userID string, rec *memoryRecord) *memory.Entry {
	if rec == nil || strings.TrimSpace(rec.ID) == "" || strings.TrimSpace(rec.Memory) == "" {
		return nil
	}
	times := parseMem0Times(rec)
	updatedAt := times.UpdatedAt
	mem := &memory.Memory{
		Memory:      rec.Memory,
		Topics:      readTopicsFromMetadata(rec.Metadata),
		LastUpdated: &updatedAt,
		Kind:        readKindFromMetadata(rec.Metadata),
		EventTime:   readEventTimeFromMetadata(rec.Metadata),
		Participants: readParticipantsFromMetadata(
			rec.Metadata,
		),
		Location: readLocationFromMetadata(rec.Metadata),
	}
	return &memory.Entry{
		ID:        rec.ID,
		AppName:   appName,
		UserID:    userID,
		Memory:    mem,
		CreatedAt: times.CreatedAt,
		UpdatedAt: times.UpdatedAt,
	}
}

func readTopicsFromMetadata(meta map[string]any) []string {
	if meta == nil {
		return nil
	}
	raw, ok := meta[metadataKeyTRPCTopics]
	if !ok || raw == nil {
		return nil
	}
	if arr, ok := raw.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			s, ok := v.(string)
			if ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
		return []string{s}
	}
	return nil
}

func readKindFromMetadata(meta map[string]any) memory.Kind {
	if meta == nil {
		return ""
	}
	kind, _ := meta[metadataKeyTRPCKind].(string)
	return memory.Kind(strings.TrimSpace(kind))
}

func readEventTimeFromMetadata(meta map[string]any) *time.Time {
	if meta == nil {
		return nil
	}
	value, _ := meta[metadataKeyTRPCEventTime].(string)
	eventTime, ok := parseMem0Time(value)
	if !ok {
		return nil
	}
	return &eventTime
}

func readParticipantsFromMetadata(meta map[string]any) []string {
	if meta == nil {
		return nil
	}
	raw, ok := meta[metadataKeyTRPCParticipants]
	if !ok || raw == nil {
		return nil
	}
	if arr, ok := raw.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			s, ok := v.(string)
			if ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if arr, ok := raw.([]string); ok {
		out := make([]string, 0, len(arr))
		for _, s := range arr {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func readLocationFromMetadata(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	location, _ := meta[metadataKeyTRPCLocation].(string)
	return strings.TrimSpace(location)
}

func messageText(msg model.Message) string {
	if strings.TrimSpace(msg.Content) != "" {
		return strings.TrimSpace(msg.Content)
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}
	var parts []string
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		if strings.TrimSpace(*part.Text) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(*part.Text))
	}
	return strings.Join(parts, "\n")
}

func matchesSearchFilters(entry *memory.Entry, opts memory.SearchOptions) bool {
	if entry == nil || entry.Memory == nil {
		return false
	}
	strictKind := opts.Kind != "" && !opts.KindFallback
	if strictKind && entry.Memory.Kind != opts.Kind {
		return false
	}
	if (opts.TimeAfter != nil || opts.TimeBefore != nil) && entry.Memory.EventTime == nil {
		return false
	}
	if opts.TimeAfter != nil && entry.Memory.EventTime != nil && entry.Memory.EventTime.Before(*opts.TimeAfter) {
		return false
	}
	if opts.TimeBefore != nil && entry.Memory.EventTime != nil && entry.Memory.EventTime.After(*opts.TimeBefore) {
		return false
	}
	if opts.SimilarityThreshold > 0 && entry.Score < opts.SimilarityThreshold {
		return false
	}
	return true
}

func sortSearchResults(results []*memory.Entry, opts memory.SearchOptions) {
	sort.Slice(results, func(i, j int) bool {
		if opts.Kind != "" && opts.KindFallback {
			ik := results[i] != nil && results[i].Memory != nil && results[i].Memory.Kind == opts.Kind
			jk := results[j] != nil && results[j].Memory != nil && results[j].Memory.Kind == opts.Kind
			if ik != jk {
				return ik
			}
		}
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if opts.OrderByEventTime {
			it, jt := results[i].Memory.EventTime, results[j].Memory.EventTime
			switch {
			case it != nil && jt != nil && !it.Equal(*jt):
				return it.Before(*jt)
			case it != nil && jt == nil:
				return true
			case it == nil && jt != nil:
				return false
			}
		}
		if results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		}
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})
}

func searchCandidateLimit(opts memory.SearchOptions, maxResults int) int {
	limit := defaultSearchTopK
	if maxResults > limit {
		limit = maxResults
	}
	if opts.Kind != "" || opts.TimeAfter != nil || opts.TimeBefore != nil {
		if limit < defaultListPageSize {
			limit = defaultListPageSize
		}
		if maxResults > 0 && limit < maxResults*3 {
			limit = maxResults * 3
		}
	}
	return limit
}

func isInvalidPageError(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound && strings.Contains(strings.ToLower(apiErr.Body), "invalid page")
}

// cloneMetadata returns a deep clone of meta with no aliased nested state.
//
// Ingestion runs asynchronously on a worker goroutine, so the outer map and
// any nested containers must be independent of the caller's memory: otherwise
// a caller mutating its metadata map after IngestSession has returned could
// race with, or change, the payload the worker eventually marshals. The clone
// round-trips through JSON because the metadata is ultimately transmitted to
// mem0 as JSON — so the canonicalization is lossless with respect to what the
// backend actually receives.
func cloneMetadata(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		// Metadata that cannot be serialized would also fail downstream when
		// the worker builds the createMemoryRequest payload; drop it here so
		// the ingest payload simply omits metadata rather than aliasing the
		// caller's map.
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
