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
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// codeLanguageSpec describes one programming language for graph source parsing.
type codeLanguageSpec struct {
	fileType   string
	ext        string
	skipSuffix string
	skipPrefix string
	skipNames  []string
}

// supportedCodeLanguages lists all languages that ReadGraph can parse when a
// DirectoryParser is registered and matching files exist in the repository.
var supportedCodeLanguages = []codeLanguageSpec{
	{fileType: codeast.FileTypeGo, ext: ".go", skipSuffix: "_test.go"},
	{fileType: codeast.FileTypePython, ext: ".py", skipPrefix: "test_", skipNames: []string{"conftest.py", "setup.py"}},
}

// ReadGraph reads repository code as graph-native data.
func (s *Source) ReadGraph(ctx context.Context, opts ...source.ReadGraphOption) (*graph.Data, error) {
	parseConcurrency := source.ReadGraphParseConcurrency(opts)
	if parseConcurrency <= 0 {
		parseConcurrency = s.parseConcurrency
	}

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

	data := &graph.Data{}

	for _, lang := range supportedCodeLanguages {
		allowed, err := s.allowedCodePaths(repoRoot, rootToScan, lang)
		if err != nil {
			return nil, err
		}
		if len(allowed) == 0 {
			continue
		}
		parser, ok := codeast.GetDirectoryParser(lang.fileType)
		if !ok {
			if lang.fileType == codeast.FileTypePython {
				slog.Warn("repo graph parser is not registered; skipping language", "file_type", lang.fileType, "files", len(allowed))
				continue
			}
			return nil, missingReaderError(lang.fileType)
		}
		var parseOpts []codeast.ParseOption
		if parseConcurrency > 0 {
			parseOpts = append(parseOpts, codeast.WithParseConcurrency(parseConcurrency))
		}
		parseOpts = append(parseOpts, codeast.WithParseIncludeFiles(absoluteAllowedPaths(repoRoot, allowed)))
		result, err := parser.ParseDirectory(rootToScan, parseOpts...)
		if err != nil {
			return nil, err
		}
		langData := s.graphDataFromCodeAST(result, repoRoot, repoInfo, allowed)
		data.Nodes = append(data.Nodes, langData.Nodes...)
		data.Edges = append(data.Edges, langData.Edges...)
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

// allowedGoPaths returns the Go-specific allowed paths for backward compatibility.
func (s *Source) allowedGoPaths(repoRoot, rootToScan string) (map[string]struct{}, error) {
	return s.allowedCodePaths(repoRoot, rootToScan, supportedCodeLanguages[0])
}

func (s *Source) allowedCodePaths(repoRoot, rootToScan string, lang codeLanguageSpec) (map[string]struct{}, error) {
	filePaths, err := s.getFilePaths(rootToScan)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{})
	for _, filePath := range filePaths {
		if filepath.Ext(filePath) != lang.ext {
			continue
		}
		name := filepath.Base(filePath)
		if lang.skipSuffix != "" && strings.HasSuffix(name, lang.skipSuffix) {
			continue
		}
		if lang.skipPrefix != "" && strings.HasPrefix(name, lang.skipPrefix) {
			continue
		}
		if slices.Contains(lang.skipNames, name) {
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

func absoluteAllowedPaths(repoRoot string, allowed map[string]struct{}) []string {
	if len(allowed) == 0 {
		return nil
	}
	paths := make([]string, 0, len(allowed))
	for relPath := range allowed {
		if relPath == "" {
			continue
		}
		paths = append(paths, filepath.Join(repoRoot, filepath.FromSlash(relPath)))
	}
	slices.Sort(paths)
	return paths
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
	symbolNodeKeys := make(map[string]string)

	for _, astNode := range result.Nodes {
		if astNode == nil || astNode.ID == "" {
			continue
		}
		relPath := toRelativeRepoPath(repoRoot, astNode.FilePath)
		if _, ok := allowedGoPaths[relPath]; !ok {
			continue
		}
		nodeKey := repoGraphNodeKey(namespace, "symbol", astNode.ID)
		nodeID := generatedGraphID("node", nodeKey)
		keptSymbols[astNode.ID] = struct{}{}
		symbolIDs[astNode.ID] = nodeID
		symbolNodeKeys[astNode.ID] = nodeKey
		nodeMap[nodeID] = graphNodeFromCodeAST(astNode, nodeID, relPath, baseMetadata)
	}

	// Build indexes for fuzzy symbol resolution (handles Python re-export paths).
	shortNameIndex := buildShortNameIndex(keptSymbols)

	for _, astEdge := range result.Edges {
		if astEdge == nil || astEdge.FromID == "" || astEdge.ToID == "" || astEdge.Type == "" {
			continue
		}
		resolvedFrom := resolveSymbolID(astEdge.FromID, keptSymbols, shortNameIndex)
		if resolvedFrom == "" {
			continue
		}
		resolvedTo := resolveSymbolID(astEdge.ToID, keptSymbols, shortNameIndex)
		if resolvedTo == "" {
			continue
		}
		fromID := symbolIDs[resolvedFrom]
		toID := symbolIDs[resolvedTo]
		edgeKey := repoGraphEdgeKey(symbolNodeKeys[resolvedFrom], string(astEdge.Type), symbolNodeKeys[resolvedTo])
		edgeID := generatedGraphID("edge", edgeKey)
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
	relPath string,
	baseMetadata map[string]any,
) *graph.Node {
	metadata := cloneAnyMap(baseMetadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
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

func repoGraphNodeKey(namespace, kind, value string) string {
	if value == "" {
		return namespace + "#" + kind
	}
	return namespace + "#" + kind + ":" + value
}

func generatedGraphID(kind, key string) string {
	// #nosec G401 -- this is a deterministic graph ID, not a security hash.
	sum := sha1.Sum([]byte(key))
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
// converts each document chunk into a document-scoped graph.Node.
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
	var readErrs []error
	for _, filePath := range filePaths {
		if err := ctx.Err(); err != nil {
			return nodes, err
		}
		ext := strings.ToLower(filepath.Ext(filePath))
		if !slices.Contains(s.docExtensions, ext) {
			continue
		}
		fileType := docExtensionType(ext)
		r, ok := s.readers[fileType]
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
			readErrs = append(readErrs, fmt.Errorf("read %s: %w", filePath, err))
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
			nodeKey := repoGraphNodeKey(namespace, "doc", fmt.Sprintf("%s#%d", relPath, chunkIndex))
			nodeID := generatedGraphID("node", nodeKey)
			nodes = append(nodes, graphNodeFromDocumentChunk(doc, nodeID, relPath, chunkIndex, baseMetadata))
		}
	}
	if len(readErrs) > 0 {
		return nodes, fmt.Errorf("readDocumentNodes: %d file(s) failed, first: %w", len(readErrs), readErrs[0])
	}
	return nodes, nil
}

func docExtensionType(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	switch ext {
	case "txt", "text":
		return "text"
	case "md", "markdown":
		return "markdown"
	case "json":
		return "json"
	case "csv":
		return "csv"
	case "pdf":
		return "pdf"
	case "docx", "doc":
		return "docx"
	default:
		return ext
	}
}

func graphNodeFromDocumentChunk(
	doc *document.Document,
	nodeID string,
	relPath string,
	chunkIndex int,
	baseMetadata map[string]any,
) *graph.Node {
	metadata := cloneAnyMap(baseMetadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
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

// buildShortNameIndex creates a mapping from the last segment of a symbol ID
// (e.g. "AgentABC" from "pkg.mod.AgentABC") to all matching full IDs.
func buildShortNameIndex(keptSymbols map[string]struct{}) map[string][]string {
	index := make(map[string][]string, len(keptSymbols))
	for id := range keptSymbols {
		short := symbolShortName(id)
		if short != "" {
			index[short] = append(index[short], id)
		}
	}
	return index
}

// resolveSymbolID attempts to find the canonical symbol ID for a given raw ID.
// It first tries exact match, then falls back to prefix-based short-name matching.
// Fuzzy matching requires both a short-name hit AND a package-prefix relationship,
// preventing false-positive edges from unrelated symbols that share a name.
func resolveSymbolID(rawID string, keptSymbols map[string]struct{}, shortNameIndex map[string][]string) string {
	if _, ok := keptSymbols[rawID]; ok {
		return rawID
	}

	// Require path context for fuzzy matching. A bare short name like "BaseModel"
	// (no dot) has no package information to anchor the match and could incorrectly
	// resolve to an unrelated symbol that happens to share the same name.
	if !strings.Contains(rawID, ".") {
		return ""
	}

	short := symbolShortName(rawID)
	if short == "" {
		return ""
	}
	candidates := shortNameIndex[short]
	if len(candidates) == 0 {
		return ""
	}

	// Require prefix relationship: the rawID's package prefix must be a prefix
	// of the candidate's full path. This handles Python re-export paths where
	// e.g. "pkg.abc.AgentABC" should match "pkg.abc._agent.AgentABC", but rejects
	// "external.Client" -> "pkg.Client" where packages are unrelated.
	prefix := symbolPrefix(rawID)
	if prefix == "" {
		return ""
	}
	var matched string
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix+".") {
			if matched != "" {
				return "" // ambiguous
			}
			matched = c
		}
	}
	return matched
}

func symbolShortName(id string) string {
	if idx := strings.LastIndex(id, "."); idx >= 0 && idx < len(id)-1 {
		return id[idx+1:]
	}
	return id
}

func symbolPrefix(id string) string {
	if idx := strings.LastIndex(id, "."); idx > 0 {
		return id[:idx]
	}
	return ""
}
