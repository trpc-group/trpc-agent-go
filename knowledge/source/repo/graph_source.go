//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package repo

import (
	"context"
	//nolint:gosec // Used only for stable graph IDs, not cryptographic security.
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	docreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// ReadGraph reads repository code as graph-native data.
func (s *Source) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	parseConcurrency := source.ReadGraphParseConcurrency(opts)

	repository := s.repository
	repoRoot, repoInfo, cleanup, err := s.resolveRepository(ctx, repository)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	rootToScan, err := resolveScanRoot(repoRoot, repository.Subdir)
	if err != nil {
		return nil, err
	}
	allowedGoPaths, err := s.allowedGoPaths(repoRoot, rootToScan)
	if err != nil {
		return nil, err
	}

	var data *graph.Data
	if len(allowedGoPaths) > 0 {
		parser, ok := codeast.GetDirectoryParser(codeast.FileTypeGo)
		if !ok {
			return nil, missingReaderError(codeast.FileTypeGo)
		}
		var parseOpts []codeast.ParseOption
		if parseConcurrency > 0 {
			parseOpts = append(parseOpts, codeast.WithParseConcurrency(parseConcurrency))
		}
		result, err := parser.ParseDirectory(rootToScan, parseOpts...)
		if err != nil {
			return nil, err
		}
		data = s.graphDataFromCodeAST(result, repoRoot, repoInfo, allowedGoPaths)
	} else {
		data = &graph.Data{}
	}

	if len(s.docExtensions) > 0 {
		docNodes, err := s.readDocumentNodes(ctx, rootToScan, repoRoot, repoInfo)
		if err != nil {
			return nil, err
		}
		data.Nodes = append(data.Nodes, docNodes...)
	}

	return data, nil
}

func (s *Source) allowedGoPaths(repoRoot, rootToScan string) (map[string]struct{}, error) {
	filePaths, err := s.getFilePaths(rootToScan)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{})
	for _, filePath := range filePaths {
		if filepath.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") {
			continue
		}
		relPath, err := filepath.Rel(repoRoot, filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to build repo-relative path: %w", err)
		}
		allowed[filepath.ToSlash(relPath)] = struct{}{}
	}
	return allowed, nil
}

func (s *Source) graphDataFromCodeAST(
	result *codeast.Result,
	repoRoot string,
	info *repoInfo,
	allowedGoPaths map[string]struct{},
) *graph.Data {
	if result == nil {
		return &graph.Data{}
	}
	baseMetadata := s.buildBaseMetadata(repoRoot, info)
	namespace := repoGraphNamespace(info)
	nodeMap := make(map[string]*graph.Node)
	edgeMap := make(map[string]*graph.Edge)
	keptSymbols := make(map[string]struct{})
	symbolIDs := make(map[string]string)
	symbolSourceIDs := make(map[string]string)

	for _, astNode := range result.Nodes {
		if astNode == nil || astNode.ID == "" {
			continue
		}
		relPath := toRelativeRepoPath(repoRoot, astNode.FilePath)
		if _, ok := allowedGoPaths[relPath]; !ok {
			continue
		}
		sourceID := repoGraphSourceID(namespace, "symbol", astNode.ID)
		nodeID := generatedGraphID("node", sourceID)
		keptSymbols[astNode.ID] = struct{}{}
		symbolIDs[astNode.ID] = nodeID
		symbolSourceIDs[astNode.ID] = sourceID
		nodeMap[nodeID] = graphNodeFromCodeAST(astNode, nodeID, sourceID, relPath, baseMetadata)
	}

	for _, astEdge := range result.Edges {
		if astEdge == nil || astEdge.FromID == "" || astEdge.ToID == "" || astEdge.Type == "" {
			continue
		}
		if _, ok := keptSymbols[astEdge.FromID]; !ok {
			continue
		}
		if _, ok := keptSymbols[astEdge.ToID]; !ok {
			continue
		}
		fromID := symbolIDs[astEdge.FromID]
		toID := symbolIDs[astEdge.ToID]
		edgeSourceID := repoGraphEdgeKey(symbolSourceIDs[astEdge.FromID], string(astEdge.Type), symbolSourceIDs[astEdge.ToID])
		edgeID := generatedGraphID("edge", edgeSourceID)
		edgeMap[edgeID] = &graph.Edge{
			ID:       edgeID,
			FromID:   fromID,
			ToID:     toID,
			Type:     string(astEdge.Type),
			Metadata: cloneAnyMap(astEdge.Metadata),
		}
		if edgeMap[edgeID].Metadata == nil {
			edgeMap[edgeID].Metadata = make(map[string]any)
		}
		edgeMap[edgeID].Metadata[source.MetaSourceID] = edgeSourceID
		edgeMap[edgeID].Metadata["builder"] = "repo_graph_source"
	}

	nodes := make([]*graph.Node, 0, len(nodeMap))
	for _, node := range nodeMap {
		nodes = append(nodes, node)
	}
	edges := make([]*graph.Edge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		edges = append(edges, edge)
	}
	return &graph.Data{Nodes: nodes, Edges: edges}
}

func graphNodeFromCodeAST(
	astNode *codeast.Node,
	nodeID string,
	sourceID string,
	relPath string,
	baseMetadata map[string]any,
) *graph.Node {
	metadata := cloneAnyMap(baseMetadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["kind"] = "code"
	metadata[source.MetaSourceID] = sourceID
	metadata[codeast.TrpcAstMetaPrefix+"type"] = string(astNode.Type)
	metadata[codeast.TrpcAstMetaPrefix+"name"] = astNode.Name
	metadata[codeast.TrpcAstMetaPrefix+"full_name"] = astNode.FullName
	metadata[codeast.TrpcAstMetaPrefix+"language"] = string(astNode.Language)
	metadata[codeast.TrpcAstMetaPrefix+"scope"] = string(astNode.Scope)
	metadata[codeast.TrpcAstMetaPrefix+"file_path"] = relPath
	metadata[codeast.TrpcAstMetaPrefix+"line_start"] = astNode.LineStart
	metadata[codeast.TrpcAstMetaPrefix+"line_end"] = astNode.LineEnd
	metadata[codeast.TrpcAstMetaPrefix+"signature"] = astNode.Signature
	if astNode.Package != "" {
		metadata[codeast.TrpcAstMetaPrefix+"package"] = astNode.Package
	}
	if astNode.Comment != "" {
		metadata[codeast.TrpcAstMetaPrefix+"comment"] = astNode.Comment
	}
	for k, v := range astNode.Metadata {
		metadata[codeast.TrpcAstMetaPrefix+k] = v
	}
	metadata[source.MetaFilePath] = relPath
	metadata[source.MetaChunkIndex] = astNode.ChunkIndex
	metadata[source.MetaContentLength] = len([]rune(astNode.Code))
	delete(metadata, source.MetaRepoURL)
	delete(metadata, source.MetaRepoPath)
	delete(metadata, codeast.TrpcAstMetaPrefix+"go_type_kind")

	return &graph.Node{
		ID:       nodeID,
		Name:     astNode.Name,
		Content:  astNode.Code,
		Metadata: metadata,
	}
}

func repoGraphNamespace(info *repoInfo) string {
	if info == nil {
		return "repo:unknown#default"
	}
	repoKey := firstNonEmpty(info.url, info.name, "unknown")
	targetKind := info.targetKind
	if targetKind == "" {
		targetKind = checkoutTargetDefault
	}
	revision := string(targetKind)
	if info.branch != "" {
		revision += ":" + info.branch
	}
	return "repo:" + repoKey + "#" + revision
}

func repoGraphSourceID(namespace, kind, value string) string {
	if value == "" {
		return namespace + "#" + kind
	}
	return namespace + "#" + kind + ":" + value
}

func generatedGraphID(kind, sourceID string) string {
	// #nosec G401 -- this is a deterministic graph ID, not a security hash.
	sum := sha1.Sum([]byte(sourceID))
	return kind + ":" + hex.EncodeToString(sum[:12])
}

func repoGraphEdgeKey(fromID, edgeType, toID string) string {
	return fromID + "::" + edgeType + "::" + toID
}

func cloneAnyMap(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for k, v := range metadata {
		cloned[k] = v
	}
	return cloned
}

// readDocumentNodes scans the directory for files matching s.docExtensions and
// converts each document chunk into a graph.Node with kind="document".
func (s *Source) readDocumentNodes(
	ctx context.Context,
	rootToScan string,
	repoRoot string,
	info *repoInfo,
) ([]*graph.Node, error) {
	filePaths, err := s.getFilePaths(rootToScan)
	if err != nil {
		return nil, err
	}
	namespace := repoGraphNamespace(info)
	baseMetadata := s.buildBaseMetadata(repoRoot, info)
	var nodes []*graph.Node
	for _, filePath := range filePaths {
		ext := strings.ToLower(filepath.Ext(filePath))
		if !slices.Contains(s.docExtensions, ext) {
			continue
		}
		r, ok := docreader.GetReader(ext)
		if !ok {
			continue
		}
		fileReader, ok := r.(interface {
			ReadFromFile(string) ([]*document.Document, error)
		})
		if !ok {
			continue
		}
		docs, err := fileReader.ReadFromFile(filePath)
		if err != nil {
			continue
		}
		relPath := toRelativeRepoPath(repoRoot, filePath)
		for i, doc := range docs {
			if doc == nil {
				continue
			}
			chunkIndex := i
			if doc.Metadata != nil {
				if v, ok := doc.Metadata[source.MetaChunkIndex]; ok {
					if n, ok := v.(int); ok {
						chunkIndex = n
					}
				}
			}
			sourceID := repoGraphSourceID(namespace, "doc", fmt.Sprintf("%s#%d", relPath, chunkIndex))
			nodeID := generatedGraphID("node", sourceID)
			nodes = append(nodes, graphNodeFromDocumentChunk(doc, nodeID, sourceID, relPath, chunkIndex, baseMetadata))
		}
	}
	_ = ctx
	return nodes, nil
}

func graphNodeFromDocumentChunk(
	doc *document.Document,
	nodeID string,
	sourceID string,
	relPath string,
	chunkIndex int,
	baseMetadata map[string]any,
) *graph.Node {
	metadata := cloneAnyMap(baseMetadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["kind"] = "document"
	metadata[source.MetaSourceID] = sourceID
	metadata[source.MetaFilePath] = relPath
	metadata[source.MetaChunkIndex] = chunkIndex
	metadata[source.MetaContentLength] = len([]rune(doc.Content))
	if doc.Metadata != nil {
		for k, v := range doc.Metadata {
			if k == source.MetaRepoURL || k == source.MetaRepoPath {
				continue
			}
			metadata[k] = v
		}
	}
	name := doc.Name
	if name == "" {
		name = relPath
	}
	return &graph.Node{
		ID:       nodeID,
		Name:     name,
		Content:  doc.Content,
		Metadata: metadata,
	}
}
