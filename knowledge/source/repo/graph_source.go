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
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	codegolang "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast/golang"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// ReadGraph reads repository code as graph-native data.
func (s *Source) ReadGraph(ctx context.Context) (*graph.Data, error) {
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
	if len(allowedGoPaths) == 0 {
		return &graph.Data{}, nil
	}

	result, err := codegolang.NewParser().ParseDirectory(rootToScan)
	if err != nil {
		return nil, err
	}
	return s.graphDataFromCodeAST(result, repoRoot, repoInfo, allowedGoPaths), nil
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
