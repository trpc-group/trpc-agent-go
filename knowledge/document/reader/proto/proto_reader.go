//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package proto provides protocol buffer definition file reader implementation.
// It reads .proto files and extracts AST-based entities (messages, enums, services, RPCs)
// using github.com/bufbuild/protocompile for true AST parsing, similar to trpc-ast-rag's approach.
// Each entity becomes a separate document chunk with accurate line numbers, comments, and code snippets.
package proto

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/bufbuild/protocompile/ast"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"google.golang.org/protobuf/types/descriptorpb"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	itransform "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

var (
	// supportedExtensions defines the file extensions supported by this reader.
	supportedExtensions = []string{".proto"}
)

// init registers the proto reader with the global registry.
func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads protocol buffer definition files and extracts AST-based entities.
type Reader struct {
	chunk        bool
	transformers []transform.Transformer
}

// New creates a new proto reader with the given options.
func New(opts ...reader.Option) reader.Reader {
	// Build config from options
	config := &reader.Config{
		Chunk: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Create reader from config
	return &Reader{
		chunk:        config.Chunk,
		transformers: config.Transformers,
	}
}

// ReadFromReader reads proto content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	// Read content from reader.
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}

	return r.processContent(string(content), name, nil)
}

// ReadFromFile reads a proto file and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	// Validate file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".proto" {
		return nil, fmt.Errorf("unsupported file extension: %s", ext)
	}

	// Open and read file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Read content from file
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	baseMetadata := map[string]any{
		source.MetaSource:        source.TypeFile,
		source.MetaFilePath:      filePath,
		source.MetaFileName:      filepath.Base(filePath),
		source.MetaFileExt:       filepath.Ext(filePath),
		source.MetaFileSize:      fileInfo.Size(),
		source.MetaFileMode:      fileInfo.Mode().String(),
		source.MetaModifiedAt:    fileInfo.ModTime().UTC(),
		source.MetaURI:           (&url.URL{Scheme: "file", Path: absPath}).String(),
		source.MetaSourceName:    r.Name(),
		source.MetaContentLength: utf8.RuneCount(content),
	}

	// Use full file path for document naming (consistent with trpc-ast-rag)
	return r.processContent(string(content), filePath, baseMetadata)
}

// ReadFromURL reads proto content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	// Validate URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Fetch content from URL
	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Extract file name from URL
	fileName := r.extractFileNameFromURL(urlStr)

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read URL content: %w", err)
	}

	return r.processContent(string(content), fileName, nil)
}

// processContent processes proto content and extracts AST-based entities.
// Each entity (message, enum, service, rpc) becomes a separate document.
func (r *Reader) processContent(content, name string, baseMetadata map[string]any) ([]*document.Document, error) {
	// If chunking is disabled, return the entire file as one document
	if !r.chunk {
		doc := r.createFileDocument(content, name, baseMetadata)
		return r.applyTransformers([]*document.Document{doc})
	}

	// Parse proto content using protocompile
	docs, err := r.parseAndExtract(content, name, baseMetadata)
	if err != nil {
		return nil, err
	}

	// If no entities found, return the entire file as one document
	if len(docs) == 0 {
		doc := r.createFileDocument(content, name, baseMetadata)
		return r.applyTransformers([]*document.Document{doc})
	}

	return r.applyTransformers(docs)
}

// parseAndExtract parses proto content using protocompile and extracts all entities.
func (r *Reader) parseAndExtract(content, name string, baseMetadata map[string]any) ([]*document.Document, error) {
	// Parse proto file into AST
	handler := reporter.NewHandler(nil)
	fileNode, err := parser.Parse(name, strings.NewReader(content), handler)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proto file: %w", err)
	}

	// Build descriptor proto from AST (for structured access to services, messages, etc.)
	result, err := parser.ResultFromAST(fileNode, false, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to build descriptor from AST: %w", err)
	}

	fd := result.FileDescriptorProto()

	// Extract file-level metadata
	protoPackage := fd.GetPackage()
	syntax := fd.GetSyntax()
	if syntax == "" {
		syntax = "proto2"
	}
	imports := fd.GetDependency()
	goPackage, javaPackage := extractFileOptions(fd)
	// Split content into lines for extracting source code ranges
	lines := strings.Split(content, "\n")

	// Create extractor
	extractor := &entityExtractor{
		fileNode:     fileNode,
		result:       result,
		fileName:     name,
		baseMetadata: baseMetadata,
		protoPackage: protoPackage,
		syntax:       syntax,
		goPackage:    goPackage,
		javaPackage:  javaPackage,
		imports:      imports,
		lines:        lines,
	}

	return extractor.extract()
}

// entityExtractor holds context for extracting entities from a parsed proto file.
type entityExtractor struct {
	fileNode        *ast.FileNode
	result          parser.Result
	fileName        string
	baseMetadata    map[string]any
	protoPackage    string
	syntax          string
	goPackage       string
	javaPackage     string
	imports         []string
	lines           []string
	allServiceNames []string
}

// extract extracts all entities (messages, enums, services, RPCs) from the parsed proto file.
func (e *entityExtractor) extract() ([]*document.Document, error) {
	var docs []*document.Document
	fd := e.result.FileDescriptorProto()
	chunkIndex := 0

	// Pre-collect service names for file-level metadata
	e.collectAllServiceNames(fd)

	// Extract messages (including nested messages)
	for _, msg := range fd.GetMessageType() {
		msgDocs := e.extractMessage(msg, e.protoPackage, &chunkIndex)
		docs = append(docs, msgDocs...)
	}

	// Extract enums (top-level only, nested enums are extracted within messages)
	for _, enum := range fd.GetEnumType() {
		doc := e.extractEnum(enum, e.protoPackage, &chunkIndex)
		if doc != nil {
			docs = append(docs, doc)
		}
	}

	// Extract services and RPCs
	for _, svc := range fd.GetService() {
		svcDocs := e.extractService(svc, &chunkIndex)
		docs = append(docs, svcDocs...)
	}

	// Add file-level metadata to all documents
	for _, doc := range docs {
		e.addFileMetadata(doc)
	}

	return docs, nil
}

// collectAllServiceNames collects all service names for file-level metadata.
func (e *entityExtractor) collectAllServiceNames(fd *descriptorpb.FileDescriptorProto) {
	// Collect service names
	for _, svc := range fd.GetService() {
		e.allServiceNames = append(e.allServiceNames, svc.GetName())
	}
}

// addFileMetadata adds file-level metadata to a document.
func (e *entityExtractor) addFileMetadata(doc *document.Document) {
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]any)
	}
	for k, v := range e.baseMetadata {
		// Keep chunk-level values computed per entity document.
		// baseMetadata carries file-level content length for ReadFromFile,
		// which should not override entity-level MetaContentLength/MetaChunkSize/MetaChunkIndex.
		if k == source.MetaContentLength || k == source.MetaChunkSize || k == source.MetaChunkIndex {
			continue
		}
		doc.Metadata[k] = v
	}

	// Add file-level metadata (same for all entities in this file)
	if e.syntax != "" {
		doc.Metadata["trpc_ast_syntax"] = e.syntax
	}
	if e.protoPackage != "" {
		doc.Metadata["trpc_ast_package"] = e.protoPackage
	}
	if e.goPackage != "" {
		doc.Metadata["trpc_ast_go_package"] = e.goPackage
	}
	if e.javaPackage != "" {
		doc.Metadata["trpc_ast_java_package"] = e.javaPackage
	}
	if len(e.imports) > 0 {
		doc.Metadata["trpc_ast_imports"] = e.imports
		doc.Metadata["trpc_ast_import_count"] = len(e.imports)
	}

	entityType, _ := doc.Metadata["trpc_ast_type"].(string)
	if entityType != "rpc" {
		if len(e.allServiceNames) > 0 {
			doc.Metadata["trpc_ast_services"] = e.allServiceNames
			doc.Metadata["trpc_ast_service_count"] = len(e.allServiceNames)
		}
	}
}

// extractService extracts a service and its RPC methods as documents.
func (e *entityExtractor) extractService(svc *descriptorpb.ServiceDescriptorProto, chunkIndex *int) []*document.Document {
	var docs []*document.Document
	svcName := svc.GetName()
	svcFullName := e.qualifiedName(svcName)

	// Get source location for the service
	svcASTNode := e.result.ServiceNode(svc)
	startLine, endLine := e.nodeLineRange(svcASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(svcASTNode)

	// Create service document
	svcDoc := e.createEntityDocument(code, svcName, "Service", svcFullName, comment, "", startLine, endLine, chunkIndex)
	svcDoc.Metadata["trpc_ast_type"] = "service"
	svcDoc.Metadata["trpc_ast_name"] = svcName
	svcDoc.Metadata["trpc_ast_full_name"] = svcFullName
	svcDoc.Metadata["trpc_ast_comment"] = comment
	svcDoc.Metadata["trpc_ast_language"] = "proto"
	svcDoc.Metadata["trpc_ast_exported"] = true
	svcDoc.Metadata["trpc_ast_scope"] = "code"

	// Build and store RPC methods list
	var rpcMethods []string
	for _, method := range svc.GetMethod() {
		rpcMethods = append(rpcMethods, method.GetName())
	}
	if len(rpcMethods) > 0 {
		svcDoc.Metadata["trpc_ast_rpc_methods"] = rpcMethods
	}

	docs = append(docs, svcDoc)

	// Extract RPC methods as separate documents
	for _, method := range svc.GetMethod() {
		rpcDoc := e.extractRPC(method, svcFullName, chunkIndex)
		if rpcDoc != nil {
			docs = append(docs, rpcDoc)
		}
	}

	return docs
}

// extractRPC extracts an RPC method as a document.
func (e *entityExtractor) extractRPC(method *descriptorpb.MethodDescriptorProto, svcFullName string, chunkIndex *int) *document.Document {
	rpcName := method.GetName()
	rpcFullName := svcFullName + "." + rpcName

	inputType := method.GetInputType()
	outputType := method.GetOutputType()
	clientStreaming := method.GetClientStreaming()
	serverStreaming := method.GetServerStreaming()

	// Build signature
	sig := e.buildRPCSignature(rpcName, inputType, outputType, clientStreaming, serverStreaming)

	// Get source location
	methodASTNode := e.result.MethodNode(method)
	startLine, endLine := e.nodeLineRange(methodASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(methodASTNode)

	doc := e.createEntityDocument(code, rpcName, "RPC", rpcFullName, comment, sig, startLine, endLine, chunkIndex)
	doc.Metadata["trpc_ast_type"] = "rpc"
	doc.Metadata["trpc_ast_name"] = rpcName
	doc.Metadata["trpc_ast_full_name"] = rpcFullName
	doc.Metadata["trpc_ast_comment"] = comment
	doc.Metadata["trpc_ast_language"] = "proto"
	doc.Metadata["trpc_ast_exported"] = true
	doc.Metadata["trpc_ast_scope"] = "code"
	doc.Metadata["trpc_ast_signature"] = sig
	doc.Metadata["trpc_ast_service"] = svcFullName
	doc.Metadata["trpc_ast_input_type"] = inputType
	doc.Metadata["trpc_ast_output_type"] = outputType
	doc.Metadata["trpc_ast_client_streaming"] = clientStreaming
	doc.Metadata["trpc_ast_server_streaming"] = serverStreaming

	return doc
}

// extractMessage extracts a message and its nested types as documents.
func (e *entityExtractor) extractMessage(msg *descriptorpb.DescriptorProto, parentPkg string, chunkIndex *int) []*document.Document {
	// Skip map entry synthetic messages
	if msg.GetOptions() != nil && msg.GetOptions().GetMapEntry() {
		return nil
	}

	var docs []*document.Document
	msgName := msg.GetName()
	msgFullName := e.qualifiedNameWithParent(msgName, parentPkg)

	// Get source location
	msgASTNode := e.result.MessageNode(msg)
	startLine, endLine := e.nodeLineRange(msgASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(msgASTNode)

	// Create message document
	doc := e.createEntityDocument(code, msgName, "Message", msgFullName, comment, "", startLine, endLine, chunkIndex)
	doc.Metadata["trpc_ast_type"] = "message"
	doc.Metadata["trpc_ast_name"] = msgName
	doc.Metadata["trpc_ast_full_name"] = msgFullName
	doc.Metadata["trpc_ast_comment"] = comment
	doc.Metadata["trpc_ast_language"] = "proto"
	doc.Metadata["trpc_ast_exported"] = true
	doc.Metadata["trpc_ast_scope"] = "code"

	docs = append(docs, doc)

	// Extract nested messages
	for _, nestedMsg := range msg.GetNestedType() {
		nestedDocs := e.extractMessage(nestedMsg, msgFullName, chunkIndex)
		docs = append(docs, nestedDocs...)
	}

	// Extract nested enums
	for _, nestedEnum := range msg.GetEnumType() {
		enumDoc := e.extractEnum(nestedEnum, msgFullName, chunkIndex)
		if enumDoc != nil {
			docs = append(docs, enumDoc)
		}
	}

	return docs
}

// extractEnum extracts an enum definition as a document.
func (e *entityExtractor) extractEnum(enum *descriptorpb.EnumDescriptorProto, parentPkg string, chunkIndex *int) *document.Document {
	enumName := enum.GetName()
	enumFullName := e.qualifiedNameWithParent(enumName, parentPkg)

	// Get source location
	enumASTNode := e.result.EnumNode(enum)
	startLine, endLine := e.nodeLineRange(enumASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(enumASTNode)

	// Extract enum values
	var enumValues []string
	for _, value := range enum.GetValue() {
		enumValues = append(enumValues, value.GetName())
	}

	doc := e.createEntityDocument(code, enumName, "Enum", enumFullName, comment, "", startLine, endLine, chunkIndex)
	doc.Metadata["trpc_ast_type"] = "enum"
	doc.Metadata["trpc_ast_name"] = enumName
	doc.Metadata["trpc_ast_full_name"] = enumFullName
	doc.Metadata["trpc_ast_comment"] = comment
	doc.Metadata["trpc_ast_language"] = "proto"
	doc.Metadata["trpc_ast_exported"] = true
	doc.Metadata["trpc_ast_scope"] = "code"
	if len(enumValues) > 0 {
		doc.Metadata["trpc_ast_enum_values"] = enumValues
	}

	return doc
}

// createEntityDocument creates a document for a proto entity with full metadata.
func (e *entityExtractor) createEntityDocument(code, name, entityType, fullName, comment, signature string, startLine, endLine int, chunkIndex *int) *document.Document {
	doc := idocument.CreateDocument(code, e.fileName)

	// Initialize metadata
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]any)
	}
	for k, v := range e.baseMetadata {
		doc.Metadata[k] = v
	}

	// Set entity-specific metadata
	doc.Metadata["trpc_ast_package"] = e.protoPackage
	doc.Metadata["trpc_ast_syntax"] = e.syntax
	if e.goPackage != "" {
		doc.Metadata["trpc_ast_go_package"] = e.goPackage
	}
	if e.javaPackage != "" {
		doc.Metadata["trpc_ast_java_package"] = e.javaPackage
	}
	if len(e.imports) > 0 {
		doc.Metadata["trpc_ast_imports"] = e.imports
		doc.Metadata["trpc_ast_import_count"] = len(e.imports)
	}
	doc.Metadata["trpc_ast_line_start"] = startLine
	doc.Metadata["trpc_ast_line_end"] = endLine
	doc.Metadata[source.MetaChunkIndex] = *chunkIndex
	doc.Metadata[source.MetaChunkSize] = utf8.RuneCountInString(code)
	doc.Metadata[source.MetaContentLength] = utf8.RuneCountInString(code)

	// Build embedding text with metadata JSON.
	// For RPC entities, include the human-readable signature in embedding payload.
	doc.EmbeddingText = e.buildEmbeddingText(code, name, entityType, fullName, comment, signature)

	*chunkIndex++
	return doc
}

// createFileDocument creates a document for the entire proto file.
func (r *Reader) createFileDocument(content, name string, baseMetadata map[string]any) *document.Document {
	doc := idocument.CreateDocument(content, name)
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]any)
	}
	for k, v := range baseMetadata {
		doc.Metadata[k] = v
	}

	// Extract file-level metadata
	fileMetadata := r.extractFileMetadata(content)
	for k, v := range fileMetadata {
		doc.Metadata[k] = v
	}

	doc.Metadata["trpc_ast_type"] = "file"
	doc.Metadata["trpc_ast_name"] = name
	doc.Metadata["trpc_ast_full_name"] = name
	doc.Metadata["trpc_ast_language"] = "proto"
	doc.Metadata["trpc_ast_exported"] = true
	doc.Metadata["trpc_ast_scope"] = "code"
	doc.Metadata[source.MetaChunkIndex] = 0
	doc.Metadata[source.MetaChunkSize] = utf8.RuneCountInString(content)
	doc.Metadata[source.MetaContentLength] = utf8.RuneCountInString(content)

	// Build embedding text
	doc.EmbeddingText = r.buildFileEmbeddingText(content, name, fileMetadata)

	return doc
}

// buildRPCSignature builds a human-readable RPC signature.
func (e *entityExtractor) buildRPCSignature(name, inputType, outputType string, clientStreaming, serverStreaming bool) string {
	var sig strings.Builder
	sig.WriteString("rpc ")
	sig.WriteString(name)
	sig.WriteString("(")
	if clientStreaming {
		sig.WriteString("stream ")
	}
	sig.WriteString(shortTypeName(inputType))
	sig.WriteString(") returns (")
	if serverStreaming {
		sig.WriteString("stream ")
	}
	sig.WriteString(shortTypeName(outputType))
	sig.WriteString(")")
	return sig.String()
}

// qualifiedName returns the fully qualified name with proto package prefix.
func (e *entityExtractor) qualifiedName(name string) string {
	if e.protoPackage != "" {
		return e.protoPackage + "." + name
	}
	return name
}

// qualifiedNameWithParent returns the fully qualified name under a parent scope.
func (e *entityExtractor) qualifiedNameWithParent(name, parent string) string {
	if parent != "" {
		return parent + "." + name
	}
	return name
}

// nodeLineRange extracts start and end line numbers from an AST node.
func (e *entityExtractor) nodeLineRange(node ast.Node) (startLine, endLine int) {
	if node == nil || e.fileNode == nil {
		return 0, 0
	}
	info := e.fileNode.NodeInfo(node)
	start := info.Start()
	end := info.End()
	return start.Line, end.Line
}

// extractCode extracts source code lines from the file.
func (e *entityExtractor) extractCode(startLine, endLine int) string {
	if startLine <= 0 || endLine <= 0 || startLine > len(e.lines) {
		return ""
	}
	if endLine > len(e.lines) {
		endLine = len(e.lines)
	}
	selected := e.lines[startLine-1 : endLine]
	return strings.Join(selected, "\n")
}

// extractComment extracts leading comments from an AST node.
func (e *entityExtractor) extractComment(node ast.Node) string {
	if node == nil || e.fileNode == nil {
		return ""
	}
	info := e.fileNode.NodeInfo(node)
	var comments []string
	for i := 0; i < info.LeadingComments().Len(); i++ {
		c := info.LeadingComments().Index(i)
		text := c.RawText()
		// Clean up comment text
		text = strings.TrimPrefix(text, "//")
		text = strings.TrimPrefix(text, "/*")
		text = strings.TrimSuffix(text, "*/")
		text = strings.TrimSpace(text)
		if text != "" {
			comments = append(comments, text)
		}
	}
	return strings.Join(comments, "\n")
}

// buildEmbeddingText constructs the text used for embedding generation.
// This follows trpc-ast-rag's approach: use structured metadata (id, type, name, package, etc.)
// plus the code content for embedding. Field names match trpc-ast-rag's convention.
func (e *entityExtractor) buildEmbeddingText(code, name, entityType, fullName, comment, signature string) string {
	data := map[string]string{
		"id":        fullName,
		"type":      entityType,
		"name":      name,
		"full_name": fullName,
		"package":   e.protoPackage,
		"file_path": e.fileName,
		"comment":   comment,
	}

	// Include signature only if it's not empty
	if signature != "" {
		data["signature"] = signature
	}

	// For Proto entities, include code
	if code != "" {
		data["code"] = code
	}

	jsonBytes, _ := json.Marshal(data)
	return string(jsonBytes)
}

// extractFileMetadata extracts file-level metadata from proto content.
func (r *Reader) extractFileMetadata(content string) map[string]any {
	metadata := make(map[string]any)

	// Extract syntax version
	if syntax := r.extractSyntax(content); syntax != "" {
		metadata["trpc_ast_syntax"] = syntax
	}

	// Extract package name
	if pkg := r.extractPackage(content); pkg != "" {
		metadata["trpc_ast_package"] = pkg
	}

	// Extract imports
	if imports := r.extractImports(content); len(imports) > 0 {
		metadata["trpc_ast_imports"] = imports
		metadata["trpc_ast_import_count"] = len(imports)
	}

	// Extract option packages
	if goPkg := r.extractOptionString(content, "go_package"); goPkg != "" {
		metadata["trpc_ast_go_package"] = goPkg
	}
	if javaPkg := r.extractOptionString(content, "java_package"); javaPkg != "" {
		metadata["trpc_ast_java_package"] = javaPkg
	}

	// Extract service names for file-level metadata
	services := r.extractServiceNames(content)
	if len(services) > 0 {
		metadata["trpc_ast_services"] = services
		metadata["trpc_ast_service_count"] = len(services)
	}

	return metadata
}

// buildFileEmbeddingText constructs the embedding text for a file-level document.
func (r *Reader) buildFileEmbeddingText(content, fileName string, fileMetadata map[string]any) string {
	data := map[string]any{
		"file_name":   fileName,
		"type":        "proto",
		"entity_type": "file",
		"entity_name": fileName,
	}

	// Add file-level metadata
	if syntax, ok := fileMetadata["trpc_ast_syntax"]; ok {
		data["syntax"] = syntax
	}
	if pkg, ok := fileMetadata["trpc_ast_package"]; ok {
		data["package"] = pkg
	}
	if imports, ok := fileMetadata["trpc_ast_imports"]; ok {
		data["imports"] = imports
	}
	if services, ok := fileMetadata["trpc_ast_services"]; ok {
		data["services"] = services
	}
	// Include the code content for semantic understanding
	data["code"] = content

	jsonBytes, _ := json.Marshal(data)
	return string(jsonBytes)
}

// extractServiceNames extracts just the service names (for metadata).
func (r *Reader) extractServiceNames(content string) []string {
	var names []string
	re := regexp.MustCompile(`(?m)^[\s]*service\s+(\w+)\s*\{`)
	matches := re.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) > 1 {
			names = append(names, match[1])
		}
	}
	return names
}

// extractSyntax extracts the proto syntax version (proto2 or proto3).
func (r *Reader) extractSyntax(content string) string {
	re := regexp.MustCompile(`(?m)^\s*syntax\s*=\s*"([^"]+)"\s*;`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractPackage extracts the proto package name.
func (r *Reader) extractPackage(content string) string {
	re := regexp.MustCompile(`(?m)^\s*package\s+(\S+)\s*;`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractImports extracts all import statements.
func (r *Reader) extractImports(content string) []string {
	re := regexp.MustCompile(`(?m)^\s*import\s+(?:public\s+|weak\s+)?"([^"]+)"\s*;`)
	matches := re.FindAllStringSubmatch(content, -1)
	imports := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			imports = append(imports, match[1])
		}
	}
	return imports
}

// extractOptionString extracts option string values like go_package/java_package.
func (r *Reader) extractOptionString(content, optionName string) string {
	re := regexp.MustCompile(`(?m)^\s*option\s+` + regexp.QuoteMeta(optionName) + `\s*=\s*"([^"]+)"\s*;`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractFileNameFromURL extracts a file name from a URL.
func (r *Reader) extractFileNameFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		fileName := parts[len(parts)-1]
		fileName = strings.Split(fileName, "?")[0]
		fileName = strings.Split(fileName, "#")[0]
		if fileName == "" {
			return "proto_file"
		}
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName))
		return fileName
	}
	return "proto_file"
}

// applyTransformers applies all transformers to the documents.
func (r *Reader) applyTransformers(docs []*document.Document) ([]*document.Document, error) {
	if len(r.transformers) == 0 {
		return docs, nil
	}

	// Apply preprocess
	result, err := itransform.ApplyPreprocess(docs, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply preprocess: %w", err)
	}

	// Apply postprocess
	result, err = itransform.ApplyPostprocess(result, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply postprocess: %w", err)
	}

	return result, nil
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "Proto Reader"
}

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}

// extractFileOptions extracts go_package and java_package from the file descriptor proto.
// Since ResultFromAST stores options as UninterpretedOption (not in typed fields),
// we iterate over them to find the well-known option names.
func extractFileOptions(fd *descriptorpb.FileDescriptorProto) (goPackage, javaPackage string) {
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
		// Option values for go_package/java_package are string literals
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

// shortTypeName extracts the short name from a potentially qualified type name.
func shortTypeName(typeName string) string {
	// Remove leading dot
	typeName = strings.TrimPrefix(typeName, ".")
	// Return the last segment
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		return typeName[idx+1:]
	}
	return typeName
}
