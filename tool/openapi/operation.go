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

	openapi "github.com/getkin/kin-openapi/openapi3"
)

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
