//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeast defines internal AST parsing abstractions shared by code-aware readers.
package codeast

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// EntityType defines the type of code entity.
type EntityType string

const (
	// EntityFunction represents a standalone function declaration.
	EntityFunction EntityType = "Function"
	// EntityMethod represents a method bound to a receiver type.
	EntityMethod EntityType = "Method"
	// EntityStruct represents a struct type declaration.
	EntityStruct EntityType = "Struct"
	// EntityInterface represents an interface type declaration.
	EntityInterface EntityType = "Interface"
	// EntityVariable represents a variable or constant declaration.
	EntityVariable EntityType = "Variable"
	// EntityAlias represents a type alias declaration.
	EntityAlias EntityType = "Alias"
	// EntityPackage represents a package-level grouping node.
	EntityPackage EntityType = "Package"
	// EntityClass represents a class declaration in class-based languages.
	EntityClass EntityType = "Class"
	// EntityModule represents a module declaration, such as in Python.
	EntityModule EntityType = "Module"
	// EntityNamespace represents a namespace declaration, such as in C++.
	EntityNamespace EntityType = "Namespace"
	// EntityTemplate represents a template declaration in C++.
	EntityTemplate EntityType = "Template"
	// EntityEnum represents an enum declaration.
	EntityEnum EntityType = "Enum"
	// EntityService represents a proto service declaration.
	EntityService EntityType = "Service"
	// EntityRPC represents a proto RPC declaration.
	EntityRPC EntityType = "RPC"
	// EntityMessage represents a proto message declaration.
	EntityMessage EntityType = "Message"
	// EntityDocument represents a non-code document node.
	EntityDocument EntityType = "Document"
)

// RelationType defines the relationship between entities.
type RelationType string

const (
	// RelationCalls marks a call-site dependency between code entities.
	RelationCalls RelationType = "CALLS"
	// RelationMethod links a receiver type to one of its methods.
	RelationMethod RelationType = "METHOD"
	// RelationField links a composite type to one of its fields.
	RelationField RelationType = "FIELD"
	// RelationImplements marks that a type implements an interface.
	RelationImplements RelationType = "IMPLEMENTS"
	// RelationParam links a callable entity to one of its parameters.
	RelationParam RelationType = "PARAM"
	// RelationReturns links a callable entity to one of its return values.
	RelationReturns RelationType = "RETURNS"
	// RelationAliasOf links a type alias to its target type.
	RelationAliasOf RelationType = "ALIAS_OF"
	// RelationTyped links a declaration to its referenced type.
	RelationTyped RelationType = "TYPE"
	// RelationImports marks an import dependency between files or modules.
	RelationImports RelationType = "IMPORTS"
	// RelationInherits marks an inheritance relationship.
	RelationInherits RelationType = "INHERITS"
	// RelationContains marks a namespace-style containment relationship.
	RelationContains RelationType = "CONTAINS"
)

// MetadataKeyCodeChunkIndex stores the code chunk index in node metadata.
// MetadataKeyReceiverType stores the receiver type for method nodes.
// MetadataKeyScope stores the retrieval scope label.
// MetadataKeyLanguage stores the language label.
const (
	MetadataKeyCodeChunkIndex string = "code_chunk_index"
	MetadataKeyReceiverType   string = "receiver_type"
	MetadataKeyScope          string = "scope"
	MetadataKeyLanguage       string = "language"
)

// TrpcAstMetaPrefix is the prefix for all metadata keys written by AST readers.
const TrpcAstMetaPrefix string = "trpc_ast_"

// Scope defines the search scope category.
type Scope string

const (
	// ScopeCode marks code entities intended for code-aware retrieval.
	ScopeCode Scope = "code"
	// ScopeDocument marks document-style content intended for document retrieval.
	ScopeDocument Scope = "document"
	// ScopeExample marks example or tutorial-style content.
	ScopeExample Scope = "example"
)

// Language defines the programming language.
type Language string

const (
	// LanguageGo identifies Go source code.
	LanguageGo Language = "go"
	// LanguageCpp identifies C++ source code.
	LanguageCpp Language = "cpp"
	// LanguagePython identifies Python source code.
	LanguagePython Language = "python"
	// LanguageProto identifies Protocol Buffers source code.
	LanguageProto Language = "proto"
	// LanguageJavascript identifies JavaScript source code.
	LanguageJavascript Language = "javascript"
)

// Node represents a code entity in the graph.
type Node struct {
	ID       string     `json:"id"`
	Type     EntityType `json:"type"`
	Name     string     `json:"name"`
	FullName string     `json:"full_name"`

	Scope    Scope    `json:"scope"`
	Language Language `json:"language"`

	Signature string `json:"signature"`
	Comment   string `json:"comment"`
	Code      string `json:"code"`

	FilePath   string `json:"file_path"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	ChunkIndex int    `json:"chunk_index"`

	RepoURL  string `json:"repo_url"`
	RepoName string `json:"repo_name"`
	Branch   string `json:"branch"`

	Package         string   `json:"package"`
	Namespace       string   `json:"namespace,omitempty"`
	UsingNamespaces []string `json:"using_namespaces,omitempty"`
	Imports         []string `json:"imports,omitempty"`

	Metadata map[string]any `json:"metadata"`

	Embedding     []float64 `json:"embedding,omitempty"`
	SparseIndices []int32   `json:"sparse_indices,omitempty"`
	SparseValues  []float32 `json:"sparse_values,omitempty"`
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	FromID   string         `json:"from_id"`
	ToID     string         `json:"to_id"`
	Type     RelationType   `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// FileInfo contains file-level parse metadata produced alongside AST results.
type FileInfo struct {
	Name     string
	Language Language
	Package  string
	Imports  []string
	Metadata map[string]any
}

// Result contains the full parse result for a source file or code unit.
type Result struct {
	File  *FileInfo
	Nodes []*Node
	Edges []*Edge
}

// DocumentPayload is a document-ready payload derived from AST parsing.
type DocumentPayload struct {
	Name          string
	Content       string
	Metadata      map[string]any
	EmbeddingText string
}

// NodeDocumentPayloadOptions configures how AST nodes are mapped to document payloads.
type NodeDocumentPayloadOptions struct {
	BaseMetadata       map[string]any
	FileInfo           *FileInfo
	FormatType         func(EntityType) string
	BuildEmbeddingText func(*Node) string
}

// NodesToDocumentPayloads converts a parse result into document payloads.
func NodesToDocumentPayloads(result *Result, opts NodeDocumentPayloadOptions) []*DocumentPayload {
	if result == nil {
		return nil
	}

	payloads := make([]*DocumentPayload, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		payload := NodeToDocumentPayload(node, opts)
		if payload != nil {
			payloads = append(payloads, payload)
		}
	}
	return payloads
}

// NodeToDocumentPayload converts a single AST node into a document payload.
func NodeToDocumentPayload(node *Node, opts NodeDocumentPayloadOptions) *DocumentPayload {
	if node == nil {
		return nil
	}

	metadata := make(map[string]any)
	for k, v := range opts.BaseMetadata {
		metadata[k] = v
	}

	typeValue := string(node.Type)
	if opts.FormatType != nil {
		typeValue = opts.FormatType(node.Type)
	}

	metadata[TrpcAstMetaPrefix+"type"] = typeValue
	metadata[TrpcAstMetaPrefix+"name"] = node.Name
	metadata[TrpcAstMetaPrefix+"full_name"] = node.FullName
	metadata[TrpcAstMetaPrefix+"language"] = string(node.Language)
	metadata[TrpcAstMetaPrefix+"scope"] = string(node.Scope)
	if node.Package != "" {
		metadata[TrpcAstMetaPrefix+"package"] = node.Package
	}
	if node.FilePath != "" {
		metadata[TrpcAstMetaPrefix+"file_path"] = node.FilePath
	}
	if node.LineStart > 0 {
		metadata[TrpcAstMetaPrefix+"line_start"] = node.LineStart
	}
	if node.LineEnd > 0 {
		metadata[TrpcAstMetaPrefix+"line_end"] = node.LineEnd
	}
	if node.Signature != "" {
		metadata[TrpcAstMetaPrefix+"signature"] = node.Signature
	}
	if node.Comment != "" {
		metadata[TrpcAstMetaPrefix+"comment"] = strings.TrimSpace(node.Comment)
	}

	imports := node.Imports
	if len(imports) == 0 && opts.FileInfo != nil {
		imports = opts.FileInfo.Imports
	}
	if len(imports) > 0 {
		metadata[TrpcAstMetaPrefix+"imports"] = append([]string(nil), imports...)
		metadata[TrpcAstMetaPrefix+"import_count"] = len(imports)
	}

	for k, v := range node.Metadata {
		metadata[TrpcAstMetaPrefix+k] = v
	}

	metadata["trpc_agent_go_chunk_index"] = node.ChunkIndex
	metadata["trpc_agent_go_chunk_size"] = utf8.RuneCountInString(node.Code)
	metadata["trpc_agent_go_content_length"] = utf8.RuneCountInString(node.Code)

	payload := &DocumentPayload{
		Name:     node.Name,
		Content:  node.Code,
		Metadata: metadata,
	}
	if opts.BuildEmbeddingText != nil {
		payload.EmbeddingText = opts.BuildEmbeddingText(node)
	}
	return payload
}

// IsExamplePath checks if a file path is under an example directory within a repository.
func IsExamplePath(filePath string, basePath string) bool {
	checkPath := filePath
	if basePath != "" {
		relPath, err := filepath.Rel(basePath, filePath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			checkPath = relPath
		}
	}

	parts := splitPath(checkPath)
	for _, part := range parts {
		lower := strings.ToLower(part)
		if lower == "example" || lower == "examples" {
			return true
		}
	}
	return false
}

// splitPath splits a file path into its components.
func splitPath(path string) []string {
	var parts []string
	for path != "" {
		dir, file := filepath.Split(path)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		if dir == path {
			break
		}
		path = strings.TrimSuffix(dir, string(filepath.Separator))
	}
	return parts
}
