//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package proto provides internal proto AST parsing logic for knowledge documents.
package proto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	parserpkg "github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"google.golang.org/protobuf/types/descriptorpb"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

// Parser parses proto files into internal codeast results.
type Parser struct {
	extractor codeast.Extractor[*extractInput]
	analyzer  codeast.Analyzer[*analyzeInput]
}

// NewParser creates a new proto parser.
func NewParser() *Parser {
	return &Parser{
		extractor: newDefaultExtractor(),
		analyzer:  newDefaultAnalyzer(),
	}
}

// ParseContent parses a proto file content and extracts all AST entities.
func (p *Parser) ParseContent(name, content string) (*codeast.Result, error) {
	handler := reporter.NewHandler(nil)
	fileNode, err := parserpkg.Parse(name, bytes.NewReader([]byte(content)), handler)
	if err != nil {
		return &codeast.Result{File: &codeast.FileInfo{Name: name, Language: codeast.LanguageProto}}, fmt.Errorf("failed to parse proto file: %w", err)
	}

	result, err := parserpkg.ResultFromAST(fileNode, false, handler)
	if err != nil {
		return &codeast.Result{File: &codeast.FileInfo{Name: name, Language: codeast.LanguageProto}}, fmt.Errorf("failed to build descriptor from AST: %w", err)
	}

	fd := result.FileDescriptorProto()
	protoPackage := fd.GetPackage()
	syntax := fd.GetSyntax()
	if syntax == "" {
		syntax = "proto2"
	}
	imports := fd.GetDependency()
	goPackage, javaPackage := ExtractFileOptions(fd)
	lines := strings.Split(content, "\n")

	nodes, err := p.extractor.Extract(&extractInput{
		fileNode:     fileNode,
		result:       result,
		fileName:     name,
		protoPackage: protoPackage,
		syntax:       syntax,
		goPackage:    goPackage,
		javaPackage:  javaPackage,
		imports:      imports,
		lines:        lines,
	})
	if err != nil {
		return nil, err
	}

	fileInfo := &codeast.FileInfo{
		Name:     name,
		Language: codeast.LanguageProto,
		Package:  protoPackage,
		Imports:  append([]string(nil), imports...),
		Metadata: map[string]any{
			"syntax": syntax,
		},
	}
	if len(imports) > 0 {
		fileInfo.Metadata["import_count"] = len(imports)
	}
	if goPackage != "" {
		fileInfo.Metadata["go_package"] = goPackage
	}
	if javaPackage != "" {
		fileInfo.Metadata["java_package"] = javaPackage
	}

	edges, err := p.analyzer.Analyze(&analyzeInput{}, nil)
	if err != nil {
		return nil, err
	}
	return &codeast.Result{File: fileInfo, Nodes: nodes, Edges: edges}, nil
}

// BuildRPCSignature builds a human-readable RPC signature.
func BuildRPCSignature(name, inputType, outputType string, clientStreaming, serverStreaming bool) string {
	var sig strings.Builder
	sig.WriteString("rpc ")
	sig.WriteString(name)
	sig.WriteString("(")
	if clientStreaming {
		sig.WriteString("stream ")
	}
	sig.WriteString(ShortTypeName(inputType))
	sig.WriteString(") returns (")
	if serverStreaming {
		sig.WriteString("stream ")
	}
	sig.WriteString(ShortTypeName(outputType))
	sig.WriteString(")")
	return sig.String()
}

// QualifiedName returns the fully qualified name with proto package prefix.
func QualifiedName(pkg, name string) string {
	if pkg != "" {
		return pkg + "." + name
	}
	return name
}

// QualifiedNameWithParent returns the fully qualified name with parent scope prefix.
func QualifiedNameWithParent(name, parent string) string {
	if parent != "" {
		return parent + "." + name
	}
	return name
}

// ExtractCode extracts code from source lines by start/end line numbers.
func ExtractCode(lines []string, startLine, endLine int) string {
	if startLine <= 0 || endLine <= 0 || startLine > len(lines) {
		return ""
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	return strings.Join(lines[startLine-1:endLine], "\n")
}

// BuildNodeEmbeddingText builds embedding payload for a proto node.
func BuildNodeEmbeddingText(node *codeast.Node) string {
	if node == nil {
		return ""
	}

	comment := strings.TrimSpace(node.Comment)
	data := map[string]string{
		"id":        node.ID,
		"type":      string(node.Type),
		"name":      node.Name,
		"full_name": node.FullName,
		"package":   node.Package,
		"file_path": node.FilePath,
		"comment":   comment,
	}

	if node.Signature != "" {
		data["signature"] = strings.TrimSpace(node.Signature)
	}
	if node.Code != "" {
		data["code"] = node.Code
	}

	for k, v := range node.Metadata {
		switch value := v.(type) {
		case string:
			data[k] = strings.TrimSpace(value)
		default:
			data[k] = fmt.Sprintf("%v", value)
		}
	}

	jsonBytes, _ := json.Marshal(data)
	return string(jsonBytes)
}

// BuildFileEmbeddingText builds embedding payload for file-level documents.
func BuildFileEmbeddingText(content, fileName string, fileMetadata map[string]any) string {
	data := map[string]string{
		"id":        fileName,
		"type":      "file",
		"name":      fileName,
		"full_name": fileName,
		"package":   "",
		"file_path": fileName,
		"signature": "",
		"comment":   "",
	}

	if fileMetadata != nil {
		if pkg, ok := fileMetadata["package"].(string); ok {
			data["package"] = pkg
		}
		if imports, ok := fileMetadata["imports"].([]string); ok && len(imports) > 0 {
			data["imports"] = strings.Join(imports, ", ")
		}
		for k, v := range fileMetadata {
			switch value := v.(type) {
			case string:
				data[k] = strings.TrimSpace(value)
			case []string:
				data[k] = strings.Join(value, ", ")
			default:
				data[k] = fmt.Sprintf("%v", value)
			}
		}
	}

	data["code"] = content

	jsonBytes, _ := json.Marshal(data)
	return string(jsonBytes)
}

// ExtractFileMetadata parses proto content and returns normalized file-level metadata.
func ExtractFileMetadata(content string) map[string]any {
	handler := reporter.NewHandler(nil)
	fileNode, err := parserpkg.Parse("metadata.proto", strings.NewReader(content), handler)
	if err != nil {
		return map[string]any{}
	}
	result, err := parserpkg.ResultFromAST(fileNode, false, handler)
	if err != nil {
		return map[string]any{}
	}
	return extractFileMetadataFromDescriptor(result.FileDescriptorProto())
}

// ExtractOptionString extracts option string values like go_package/java_package.
func ExtractOptionString(content, optionName string) string {
	re := regexp.MustCompile(`(?m)^\s*option\s+` + regexp.QuoteMeta(optionName) + `\s*=\s*"([^"]+)"\s*;`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// ExtractFileOptions extracts go_package/java_package from descriptor options.
func ExtractFileOptions(fd *descriptorpb.FileDescriptorProto) (goPackage, javaPackage string) {
	opts := fd.GetOptions()
	if opts == nil {
		return "", ""
	}
	for _, uo := range opts.GetUninterpretedOption() {
		parts := uo.GetName()
		if len(parts) != 1 {
			continue
		}
		name := parts[0].GetNamePart()
		strVal := string(uo.GetStringValue())
		switch name {
		case "go_package":
			goPackage = strVal
		case "java_package":
			javaPackage = strVal
		}
	}
	return goPackage, javaPackage
}

// ShortTypeName returns short name from a qualified type name.
func ShortTypeName(typeName string) string {
	typeName = strings.TrimPrefix(typeName, ".")
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		return typeName[idx+1:]
	}
	return typeName
}

func extractFileMetadataFromDescriptor(fd *descriptorpb.FileDescriptorProto) map[string]any {
	fileMetadata := map[string]any{}
	if fd == nil {
		return fileMetadata
	}

	if syntax := fd.GetSyntax(); syntax != "" {
		fileMetadata["syntax"] = syntax
	}
	if pkg := fd.GetPackage(); pkg != "" {
		fileMetadata["package"] = pkg
	}
	if imports := fd.GetDependency(); len(imports) > 0 {
		fileMetadata["imports"] = imports
		fileMetadata["import_count"] = len(imports)
	}
	if goPkg, javaPkg := ExtractFileOptions(fd); goPkg != "" || javaPkg != "" {
		if goPkg != "" {
			fileMetadata["go_package"] = goPkg
		}
		if javaPkg != "" {
			fileMetadata["java_package"] = javaPkg
		}
	}

	services := make([]string, 0, len(fd.GetService()))
	for _, svc := range fd.GetService() {
		services = append(services, svc.GetName())
	}
	if len(services) > 0 {
		fileMetadata["services"] = services
		fileMetadata["service_count"] = len(services)
	}

	return fileMetadata
}
