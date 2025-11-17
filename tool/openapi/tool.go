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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type openAPITool struct {
	inputSchema  *tool.Schema
	outputSchema *tool.Schema

	operation *Operation
	ts        *openAPIToolSet
}

func newOpenAPITool(ts *openAPIToolSet, operation *Operation) tool.CallableTool {
	o := &openAPITool{
		operation: operation,
		ts:        ts,
	}
	o.inputSchema = operation.toolInputSchema()
	o.outputSchema = operation.toolOutputSchema()
	return o
}

// Call executes the API call.
// parameter replace:  "query", "header", "path" or "cookie"
func (o *openAPITool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	log.Debug("Calling OpenAPI tool", "name", o.operation.name)
	args := make(map[string]any)
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}

	for _, param := range o.operation.requestParams {
		_, ok := args[param.OriginalName]
		if !ok && param.Required && param.schema.Default != nil {
			args[param.OriginalName] = param.schema.Default
		}
	}

	req, err := o.prepareRequest(ctx, args)
	if err != nil {
		return nil, err
	}

	resp, err := o.ts.config.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response based on status code
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var result any
		if err := json.Unmarshal(respBody, &result); err != nil {
			// If JSON parsing fails, return as string
			return string(respBody), nil
		}
		return result, nil
	}

	return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
}

func (o *openAPITool) prepareRequest(ctx context.Context, args map[string]any) (*http.Request, error) {
	apiParams := make(map[string]*APIParameter)
	for _, param := range o.operation.requestParams {
		apiParams[param.OriginalName] = param
	}

	var (
		queryParams  = make(map[string]any)
		pathParams   = make(map[string]any)
		headerParams = make(map[string]any)
		cookieParams = make(map[string]any)
	)

	for argName, argValue := range args {
		param, ok := apiParams[argName]
		if !ok {
			continue
		}
		switch param.Location {
		case QueryParameter:
			queryParams[param.OriginalName] = argValue
		case PathParameter:
			pathParams[param.OriginalName] = argValue
		case HeaderParameter:
			headerParams[param.OriginalName] = argValue
		case CookieParameter:
			cookieParams[param.OriginalName] = argValue
		default:
			// TODO(Ericxwang): support body parameter
		}
	}

	endpointURL := makeRequestURL(o.operation.endpoint, pathParams)
	req, err := http.NewRequestWithContext(ctx, o.operation.endpoint.method, endpointURL, nil)
	if err != nil {
		return nil, err
	}
	// Add headers
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", o.ts.config.userAgent)
	return req, nil
}

func makeRequestURL(endpoint *operationEndpoint, pathParams map[string]any) string {
	endpointURL := endpoint.baseURL + endpoint.path
	for arg, value := range pathParams {
		endpointURL = strings.ReplaceAll(endpointURL, fmt.Sprintf("{%s}", arg), fmt.Sprintf("%v", value))
	}
	return endpointURL
}

// Declaration returns the declaration of the tool.
func (o *openAPITool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         o.operation.name,
		Description:  o.operation.description,
		InputSchema:  o.inputSchema,
		OutputSchema: o.outputSchema,
	}
}
