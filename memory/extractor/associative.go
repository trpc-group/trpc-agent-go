//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package extractor

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

// BuildAssociationDocumentsFromOperations converts extractor operations into
// association documents anchored to memory entry style references.
func BuildAssociationDocumentsFromOperations(
	userKey memory.UserKey,
	ops []*Operation,
) []memory.AssociationDocument {
	docs := make([]memory.AssociationDocument, 0, len(ops))
	for _, op := range ops {
		if op == nil || op.Type == OperationDelete || op.Type == OperationClear {
			continue
		}
		text := strings.TrimSpace(op.Memory)
		if text == "" {
			continue
		}
		docs = append(docs, memory.AssociationDocument{
			ID:   op.MemoryID,
			Text: text,
			Tags: op.Topics,
			Ref: memory.ContentRef{
				Kind:     memory.RefKindMemoryEntry,
				AppName:  userKey.AppName,
				UserID:   userKey.UserID,
				SourceID: op.MemoryID,
			},
			Metadata: memory.AssociationMetadata{
				Topics:       op.Topics,
				EventTime:    derefOperationTime(op.EventTime),
				Participants: op.Participants,
				Location:     op.Location,
				Kind:         op.MemoryKind,
			},
		})
	}
	return docs
}

func derefOperationTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
