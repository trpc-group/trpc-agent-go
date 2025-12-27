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
	"iter"
	"net/http"
	"sort"
	"strings"

	openapi "github.com/getkin/kin-openapi/openapi3"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

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

// Operation represents an operation in the spec.
// parameters are collected from the following sources:
// - path parameters
// - operation parameters
// - request body parameters
// - response parameters
type Operation struct {
	name        string
	description string

	endpoint        *operationEndpoint
	operationParams []*APIParameter
	responseParam   *APIParameter
	originOperation *openapi.Operation
}

func newOperation(name, desc string, endpoint *operationEndpoint, originOperation *openapi.Operation) *Operation {
	return &Operation{
		name:            name,
		description:     desc,
		endpoint:        endpoint,
		originOperation: originOperation,
	}
}

func (o *Operation) toolInputSchema() *tool.Schema {
	s := &tool.Schema{
		Type:       openapi.TypeObject,
		Properties: make(map[string]*tool.Schema),
	}
	for _, param := range o.operationParams {
		s.Properties[param.OriginalName] = convertOpenAPISchemaToToolSchema(param.schema)
	}
	return s
}

func (o *Operation) toolOutputSchema() *tool.Schema {
	s := &tool.Schema{
		Type:       openapi.TypeObject,
		Properties: make(map[string]*tool.Schema),
	}
	s.Properties[o.responseParam.OriginalName] = convertOpenAPISchemaToToolSchema(o.responseParam.schema)
	return s
}

func (o *Operation) collectSpecOperationParameters(params openapi.Parameters) {
	if len(params) == 0 {
		return
	}
	var apiParams []*APIParameter
	for _, param := range params {
		if param == nil || param.Value == nil {
			continue
		}
		v := param.Value
		param := &APIParameter{
			OriginalName: v.Name,
			Description:  v.Description,
			Required:     v.Required,
			Location:     ParameterLocation(v.In),
		}
		if v.Schema != nil && v.Schema.Value != nil {
			param.schema = v.Schema.Value
		}
		apiParams = append(apiParams, param)
	}
	o.collectOperationParameters(apiParams)
}

func (o *Operation) collectSpecRequestParameters(request *openapi.RequestBodyRef) {
	if request == nil {
		return
	}
	if request.Value == nil {
		return
	}
	for _, content := range request.Value.Content {
		var apiParams []*APIParameter
		if content.Schema.Value.Type.Is(openapi.TypeObject) {
			for propName, propSchema := range content.Schema.Value.Properties {
				if propSchema == nil {
					continue
				}
				apiParams = append(apiParams, &APIParameter{
					OriginalName: propName,
					Description:  propSchema.Value.Description,
					Location:     BodyParameter,
					schema:       propSchema.Value,
				})
			}
		} else if content.Schema.Value.Type.Is(openapi.TypeArray) {
			apiParams = append(apiParams, &APIParameter{
				OriginalName: openapi.TypeArray,
				Description:  content.Schema.Value.Items.Value.Description,
				Location:     BodyParameter,
				schema:       content.Schema.Value.Items.Value,
			})
		} else {
			apiParams = append(apiParams, &APIParameter{
				OriginalName: "",
				Description:  content.Schema.Value.Description,
				Location:     BodyParameter,
				schema:       content.Schema.Value,
			})
		}
		o.collectOperationParameters(apiParams)
	}
}

func (o *Operation) collectOperationParameters(params []*APIParameter) {
	o.operationParams = append(o.operationParams, params...)
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
					if c.Schema == nil || c.Schema.Value == nil {
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

// ParameterLocation defines the location of a parameter.
type ParameterLocation string

const (
	PathParameter     ParameterLocation = "path"
	QueryParameter    ParameterLocation = "query"
	HeaderParameter   ParameterLocation = "header"
	BodyParameter     ParameterLocation = "body"
	CookieParameter   ParameterLocation = "cookie"
	ResponseParameter ParameterLocation = "response"
)

// APIParameter represents an API parameter.
type APIParameter struct {
	OriginalName string            `json:"original_name"`
	Description  string            `json:"description"`
	Location     ParameterLocation `json:"location"`
	Required     bool              `json:"required"`

	level  string
	schema *openapi.Schema
}

var (
	pathItemMethods = [...]string{
		http.MethodConnect,
		http.MethodDelete,
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodPatch,
		http.MethodPost,
		http.MethodPut,
		http.MethodTrace,
	}
)

func methodOperations(p *openapi.PathItem) iter.Seq2[string, *openapi.Operation] {
	ops := [...]*openapi.Operation{
		p.Connect,
		p.Delete,
		p.Get,
		p.Head,
		p.Options,
		p.Patch,
		p.Post,
		p.Put,
		p.Trace,
	}
	return func(yield func(string, *openapi.Operation) bool) {
		for i := range len(pathItemMethods) {
			if !yield(pathItemMethods[i], ops[i]) {
				return
			}
		}
	}
}
