//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

const maxResourceIngestionEntries = 1_000_000

func (dk *BuiltinKnowledge) validateResourceSourceIDs(sources []source.Source) error {
	if dk.resourceStore == nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, src := range sources {
		if src == nil {
			return errors.New("source cannot be nil")
		}
		_, isResourceSource := src.(source.ResourceSource)
		sourceID := src.Name()
		if previousIsResourceSource, duplicate := seen[sourceID]; duplicate {
			if previousIsResourceSource || isResourceSource {
				return fmt.Errorf("duplicate resource source ID %q", sourceID)
			}
		} else {
			seen[sourceID] = isResourceSource
		}
		if !isResourceSource {
			continue
		}
		if strings.TrimSpace(sourceID) == "" {
			return errors.New("resource source name cannot be empty")
		}
		if sourceID != strings.TrimSpace(sourceID) {
			return fmt.Errorf("resource source name %q has surrounding whitespace", sourceID)
		}
	}
	return nil
}

// readSourceDocuments persists source-level text before returning the derived
// documents that may reference it. Legacy Sources remain vector-only.
func (dk *BuiltinKnowledge) readSourceDocuments(
	ctx context.Context,
	src source.Source,
) ([]*document.Document, []string, error) {
	resourceSource, ok := src.(source.ResourceSource)
	if dk.resourceStore == nil || !ok {
		documents, err := src.ReadDocuments(ctx)
		return documents, nil, err
	}
	resources, err := resourceSource.ReadResources(ctx)
	if err != nil {
		return nil, nil, err
	}
	sourceID := src.Name()
	documents, resourcePaths, err := prepareSourceResources(sourceID, resources)
	if err != nil {
		return nil, nil, err
	}
	if err := dk.resourceStore.PutSource(ctx, &resourcestore.SourceInfo{
		ID:   sourceID,
		Name: sourceID,
		Type: strings.TrimSpace(src.Type()),
	}); err != nil {
		return nil, nil, publicResourceStoreError(ctx, err)
	}
	for index, resource := range resources {
		resourcePath := resourcePaths[index]
		info := &resourcestore.ResourceInfo{
			SourceID:   sourceID,
			Path:       resourcePath,
			Name:       path.Base(resourcePath),
			Size:       int64(len(resource.Content)),
			ModifiedAt: resource.ModifiedAt,
		}
		if err := dk.resourceStore.PutResource(ctx, info, strings.NewReader(resource.Content)); err != nil {
			return nil, nil, publicResourceStoreError(ctx, err)
		}
	}
	return documents, resourcePaths, nil
}

func prepareSourceResources(
	sourceID string,
	resources []*source.Resource,
) ([]*document.Document, []string, error) {
	seenPaths := make(map[string]struct{}, len(resources))
	documentPaths := make(map[*document.Document]string)
	resourcePaths := make([]string, 0, len(resources))
	var documents []*document.Document
	for _, resource := range resources {
		if resource == nil {
			return nil, nil, errors.New("resource source returned a nil resource")
		}
		resourcePath, ok := cleanResourcePath(resource.Path)
		if !ok {
			return nil, nil, fmt.Errorf("invalid resource path %q", resource.Path)
		}
		if _, duplicate := seenPaths[resourcePath]; duplicate {
			return nil, nil, fmt.Errorf("duplicate resource path %q", resourcePath)
		}
		if !utf8.ValidString(resource.Content) {
			return nil, nil, fmt.Errorf("resource %q is not valid UTF-8", resourcePath)
		}
		seenPaths[resourcePath] = struct{}{}
		resourcePaths = append(resourcePaths, resourcePath)
		for _, doc := range resource.Documents {
			if doc == nil {
				return nil, nil, fmt.Errorf("resource %q returned a nil document", resourcePath)
			}
			if previousPath, duplicate := documentPaths[doc]; duplicate {
				return nil, nil, fmt.Errorf(
					"document is shared by resources %q and %q",
					previousPath,
					resourcePath,
				)
			}
			documentPaths[doc] = resourcePath
			preparedDocument := doc.Clone()
			annotateResourceDocument(preparedDocument, sourceID, resourcePath, resource.Content)
			documents = append(documents, preparedDocument)
		}
	}
	return documents, resourcePaths, nil
}

func annotateResourceDocument(
	doc *document.Document,
	sourceID string,
	resourcePath string,
	resourceContent string,
) {
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]any)
	}
	totalLines := resourceLineCount(resourceContent)
	start, end, hasResourceRange := metadataLineRange(
		doc.Metadata,
		source.MetaResourceStartLine,
		source.MetaResourceEndLine,
		totalLines,
	)
	doc.Metadata[source.MetaSourceID] = sourceID
	doc.Metadata[source.MetaSourceName] = sourceID
	doc.Metadata[source.MetaResourcePath] = resourcePath
	delete(doc.Metadata, source.MetaResourceStartLine)
	delete(doc.Metadata, source.MetaResourceEndLine)

	if hasResourceRange {
		doc.Metadata[source.MetaResourceStartLine] = start
		doc.Metadata[source.MetaResourceEndLine] = end
		return
	}
	if start, end, ok := metadataResourceLineRange(doc.Metadata, totalLines); ok {
		doc.Metadata[source.MetaResourceStartLine] = start
		doc.Metadata[source.MetaResourceEndLine] = end
		return
	}
	if start, end, ok := uniqueDocumentLineRange(resourceContent, doc.Content); ok {
		doc.Metadata[source.MetaResourceStartLine] = start
		doc.Metadata[source.MetaResourceEndLine] = end
	}
}

func metadataResourceLineRange(metadata map[string]any, totalLines int) (int, int, bool) {
	return metadataLineRange(
		metadata,
		codeast.TrpcAstMetaPrefix+"line_start",
		codeast.TrpcAstMetaPrefix+"line_end",
		totalLines,
	)
}

func metadataLineRange(
	metadata map[string]any,
	startKey string,
	endKey string,
	totalLines int,
) (int, int, bool) {
	start, startOK := convertToInt(metadata[startKey])
	end, endOK := convertToInt(metadata[endKey])
	if !startOK || !endOK || start <= 0 || end < start || end > totalLines {
		return 0, 0, false
	}
	return start, end, true
}

func uniqueDocumentLineRange(content string, documentContent string) (int, int, bool) {
	if documentContent == "" {
		return 0, 0, false
	}
	startOffset := strings.Index(content, documentContent)
	if startOffset < 0 || startOffset != strings.LastIndex(content, documentContent) {
		return 0, 0, false
	}
	startLine := 1 + strings.Count(content[:startOffset], "\n")
	endOffset := startOffset + len(documentContent) - 1
	endLine := 1 + strings.Count(content[:endOffset], "\n")
	return startLine, endLine, true
}

func resourceLineCount(content string) int {
	if content == "" {
		return 0
	}
	lineCount := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lineCount++
	}
	return lineCount
}

func (dk *BuiltinKnowledge) cleanupStaleResources(
	ctx context.Context,
	currentPaths map[string][]string,
) error {
	if dk.resourceStore == nil || len(currentPaths) == 0 {
		return nil
	}
	for sourceID, paths := range currentPaths {
		existingPaths, err := dk.listStoredResourcePaths(ctx, sourceID)
		if err != nil {
			return err
		}
		current := make(map[string]struct{}, len(paths))
		for _, resourcePath := range paths {
			current[resourcePath] = struct{}{}
		}
		stale := make([]string, 0)
		for _, resourcePath := range existingPaths {
			if _, ok := current[resourcePath]; !ok {
				stale = append(stale, resourcePath)
			}
		}
		sort.Strings(stale)
		for _, resourcePath := range stale {
			if err := dk.resourceStore.DeleteResource(ctx, sourceID, resourcePath); err != nil &&
				!errors.Is(err, resourcestore.ErrNotFound) {
				return publicResourceStoreError(ctx, err)
			}
		}
	}
	return nil
}

func (dk *BuiltinKnowledge) listStoredResourcePaths(
	ctx context.Context,
	sourceID string,
) ([]string, error) {
	queue := []string{""}
	seenDirectories := map[string]struct{}{"": {}}
	seenEntries := make(map[string]struct{})
	var paths []string
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parentPath := queue[0]
		queue = queue[1:]
		entries, err := dk.resourceStore.ListResources(ctx, sourceID, parentPath)
		if err != nil {
			if parentPath == "" && errors.Is(err, resourcestore.ErrNotFound) {
				return nil, nil
			}
			return nil, publicResourceStoreError(ctx, err)
		}
		for _, entry := range entries {
			projected, valid := projectListedResource(sourceID, parentPath, entry)
			if !valid {
				return nil, ErrResourceStoreUnavailable
			}
			if _, duplicate := seenEntries[projected.Path]; duplicate {
				return nil, ErrResourceStoreUnavailable
			}
			seenEntries[projected.Path] = struct{}{}
			if len(seenEntries) > maxResourceIngestionEntries {
				return nil, ErrResourceLimitExceeded
			}
			if projected.IsDir {
				if _, duplicate := seenDirectories[projected.Path]; duplicate {
					return nil, ErrResourceStoreUnavailable
				}
				seenDirectories[projected.Path] = struct{}{}
				queue = append(queue, projected.Path)
				continue
			}
			paths = append(paths, projected.Path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (dk *BuiltinKnowledge) deleteStoredResourceSource(ctx context.Context, sourceID string) error {
	if dk.resourceStore == nil {
		return nil
	}
	if strings.TrimSpace(sourceID) == "" {
		return errors.New("source ID cannot be empty")
	}
	if sourceID != strings.TrimSpace(sourceID) {
		return errors.New("source ID cannot have surrounding whitespace")
	}
	if err := dk.resourceStore.DeleteSource(ctx, sourceID); err != nil &&
		!errors.Is(err, resourcestore.ErrNotFound) {
		return publicResourceStoreError(ctx, err)
	}
	return nil
}
