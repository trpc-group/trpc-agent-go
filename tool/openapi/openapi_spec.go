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
	"net/url"

	openapi "github.com/getkin/kin-openapi/openapi3"
)

// Loader loads the OpenAPI spec.
type Loader interface {
	// Load loads the OpenAPI spec.
	Load(ctx context.Context) (*openapi.T, error)
}

// LoaderOption is the option for the loader.
type LoaderOption func(*openapi.Loader)

// WithExternalRefs sets whether to allow external $ref references.
func WithExternalRefs(allow bool) LoaderOption {
	return func(l *openapi.Loader) {
		l.IsExternalRefsAllowed = allow
	}
}

// WithReadFromURI sets the function to read from URI.
func WithReadFromURI(f openapi.ReadFromURIFunc) LoaderOption {
	return func(l *openapi.Loader) {
		l.ReadFromURIFunc = f
	}
}

type dataLoader struct {
	loader *openapi.Loader
	data   []byte
}

// Load loads the OpenAPI spec from io.Reader.
func (d *dataLoader) Load(ctx context.Context) (*openapi.T, error) {
	d.loader.Context = ctx
	return d.loader.LoadFromData(d.data)
}

// NewDataLoader creates a new data spec loader.
func NewDataLoader(data []byte, opts ...LoaderOption) (*dataLoader, error) {
	loader := &openapi.Loader{}
	for _, opt := range opts {
		opt(loader)
	}
	return &dataLoader{loader: loader, data: data}, nil
}

type fileLoader struct {
	loader *openapi.Loader
	path   string
}

// Load loads the OpenAPI spec from file.
func (f *fileLoader) Load(ctx context.Context) (*openapi.T, error) {
	f.loader.Context = ctx
	return f.loader.LoadFromFile(f.path)
}

// NewFileLoader creates a new file spec loader.
func NewFileLoader(filePath string, opts ...LoaderOption) (*fileLoader, error) {
	loader := &openapi.Loader{}
	for _, opt := range opts {
		opt(loader)
	}
	return &fileLoader{loader: loader, path: filePath}, nil
}

type urlLoader struct {
	loader   *openapi.Loader
	location *url.URL
}

// Load loads the OpenAPI spec from url.
func (u *urlLoader) Load(ctx context.Context) (*openapi.T, error) {
	loader := openapi.Loader{Context: ctx}
	return loader.LoadFromURI(u.location)
}

// NewURILoader creates a new url spec loader.
func NewURILoader(uri string, opts ...LoaderOption) (*urlLoader, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	loader := &openapi.Loader{}
	for _, opt := range opts {
		opt(loader)
	}
	return &urlLoader{loader: loader, location: u}, nil
}

func newDocProcessor(doc *openapi.T) *docProcessor {
	return &docProcessor{doc: doc}
}

type docProcessor struct {
	doc        *openapi.T
	operations []*Operation
}

// InfoTitle returns the title of the spec.
func (s *docProcessor) InfoTitle() string {
	if s.doc.Info == nil {
		return ""
	}
	return s.doc.Info.Title
}

const defaultBaseURL = "/"

// processOperations processes the operations in the spec.
// Currently, only the first server URL is used as the base URL.
// And variables are not supported.
func (s *docProcessor) processOperations() error {
	baseURL := defaultBaseURL
	if len(s.doc.Servers) != 0 {
		baseURL = s.doc.Servers[0].URL
	}

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
				specOperation,
			)
			op.collectSpecOperationParameters(specOperation.Parameters)
			op.collectSpecRequestParameters(specOperation.RequestBody)
			op.collectResponseParameter(specOperation.Responses)
			s.operations = append(s.operations, op)
		}
	}
	return nil
}
