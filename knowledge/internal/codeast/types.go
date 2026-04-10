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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// EntityType defines the type of code entity.
type EntityType string

const (
	EntityFunction  EntityType = "Function"
	EntityMethod    EntityType = "Method"
	EntityStruct    EntityType = "Struct"
	EntityInterface EntityType = "Interface"
	EntityVariable  EntityType = "Variable"
	EntityAlias     EntityType = "Alias"
	EntityPackage   EntityType = "Package"
	// Class-based languages (Python, C++, Java, etc.)
	EntityClass EntityType = "Class"

	// Python specific
	EntityModule EntityType = "Module"

	// C++ specific
	EntityNamespace EntityType = "Namespace"
	EntityTemplate  EntityType = "Template"
	EntityEnum      EntityType = "Enum"

	// Proto specific
	EntityService EntityType = "Service"
	EntityRPC     EntityType = "RPC"
	EntityMessage EntityType = "Message"

	// Document type for file parser (markdown, pdf, txt, etc.)
	EntityDocument EntityType = "Document"
)

// RelationType defines the relationship between entities.
type RelationType string

const (
	RelationCalls      RelationType = "CALLS"
	RelationMethod     RelationType = "METHOD"
	RelationField      RelationType = "FIELD"
	RelationImplements RelationType = "IMPLEMENTS"
	RelationParam      RelationType = "PARAM"
	RelationReturns    RelationType = "RETURNS"
	RelationAliasOf    RelationType = "ALIAS_OF"
	RelationTyped      RelationType = "TYPE"
	// Python specific
	RelationImports  RelationType = "IMPORTS"
	RelationInherits RelationType = "INHERITS"
	// C++ specific
	RelationContains RelationType = "CONTAINS"
)

// Metadata keys.
const (
	MetadataKeyExported       string = "exported"
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
	ScopeCode     Scope = "code"
	ScopeDocument Scope = "document"
	ScopeExample  Scope = "example"
)

// Language defines the programming language.
type Language string

const (
	LanguageGo         Language = "go"
	LanguageCpp        Language = "cpp"
	LanguagePython     Language = "python"
	LanguageProto      Language = "proto"
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

// TrpcAgentDocument is a document for trpc-agent-go.
type TrpcAgentDocument = document.Document

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
