//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// esDocument is the Elasticsearch document representation of a memory entry.
type esDocument struct {
	MemoryID     string     `json:"memory_id"`
	AppName      string     `json:"app_name"`
	UserID       string     `json:"user_id"`
	Content      string     `json:"content"`
	Topics       []string   `json:"topics,omitempty"`
	Kind         string     `json:"kind"`
	EventTime    *time.Time `json:"event_time,omitempty"`
	Participants []string   `json:"participants,omitempty"`
	Location     string     `json:"location,omitempty"`
	Embedding    []float32  `json:"embedding"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	// DeletedAt marks a soft-deleted document. The field is omitted
	// for active documents so that indexing a full document also
	// reactivates a tombstone.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// newESDocument builds the stored document for a normalized memory.
func newESDocument(
	memoryID string,
	userKey memory.UserKey,
	mem *memory.Memory,
	embedding []float32,
	createdAt, updatedAt time.Time,
) *esDocument {
	return &esDocument{
		MemoryID:     memoryID,
		AppName:      userKey.AppName,
		UserID:       userKey.UserID,
		Content:      mem.Memory,
		Topics:       mem.Topics,
		Kind:         string(imemory.EffectiveKind(mem)),
		EventTime:    mem.EventTime,
		Participants: mem.Participants,
		Location:     mem.Location,
		Embedding:    embedding,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
}

// tombstoneUpdate builds a partial update body marking the document deleted.
func tombstoneUpdate(deletedAt time.Time) map[string]any {
	return map[string]any{
		fieldDeletedAt: deletedAt,
	}
}

// buildEntry converts a stored document into a memory entry.
func buildEntry(doc *esDocument) *memory.Entry {
	mem := &memory.Memory{
		Memory:       doc.Content,
		Topics:       doc.Topics,
		LastUpdated:  &doc.UpdatedAt,
		Kind:         memory.Kind(doc.Kind),
		EventTime:    doc.EventTime,
		Participants: doc.Participants,
		Location:     doc.Location,
	}
	imemory.NormalizeMemory(mem)

	return &memory.Entry{
		ID:        doc.MemoryID,
		AppName:   doc.AppName,
		UserID:    doc.UserID,
		Memory:    mem,
		CreatedAt: doc.CreatedAt,
		UpdatedAt: doc.UpdatedAt,
	}
}

// searchHit is a single Elasticsearch hit.
type searchHit struct {
	ID     string          `json:"_id"`
	Score  *float64        `json:"_score"`
	Source json.RawMessage `json:"_source"`
}

// searchResponse is the subset of the Elasticsearch search response
// used by this package.
type searchResponse struct {
	Hits struct {
		Hits []searchHit `json:"hits"`
	} `json:"hits"`
}

// decodeSearchDocs parses a search response into stored documents and
// their scores. Malformed hits are skipped so a single bad document does
// not break a read.
func decodeSearchDocs(data []byte) ([]*esDocument, []float64, error) {
	var resp searchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode search response failed: %w", err)
	}

	docs := make([]*esDocument, 0, len(resp.Hits.Hits))
	scores := make([]float64, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		if len(hit.Source) == 0 {
			continue
		}
		var doc esDocument
		if err := json.Unmarshal(hit.Source, &doc); err != nil {
			continue
		}
		if doc.MemoryID == "" {
			continue
		}
		docs = append(docs, &doc)
		score := 0.0
		if hit.Score != nil {
			score = *hit.Score
		}
		scores = append(scores, score)
	}
	return docs, scores, nil
}

// docEntries converts stored documents into memory entries.
func docEntries(docs []*esDocument) []*memory.Entry {
	entries := make([]*memory.Entry, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, buildEntry(doc))
	}
	return entries
}

// convertToFloat32 converts an embedding to the float32 representation
// stored in the dense_vector field.
func convertToFloat32(embedding []float64) []float32 {
	result := make([]float32, len(embedding))
	for i, v := range embedding {
		result[i] = float32(v)
	}
	return result
}
