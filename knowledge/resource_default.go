//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
)

const (
	defaultResourceReadLines = 200
	maxResourceListEntries   = 1000
	maxResourceReadLines     = 1000
	maxResourceReadBytes     = 8 << 20
	maxResourceLineBytes     = 1 << 20
	defaultResourceMatches   = 20
	maxResourceMatches       = 100
	maxResourceGrepContext   = 20
	maxResourceGrepBytes     = 16 << 20
	maxResourceGrepLines     = 250000
	maxResourcePatternBytes  = 4096
)

// ListSources lists sources persisted in the configured resource store.
func (dk *BuiltinKnowledge) ListSources(
	ctx context.Context,
	req *ListSourcesRequest,
) (*ListSourcesResult, error) {
	if req == nil {
		return nil, errors.New("list sources request cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, err := dk.configuredResourceStore(ctx)
	if err != nil {
		return nil, err
	}
	listed, err := store.ListSources(ctx)
	if err != nil {
		return nil, publicResourceStoreError(ctx, err)
	}
	if len(listed) > maxResourceListEntries {
		return nil, ErrResourceLimitExceeded
	}
	result := &ListSourcesResult{Sources: make([]*SourceInfo, 0, len(listed))}
	seen := make(map[string]struct{}, len(listed))
	for _, info := range listed {
		if info == nil {
			return nil, ErrResourceStoreUnavailable
		}
		sourceID := strings.TrimSpace(info.ID)
		if sourceID == "" {
			return nil, ErrResourceStoreUnavailable
		}
		if _, duplicate := seen[sourceID]; duplicate {
			return nil, ErrResourceStoreUnavailable
		}
		seen[sourceID] = struct{}{}
		projected := *info
		projected.ID = sourceID
		projected.Name = strings.TrimSpace(projected.Name)
		projected.Type = strings.TrimSpace(projected.Type)
		result.Sources = append(result.Sources, &projected)
	}
	sort.Slice(result.Sources, func(i, j int) bool {
		return result.Sources[i].ID < result.Sources[j].ID
	})
	return result, nil
}

// ListResources lists direct children from a persisted source snapshot.
func (dk *BuiltinKnowledge) ListResources(
	ctx context.Context,
	req *ListResourcesRequest,
) (*ListResourcesResult, error) {
	if req == nil {
		return nil, errors.New("list resources request cannot be nil")
	}
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		return nil, errors.New("source ID cannot be empty")
	}
	parentPath, ok := cleanOptionalResourcePath(req.ParentPath)
	if !ok {
		return nil, errors.New("invalid parent path")
	}
	store, err := dk.configuredResourceStore(ctx)
	if err != nil {
		return nil, err
	}
	listed, err := store.ListResources(ctx, sourceID, parentPath)
	if err != nil {
		return nil, publicResourceStoreError(ctx, err)
	}
	if len(listed) > maxResourceListEntries {
		return nil, ErrResourceLimitExceeded
	}
	result := &ListResourcesResult{Resources: make([]*resourcestore.ResourceInfo, 0, len(listed))}
	seen := make(map[string]struct{}, len(listed))
	for _, info := range listed {
		projected, valid := projectListedResource(sourceID, parentPath, info)
		if !valid {
			return nil, ErrResourceStoreUnavailable
		}
		if _, duplicate := seen[projected.Path]; duplicate {
			return nil, ErrResourceStoreUnavailable
		}
		seen[projected.Path] = struct{}{}
		result.Resources = append(result.Resources, projected)
	}
	sort.Slice(result.Resources, func(i, j int) bool {
		return result.Resources[i].Path < result.Resources[j].Path
	})
	return result, nil
}

// ReadResource reads a bounded, inclusive line range from persisted content.
func (dk *BuiltinKnowledge) ReadResource(
	ctx context.Context,
	req *ReadResourceRequest,
) (*ReadResourceResult, error) {
	if req == nil {
		return nil, errors.New("read resource request cannot be nil")
	}
	startLine := req.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	endLine := req.EndLine
	if endLine <= 0 {
		endLine = startLine + defaultResourceReadLines - 1
	}
	if endLine < startLine {
		return nil, errors.New("end line must not be before start line")
	}
	if endLine-startLine+1 > maxResourceReadLines {
		return nil, ErrResourceLimitExceeded
	}
	sourceID, resourcePath, content, err := dk.openStoredResource(ctx, req.SourceID, req.Path)
	if err != nil {
		return nil, err
	}
	defer content.Close()

	lines, eof, err := readResourceLines(ctx, content, startLine, endLine)
	if err != nil {
		return nil, err
	}
	actualEnd := 0
	if len(lines) > 0 {
		actualEnd = startLine + len(lines) - 1
	}
	result := &ReadResourceResult{
		SourceID:  sourceID,
		Path:      resourcePath,
		Lines:     lines,
		StartLine: startLine,
		EndLine:   actualEnd,
		EOF:       eof,
	}
	if !eof {
		result.NextStartLine = endLine + 1
	}
	return result, nil
}

// GrepResource finds bounded literal or regular-expression matches in persisted content.
func (dk *BuiltinKnowledge) GrepResource(
	ctx context.Context,
	req *GrepResourceRequest,
) (*GrepResourceResult, error) {
	if req == nil {
		return nil, errors.New("grep resource request cannot be nil")
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, errors.New("grep pattern cannot be empty")
	}
	if len(req.Pattern) > maxResourcePatternBytes {
		return nil, ErrResourceLimitExceeded
	}
	if req.Before < 0 || req.After < 0 {
		return nil, errors.New("grep context cannot be negative")
	}
	if req.Before > maxResourceGrepContext || req.After > maxResourceGrepContext {
		return nil, ErrResourceLimitExceeded
	}
	maxMatches := req.MaxMatches
	if maxMatches <= 0 {
		maxMatches = defaultResourceMatches
	}
	if maxMatches > maxResourceMatches {
		return nil, ErrResourceLimitExceeded
	}
	matcher, err := buildResourceMatcher(req.Pattern, req.Regex)
	if err != nil {
		return nil, err
	}
	sourceID, resourcePath, content, err := dk.openStoredResource(ctx, req.SourceID, req.Path)
	if err != nil {
		return nil, err
	}
	defer content.Close()
	lines, err := readAllResourceLines(ctx, content)
	if err != nil {
		return nil, err
	}
	matchLines := make([]int, 0, maxMatches+1)
	for index, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !matcher(line) {
			continue
		}
		matchLines = append(matchLines, index+1)
		if len(matchLines) > maxMatches {
			break
		}
	}
	truncated := len(matchLines) > maxMatches
	if truncated {
		matchLines = matchLines[:maxMatches]
	}
	return &GrepResourceResult{
		SourceID:  sourceID,
		Path:      resourcePath,
		Blocks:    buildGrepBlocks(lines, matchLines, req.Before, req.After),
		Truncated: truncated,
	}, nil
}

func (dk *BuiltinKnowledge) openStoredResource(
	ctx context.Context,
	sourceID string,
	resourcePath string,
) (string, string, io.ReadCloser, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return "", "", nil, errors.New("source ID cannot be empty")
	}
	cleanedPath, ok := cleanResourcePath(resourcePath)
	if !ok {
		return "", "", nil, errors.New("invalid resource path")
	}
	store, err := dk.configuredResourceStore(ctx)
	if err != nil {
		return "", "", nil, err
	}
	content, err := store.OpenResource(ctx, sourceID, cleanedPath)
	if err != nil {
		return "", "", nil, publicResourceStoreError(ctx, err)
	}
	if content == nil {
		return "", "", nil, ErrResourceRepresentationUnavailable
	}
	return sourceID, cleanedPath, content, nil
}

func (dk *BuiltinKnowledge) configuredResourceStore(ctx context.Context) (resourcestore.Store, error) {
	if dk == nil || dk.resourceStore == nil {
		return nil, ErrResourceCapabilityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return dk.resourceStore, nil
}

func projectListedResource(
	sourceID string,
	parentPath string,
	info *resourcestore.ResourceInfo,
) (*resourcestore.ResourceInfo, bool) {
	if info == nil {
		return nil, false
	}
	if listedSourceID := strings.TrimSpace(info.SourceID); listedSourceID != "" && listedSourceID != sourceID {
		return nil, false
	}
	resourcePath, ok := cleanResourcePath(info.Path)
	if !ok || parentResourcePath(resourcePath) != parentPath {
		return nil, false
	}
	projected := *info
	projected.SourceID = sourceID
	projected.Path = resourcePath
	if projected.Name == "" {
		projected.Name = path.Base(resourcePath)
	}
	return &projected, true
}

func parentResourcePath(value string) string {
	parent := path.Dir(value)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

func readResourceLines(ctx context.Context, reader io.Reader, startLine, endLine int) ([]string, bool, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxResourceLineBytes)
	lines := make([]string, 0, endLine-startLine+1)
	lineNumber := 0
	bytesRead := 0
	more := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		lineNumber++
		bytesRead += len(scanner.Bytes()) + 1
		if bytesRead > maxResourceReadBytes {
			return nil, false, ErrResourceLimitExceeded
		}
		if lineNumber < startLine {
			continue
		}
		if lineNumber > endLine {
			more = true
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, ctxErr
		}
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, false, ErrResourceLimitExceeded
		}
		return nil, false, ErrResourceStoreUnavailable
	}
	if more {
		return lines, false, nil
	}
	return lines, true, nil
}

func readAllResourceLines(ctx context.Context, reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxResourceLineBytes)
	lines := make([]string, 0)
	bytesRead := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bytesRead += len(scanner.Bytes()) + 1
		if bytesRead > maxResourceGrepBytes || len(lines) >= maxResourceGrepLines {
			return nil, ErrResourceLimitExceeded
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, ErrResourceLimitExceeded
		}
		return nil, ErrResourceStoreUnavailable
	}
	return lines, nil
}

func buildResourceMatcher(pattern string, regex bool) (func(string) bool, error) {
	if !regex {
		return func(line string) bool { return strings.Contains(line, pattern) }, nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile grep pattern: %w", err)
	}
	return compiled.MatchString, nil
}

func publicResourceStoreError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	for _, publicErr := range []error{
		ErrResourceNotFound,
		ErrResourceRepresentationUnavailable,
		ErrResourcePermissionDenied,
		ErrResourceStoreUnavailable,
	} {
		if errors.Is(err, publicErr) {
			return publicErr
		}
	}
	return ErrResourceStoreUnavailable
}

func buildGrepBlocks(lines []string, matches []int, before, after int) []*GrepBlock {
	blocks := make([]*GrepBlock, 0, len(matches))
	for _, matchLine := range matches {
		start := max(1, matchLine-before)
		end := min(len(lines), matchLine+after)
		if len(blocks) > 0 && start <= blocks[len(blocks)-1].EndLine+1 {
			block := blocks[len(blocks)-1]
			if end > block.EndLine {
				block.Lines = append(block.Lines, lines[block.EndLine:end]...)
				block.EndLine = end
			}
			block.MatchLines = append(block.MatchLines, matchLine)
			continue
		}
		blocks = append(blocks, &GrepBlock{
			StartLine:  start,
			EndLine:    end,
			Lines:      append([]string(nil), lines[start-1:end]...),
			MatchLines: []int{matchLine},
		})
	}
	return blocks
}
