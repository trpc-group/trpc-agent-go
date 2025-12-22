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
	"io"
	"net/url"

	openapi "github.com/getkin/kin-openapi/openapi3"
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

const defaultBaseURL = "/"

func (s *specProcessor) processOperations() error {
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
