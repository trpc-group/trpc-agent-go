//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deepsearch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const defaultQueryLimit = 10

// EntryRow contains one memory entry and its hidden row-attached index.
type EntryRow struct {
	Entry *memory.Entry
	Index *Index
}

type attachedEntry struct {
	*memory.Entry
	DeepSearchIndex       *Index    `json:"deepsearch_index,omitempty"`
	DeepSearchText        string    `json:"deepsearch_text,omitempty"`
	DeepSearchFingerprint string    `json:"deepsearch_fingerprint,omitempty"`
	DeepSearchVersion     int       `json:"deepsearch_version,omitempty"`
	DeepSearchIndexedAt   time.Time `json:"deepsearch_indexed_at,omitempty"`
}

// MarshalAttachedEntry marshals a memory entry with optional hidden DeepSearch
// fields. The public memory.Entry shape is unchanged when callers unmarshal
// the result into memory.Entry.
func MarshalAttachedEntry(entry *memory.Entry, index *Index) ([]byte, error) {
	if entry == nil {
		return nil, fmt.Errorf("deepsearch marshal entry: entry is nil")
	}
	row := attachedEntry{Entry: entry}
	if index != nil {
		row.DeepSearchIndex = index
		row.DeepSearchText = IndexText(index)
		row.DeepSearchFingerprint = index.SourceFingerprint
		row.DeepSearchVersion = index.Version
		row.DeepSearchIndexedAt = index.IndexedAt
	}
	return json.Marshal(row)
}

// UnmarshalAttachedEntry unmarshals a memory row JSON object and returns the
// public entry plus hidden DeepSearch index when present.
func UnmarshalAttachedEntry(data []byte) (*memory.Entry, *Index, error) {
	var row attachedEntry
	if err := json.Unmarshal(data, &row); err != nil {
		return nil, nil, err
	}
	if row.Entry == nil {
		return nil, nil, fmt.Errorf("deepsearch unmarshal entry: entry is nil")
	}
	return row.Entry, row.DeepSearchIndex, nil
}

// NewIndex builds a hidden row-attached index from one generated document.
func NewIndex(document Document, indexedAt time.Time) *Index {
	content := Content{
		ID:       document.ID,
		Text:     document.Text,
		Ref:      document.Ref,
		Metadata: document.Metadata,
		Created:  document.Created,
		Updated:  document.Updated,
	}
	return &Index{
		Version:           IndexVersion,
		Content:           content,
		Cues:              uniqueStrings(document.Cues),
		Tags:              uniqueStrings(document.Tags),
		SourceFingerprint: document.Metadata.SourceFingerprint,
		IndexedAt:         indexedAt,
	}
}

// IndexText returns a compact searchable text representation of an index.
func IndexText(index *Index) string {
	if index == nil {
		return ""
	}
	parts := make([]string, 0, 2+len(index.Cues)+len(index.Tags))
	parts = append(parts, index.Content.Text)
	parts = append(parts, index.Cues...)
	parts = append(parts, index.Tags...)
	return strings.Join(parts, "\n")
}

// IsCurrent reports whether a hidden index matches the source entry.
func IsCurrent(entry *memory.Entry, index *Index) bool {
	return entry != nil &&
		index != nil &&
		index.Version == IndexVersion &&
		index.SourceFingerprint == SourceFingerprint(entry)
}

// RowsCurrent reports whether every entry has a matching hidden index.
func RowsCurrent(rows []EntryRow) bool {
	for _, row := range rows {
		if !IsCurrent(row.Entry, row.Index) {
			return false
		}
	}
	return true
}

// SearchCues searches cue nodes from row-attached indexes.
func SearchCues(rows []EntryRow, req CueSearchRequest) *CueSearchResult {
	query := strings.TrimSpace(req.Query)
	limit := normalizeLimit(req.MaxResults)
	minScore := req.MinScore
	cues := make([]Cue, 0)
	for _, row := range rows {
		if row.Index == nil {
			continue
		}
		contentID := row.Index.Content.ID
		for i, cueText := range row.Index.Cues {
			score := textScore(cueText+" "+strings.Join(row.Index.Tags, " ")+" "+row.Index.Content.Text, query)
			if score <= 0 || score < minScore {
				continue
			}
			cues = append(cues, Cue{
				ID:    cueID(contentID, i),
				Text:  cueText,
				Score: score,
			})
		}
	}
	sort.SliceStable(cues, func(i, j int) bool {
		if cues[i].Score == cues[j].Score {
			return cues[i].ID < cues[j].ID
		}
		return cues[i].Score > cues[j].Score
	})
	if len(cues) > limit {
		cues = cues[:limit]
	}
	return &CueSearchResult{Query: query, Cues: cues}
}

// ExpandTags expands cue IDs or cue texts into tag/content paths.
func ExpandTags(rows []EntryRow, req TagExpandRequest) *TagExpandResult {
	limit := normalizeLimit(req.MaxContents)
	maxTags := req.MaxTagsPerCue
	if maxTags <= 0 {
		maxTags = defaultQueryLimit
	}
	wantIDs := stringSet(req.CueIDs)
	wantCues := normalizedSet(req.Cues)
	paths := make([]Path, 0)
	for _, row := range rows {
		if row.Index == nil {
			continue
		}
		contentID := row.Index.Content.ID
		for cueIndex, cueText := range row.Index.Cues {
			cue := Cue{ID: cueID(contentID, cueIndex), Text: cueText}
			if !matchesCue(cue, wantIDs, wantCues) {
				continue
			}
			for tagIndex, tagText := range row.Index.Tags {
				if tagIndex >= maxTags {
					break
				}
				tag := Tag{
					ID:        tagID(contentID, cueIndex, tagIndex),
					Text:      tagText,
					CueID:     cue.ID,
					ContentID: contentID,
					Weight:    1,
				}
				path := Path{Cue: cue, Tag: tag, Score: 1}
				if path.Score < req.MinPathScore {
					continue
				}
				if req.IncludeContent {
					content := row.Index.Content
					path.Content = &content
				}
				paths = append(paths, path)
			}
		}
	}
	if len(paths) > limit {
		paths = paths[:limit]
	}
	tags := make([]Tag, 0, len(paths))
	for _, path := range paths {
		tags = append(tags, path.Tag)
	}
	return &TagExpandResult{Tags: tags, Paths: paths}
}

// LoadContents loads content nodes by ID or source reference.
func LoadContents(rows []EntryRow, req ContentLoadRequest) *ContentLoadResult {
	limit := normalizeLimit(req.MaxResults)
	wantIDs := stringSet(req.ContentIDs)
	wantRefs := make(map[string]struct{}, len(req.Refs))
	for _, ref := range req.Refs {
		wantRefs[contentRefKey(ref)] = struct{}{}
	}
	contents := make([]Content, 0)
	for _, row := range rows {
		if row.Index == nil {
			continue
		}
		content := row.Index.Content
		if len(wantIDs) == 0 && len(wantRefs) == 0 {
			contents = append(contents, content)
			continue
		}
		if _, ok := wantIDs[content.ID]; ok {
			contents = append(contents, content)
			continue
		}
		if _, ok := wantRefs[contentRefKey(content.Ref)]; ok {
			contents = append(contents, content)
		}
	}
	sort.SliceStable(contents, func(i, j int) bool {
		return contents[i].Updated.After(contents[j].Updated)
	})
	if len(contents) > limit {
		contents = contents[:limit]
	}
	return &ContentLoadResult{Contents: contents}
}

func matchesCue(cue Cue, ids map[string]struct{}, cues map[string]struct{}) bool {
	if len(ids) == 0 && len(cues) == 0 {
		return true
	}
	if _, ok := ids[cue.ID]; ok {
		return true
	}
	_, ok := cues[normalizeTerm(cue.Text)]
	return ok
}

func cueID(contentID string, cueIndex int) string {
	return fmt.Sprintf("cue:%s:%d", contentID, cueIndex)
}

func tagID(contentID string, cueIndex, tagIndex int) string {
	return fmt.Sprintf("tag:%s:%d:%d", contentID, cueIndex, tagIndex)
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultQueryLimit
	}
	return limit
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func normalizedSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeTerm(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func contentRefKey(ref ContentRef) string {
	return string(ref.Kind) + "\x00" + ref.AppName + "\x00" + ref.UserID + "\x00" + ref.SourceID
}

func textScore(text, query string) float64 {
	queryTerms := terms(query)
	if len(queryTerms) == 0 {
		return 0
	}
	text = normalizeTerm(text)
	matches := 0
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	score := float64(matches) / float64(len(queryTerms))
	if strings.Contains(text, normalizeTerm(query)) {
		score += 0.25
	}
	if score > 1 {
		return 1
	}
	return score
}

func terms(value string) []string {
	fields := strings.Fields(normalizeTerm(value))
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func normalizeTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(
		",", " ",
		".", " ",
		":", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"\"", " ",
		"'", " ",
		"\n", " ",
		"\t", " ",
	).Replace(value)
	return strings.Join(strings.Fields(value), " ")
}
