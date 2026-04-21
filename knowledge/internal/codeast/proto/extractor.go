//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package proto

import (
	"strings"

	"github.com/bufbuild/protocompile/ast"
	parserpkg "github.com/bufbuild/protocompile/parser"
	"google.golang.org/protobuf/types/descriptorpb"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

type extractInput struct {
	fileNode     *ast.FileNode
	result       parserpkg.Result
	fileName     string
	protoPackage string
	syntax       string
	goPackage    string
	javaPackage  string
	imports      []string
	lines        []string
}

type defaultExtractor struct{}

func newDefaultExtractor() *defaultExtractor {
	return &defaultExtractor{}
}

func (e *defaultExtractor) Extract(input *extractInput) ([]*codeast.Node, error) {
	if input == nil || input.result == nil {
		return nil, nil
	}

	extractor := &entityExtractor{
		fileNode:     input.fileNode,
		result:       input.result,
		fileName:     input.fileName,
		protoPackage: input.protoPackage,
		syntax:       input.syntax,
		goPackage:    input.goPackage,
		javaPackage:  input.javaPackage,
		imports:      input.imports,
		lines:        input.lines,
	}
	return extractor.extract(), nil
}

type entityExtractor struct {
	fileNode        *ast.FileNode
	result          parserpkg.Result
	fileName        string
	protoPackage    string
	syntax          string
	goPackage       string
	javaPackage     string
	imports         []string
	lines           []string
	allServiceNames []string
}

func (e *entityExtractor) extract() []*codeast.Node {
	fd := e.result.FileDescriptorProto()
	chunkIndex := 0
	var nodes []*codeast.Node

	e.collectAllServiceNames(fd)

	for _, msg := range fd.GetMessageType() {
		nodes = append(nodes, e.extractMessage(msg, e.protoPackage, &chunkIndex)...)
	}

	for _, enum := range fd.GetEnumType() {
		n := e.extractEnum(enum, e.protoPackage, &chunkIndex)
		nodes = append(nodes, n)
	}

	for _, svc := range fd.GetService() {
		nodes = append(nodes, e.extractService(svc, &chunkIndex)...)
	}

	return nodes
}

func (e *entityExtractor) collectAllServiceNames(fd *descriptorpb.FileDescriptorProto) {
	for _, svc := range fd.GetService() {
		e.allServiceNames = append(e.allServiceNames, svc.GetName())
	}
}

func (e *entityExtractor) addFileMetadata(node *codeast.Node, includeServices bool) {
	if node.Metadata == nil {
		node.Metadata = make(map[string]any)
	}

	if e.syntax != "" {
		node.Metadata["syntax"] = e.syntax
	}
	if e.protoPackage != "" {
		node.Metadata["package"] = e.protoPackage
	}
	if e.goPackage != "" {
		node.Metadata["go_package"] = e.goPackage
	}
	if e.javaPackage != "" {
		node.Metadata["java_package"] = e.javaPackage
	}
	if len(e.imports) > 0 {
		node.Metadata["imports"] = append([]string(nil), e.imports...)
		node.Metadata["import_count"] = len(e.imports)
	}
	if includeServices && len(e.allServiceNames) > 0 {
		node.Metadata["services"] = append([]string(nil), e.allServiceNames...)
		node.Metadata["service_count"] = len(e.allServiceNames)
	}

	node.Imports = append([]string(nil), e.imports...)
}

func (e *entityExtractor) extractService(svc *descriptorpb.ServiceDescriptorProto, chunkIndex *int) []*codeast.Node {
	var docs []*codeast.Node
	svcName := svc.GetName()
	svcFullName := QualifiedName(e.protoPackage, svcName)

	svcNode := e.result.ServiceNode(svc)
	startLine, endLine := e.nodeLineRange(svcNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(svcNode)

	svcDoc := e.createEntityNode(code, svcName, codeast.EntityService, svcFullName, comment, "", startLine, endLine, chunkIndex)
	e.addFileMetadata(svcDoc, true)
	svcDoc.Metadata["language"] = string(codeast.LanguageProto)
	svcDoc.Metadata["scope"] = string(codeast.ScopeCode)
	svcDoc.Metadata["rpc_methods"] = e.collectRPCMethods(svc)
	docs = append(docs, svcDoc)

	for _, method := range svc.GetMethod() {
		rpcDoc := e.extractRPC(method, svcFullName, chunkIndex)
		if rpcDoc != nil {
			docs = append(docs, rpcDoc)
		}
	}

	return docs
}

func (e *entityExtractor) collectRPCMethods(svc *descriptorpb.ServiceDescriptorProto) []string {
	var methods []string
	for _, method := range svc.GetMethod() {
		methods = append(methods, method.GetName())
	}
	return methods
}

func (e *entityExtractor) extractRPC(method *descriptorpb.MethodDescriptorProto, svcFullName string, chunkIndex *int) *codeast.Node {
	rpcName := method.GetName()
	rpcFullName := svcFullName + "." + rpcName

	inputType := method.GetInputType()
	outputType := method.GetOutputType()
	clientStreaming := method.GetClientStreaming()
	serverStreaming := method.GetServerStreaming()
	sig := BuildRPCSignature(rpcName, inputType, outputType, clientStreaming, serverStreaming)

	methodASTNode := e.result.MethodNode(method)
	startLine, endLine := e.nodeLineRange(methodASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(methodASTNode)

	doc := e.createEntityNode(code, rpcName, codeast.EntityRPC, rpcFullName, comment, sig, startLine, endLine, chunkIndex)
	e.addFileMetadata(doc, false)
	doc.Metadata["language"] = string(codeast.LanguageProto)
	doc.Metadata["scope"] = string(codeast.ScopeCode)
	doc.Metadata["signature"] = sig
	doc.Metadata["service"] = svcFullName
	doc.Metadata["input_type"] = inputType
	doc.Metadata["output_type"] = outputType
	doc.Metadata["client_streaming"] = clientStreaming
	doc.Metadata["server_streaming"] = serverStreaming
	return doc
}

func (e *entityExtractor) extractMessage(msg *descriptorpb.DescriptorProto, parentPkg string, chunkIndex *int) []*codeast.Node {
	if msg.GetOptions() != nil && msg.GetOptions().GetMapEntry() {
		return nil
	}

	msgName := msg.GetName()
	msgFullName := QualifiedNameWithParent(msgName, parentPkg)

	msgASTNode := e.result.MessageNode(msg)
	startLine, endLine := e.nodeLineRange(msgASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(msgASTNode)

	doc := e.createEntityNode(code, msgName, codeast.EntityMessage, msgFullName, comment, "", startLine, endLine, chunkIndex)
	e.addFileMetadata(doc, true)
	doc.Metadata["language"] = string(codeast.LanguageProto)
	doc.Metadata["scope"] = string(codeast.ScopeCode)

	var docs []*codeast.Node
	docs = append(docs, doc)

	for _, nestedMsg := range msg.GetNestedType() {
		docs = append(docs, e.extractMessage(nestedMsg, msgFullName, chunkIndex)...)
	}

	for _, nestedEnum := range msg.GetEnumType() {
		enumDoc := e.extractEnum(nestedEnum, msgFullName, chunkIndex)
		if enumDoc != nil {
			docs = append(docs, enumDoc)
		}
	}

	return docs
}

func (e *entityExtractor) extractEnum(enum *descriptorpb.EnumDescriptorProto, parentPkg string, chunkIndex *int) *codeast.Node {
	enumName := enum.GetName()
	enumFullName := QualifiedNameWithParent(enumName, parentPkg)

	enumASTNode := e.result.EnumNode(enum)
	startLine, endLine := e.nodeLineRange(enumASTNode)
	code := e.extractCode(startLine, endLine)
	comment := e.extractComment(enumASTNode)

	var values []string
	for _, value := range enum.GetValue() {
		values = append(values, value.GetName())
	}

	doc := e.createEntityNode(code, enumName, codeast.EntityEnum, enumFullName, comment, "", startLine, endLine, chunkIndex)
	e.addFileMetadata(doc, true)
	doc.Metadata["language"] = string(codeast.LanguageProto)
	doc.Metadata["scope"] = string(codeast.ScopeCode)
	if len(values) > 0 {
		doc.Metadata["enum_values"] = append([]string(nil), values...)
	}
	return doc
}

func (e *entityExtractor) createEntityNode(code, name string, nodeType codeast.EntityType, fullName, comment, signature string, startLine, endLine int, chunkIndex *int) *codeast.Node {
	node := &codeast.Node{
		ID:         fullName,
		Type:       nodeType,
		Name:       name,
		FullName:   fullName,
		Scope:      codeast.ScopeCode,
		Language:   codeast.LanguageProto,
		Signature:  strings.TrimSpace(signature),
		Comment:    strings.TrimSpace(comment),
		Code:       code,
		FilePath:   e.fileName,
		LineStart:  startLine,
		LineEnd:    endLine,
		ChunkIndex: *chunkIndex,
		Package:    e.protoPackage,
		Metadata:   map[string]any{},
	}
	*chunkIndex++
	return node
}

func (e *entityExtractor) nodeLineRange(node ast.Node) (startLine, endLine int) {
	if node == nil || e.fileNode == nil {
		return 0, 0
	}
	info := e.fileNode.NodeInfo(node)
	start := info.Start()
	end := info.End()
	return start.Line, end.Line
}

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

func (e *entityExtractor) extractComment(node ast.Node) string {
	if node == nil || e.fileNode == nil {
		return ""
	}
	info := e.fileNode.NodeInfo(node)
	var comments []string
	for i := 0; i < info.LeadingComments().Len(); i++ {
		c := info.LeadingComments().Index(i)
		text := c.RawText()
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
