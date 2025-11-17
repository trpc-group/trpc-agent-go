//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openapi

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	openapi "github.com/getkin/kin-openapi/openapi3"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SpecLoader loads the OpenAPI spec.
type SpecLoader interface {
	// Load loads the OpenAPI spec.
	Load(ctx context.Context) (*openapi.T, error)
}

type dataSpecLoader struct {
	r io.Reader
}

// Load loads the OpenAPI spec from io.Reader.
func (d *dataSpecLoader) Load(ctx context.Context) (*openapi.T, error) {
	loader := openapi.Loader{Context: ctx}
	return loader.LoadFromIoReader(d.r)
}

// NewDataLoader creates a new data spec loader.
func NewDataLoader(r io.Reader) *dataSpecLoader {
	return &dataSpecLoader{r: r}
}

type fileSpecLoader struct {
	path string
}

// Load loads the OpenAPI spec from file.
func (f *fileSpecLoader) Load(ctx context.Context) (*openapi.T, error) {
	loader := openapi.Loader{Context: ctx}
	return loader.LoadFromFile(f.path)
}

// NewFileLoader creates a new file spec loader.
func NewFileLoader(filePath string) *fileSpecLoader {
	return &fileSpecLoader{path: filePath}
}

type urlSpecLoader struct {
	uri string
}

// Load loads the OpenAPI spec from url.
func (u *urlSpecLoader) Load(ctx context.Context) (*openapi.T, error) {
	uri, err := url.Parse(u.uri)
	if err != nil {
		return nil, err
	}
	loader := openapi.Loader{Context: ctx}
	return loader.LoadFromURI(uri)
}

// NewURILoader creates a new url spec loader.
func NewURILoader(uri string) *urlSpecLoader {
	return &urlSpecLoader{uri: uri}
}

func newSpecProcessor(doc *openapi.T) *specProcessor {
	return &specProcessor{doc: doc}
}

type specProcessor struct {
	doc        *openapi.T
	operations []*Operation
}

// InfoTitle returns the title of the spec.
func (s *specProcessor) InfoTitle() string {
	return s.doc.Info.Title
}

func (s *specProcessor) processOperations() error {
	if len(s.doc.Servers) == 0 {
		return fmt.Errorf("no server defined")
	}
	baseURL := s.doc.Servers[0].URL

	// TODO: security scheme, if any
	for path, pathItem := range s.doc.Paths.Map() {
		if pathItem == nil {
			continue
		}
		// TODO: add path-level parameters
		for method, specOperation := range methodOperations(pathItem) {
			if specOperation == nil {
				continue
			}
			op := newOperation(
				operationName(specOperation, path, method),
				operationDesc(specOperation),
				newOperationEndpoint(baseURL, path, method),
			)
			op.collectSpecOperationParameters(specOperation.Parameters)
			op.collectSpecRequestParameters(specOperation.RequestBody)
			op.collectResponseParameter(specOperation.Responses)
			s.operations = append(s.operations, op)
		}
	}
	return nil
}

func operationName(op *openapi.Operation, path, method string) string {
	n := op.OperationID
	if n == "" {
		n = strings.ToLower(method + "_" + path)
	}
	return n
}

func operationDesc(op *openapi.Operation) string {
	desc := op.Description
	if desc == "" {
		desc = op.Summary
	}
	return desc
}

func newOperation(name, desc string, endpoint *operationEndpoint) *Operation {
	return &Operation{
		name:        name,
		description: desc,
		endpoint:    endpoint,
	}
}

// Operation represents an operation in the spec.
// parameters are collected from the following sources:
// - path parameters
// - operation parameters
// - request body parameters
// - response parameters
type Operation struct {
	name        string
	description string

	endpoint      *operationEndpoint
	requestParams []*APIParameter
	responseParam *APIParameter
}

func (o *Operation) toolInputSchema() *tool.Schema {
	s := &tool.Schema{
		Type:       openapi.TypeObject,
		Properties: make(map[string]*tool.Schema),
	}
	for _, param := range o.requestParams {
		s.Properties[param.OriginalName] = convertOpenAPISchemaToToolSchema(param.schema)
	}
	return s
}

func (o *Operation) toolOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:       openapi.TypeObject,
		Properties: make(map[string]*tool.Schema),
	}
}

func (o *Operation) collectSpecOperationParameters(params openapi.Parameters) {
	if len(params) == 0 {
		return
	}
	var ps []*APIParameter
	for _, param := range params {
		if param == nil {
			continue
		}
		pv := param.Value
		ps = append(ps, &APIParameter{
			OriginalName: pv.Name,
			Description:  pv.Description,
			Location:     ParameterLocation(pv.In),
			Required:     pv.Required,
			schema:       pv.Schema.Value,
		})
	}
	o.collectOperationParameters(ps)
}

func (o *Operation) collectSpecRequestParameters(request *openapi.RequestBodyRef) {
	if request == nil {
		return
	}
	if request.Value == nil {
		return
	}
	for _, content := range request.Value.Content {
		var ps []*APIParameter
		if content.Schema.Value.Type.Is(openapi.TypeObject) {
			for propName, propSchema := range content.Schema.Value.Properties {
				if propSchema == nil {
					continue
				}
				ps = append(ps, &APIParameter{
					OriginalName: propName,
					Description:  propSchema.Value.Description,
					Location:     BodyParameter,
					schema:       propSchema.Value,
				})
			}
		} else if content.Schema.Value.Type.Is(openapi.TypeArray) {
			ps = append(ps, &APIParameter{
				OriginalName: openapi.TypeArray,
				Description:  content.Schema.Value.Items.Value.Description,
				Location:     BodyParameter,
				schema:       content.Schema.Value.Items.Value,
			})
		} else {
			ps = append(ps, &APIParameter{
				OriginalName: "",
				Description:  content.Schema.Value.Description,
				Location:     BodyParameter,
				schema:       content.Schema.Value,
			})
		}
		o.collectOperationParameters(ps)
	}
}

func (o *Operation) collectOperationParameters(params []*APIParameter) {
	o.requestParams = append(o.requestParams, params...)
}

func (o *Operation) collectResponseParameter(responses *openapi.Responses) {
	var (
		schema      *openapi.Schema
		statusCodes []string
	)

	for code := range responses.Map() {
		if strings.HasPrefix(code, "2") {
			statusCodes = append(statusCodes, code)
		}
	}
	sort.Strings(statusCodes)
	if len(statusCodes) != 0 {
		validCode := statusCodes[0]
		if v := responses.Value(validCode); v != nil {
			if v.Value != nil && len(v.Value.Content) != 0 {
				for _, c := range v.Value.Content {
					if c.Schema == nil {
						continue
					}
					schema = c.Schema.Value
					break
				}
			}
		}
	}
	o.responseParam = &APIParameter{
		Location: ResponseParameter,
		schema:   schema,
	}
}

type operationEndpoint struct {
	baseURL string
	path    string
	method  string
}

func newOperationEndpoint(baseURL, path, method string) *operationEndpoint {
	return &operationEndpoint{
		baseURL: baseURL,
		path:    path,
		method:  method,
	}
}

// convertOpenAPISchemaToToolSchema converts kin-openapi/openapi3.Schema to tool.Schema
func convertOpenAPISchemaToToolSchema(openapiSchema *openapi.Schema) *tool.Schema {
	if openapiSchema == nil {
		return &tool.Schema{Type: "object"}
	}
	toolSchema := &tool.Schema{}

	// Convert type
	// openapi scheme types include: "string", "number", "integer", "boolean", "array", "object"
	// tool.Schema type is similar, we take the first type if multiple are defined
	if len(openapiSchema.Type.Slice()) > 0 {
		toolSchema.Type = openapiSchema.Type.Slice()[0]
	} else {
		toolSchema.Type = "object" // Default type
	}

	// Convert description
	toolSchema.Description = openapiSchema.Description

	// Convert properties
	if openapiSchema.Properties != nil {
		toolSchema.Properties = make(map[string]*tool.Schema)
		for propName, propSchema := range openapiSchema.Properties {
			if propSchema.Value != nil {
				toolSchema.Properties[propName] = convertOpenAPISchemaToToolSchema(propSchema.Value)
			}
		}
	}

	// Convert required fields
	if len(openapiSchema.Required) > 0 {
		toolSchema.Required = openapiSchema.Required
	}

	// Convert items (for array types)
	if openapiSchema.Items != nil && openapiSchema.Items.Value != nil {
		toolSchema.Items = convertOpenAPISchemaToToolSchema(openapiSchema.Items.Value)
	}

	// Convert additional properties
	if has := openapiSchema.AdditionalProperties.Has; has != nil && *has {
		if openapiSchema.AdditionalProperties.Schema != nil && openapiSchema.AdditionalProperties.Schema.Value != nil {
			toolSchema.AdditionalProperties = convertOpenAPISchemaToToolSchema(openapiSchema.AdditionalProperties.Schema.Value)
		}
	}

	// Convert default value
	if openapiSchema.Default != nil {
		toolSchema.Default = openapiSchema.Default
	}

	// Convert enum values
	if len(openapiSchema.Enum) > 0 {
		toolSchema.Enum = openapiSchema.Enum
	}

	return toolSchema
}
