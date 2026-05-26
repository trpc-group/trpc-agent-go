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
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	openapi "github.com/getkin/kin-openapi/openapi3"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type openAPITool struct {
	inputSchema  *tool.Schema
	outputSchema *tool.Schema

	operation *Operation
	config    *config
}

func newOpenAPITool(config *config, operation *Operation) tool.CallableTool {
	o := &openAPITool{
		operation: operation,
		config:    config,
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
	dec := json.NewDecoder(bytes.NewReader(jsonArgs))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, err
	}
	// Reject trailing non-whitespace data to match json.Unmarshal strictness.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON arguments")
	}

	for _, param := range o.operation.operationParams {
		_, ok := args[param.OriginalName]
		if !ok && param.Required && param.schema.Default != nil {
			args[param.OriginalName] = param.schema.Default
		}
	}

	req, err := o.prepareRequest(ctx, args)
	if err != nil {
		return nil, err
	}

	resp, err := o.config.httpClient.Do(req)
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
	params := make(map[string]*APIParameter)
	for _, param := range o.operation.operationParams {
		params[param.OriginalName] = param
	}

	var (
		queryParams  = make(map[string]any)
		pathParams   = make(map[string]any)
		headerParams = make(map[string]any)
		cookieParams = make(map[string]any)
		bodyParams   = make(map[string]any)
	)

	for argName, argValue := range args {
		param, ok := params[argName]
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
		case BodyParameter:
			bodyParams[param.OriginalName] = argValue
		default:
		}
	}

	endpointURL, err := makeRequestURL(o.operation.endpoint, pathParams, queryParams)
	if err != nil {
		return nil, err
	}
	requestBody := makeRequestBody(o.operation, bodyParams)
	req, err := http.NewRequestWithContext(ctx, o.operation.endpoint.method, endpointURL, requestBody)
	if err != nil {
		return nil, err
	}
	for _, cookie := range makeRequestCookies(cookieParams) {
		req.AddCookie(cookie)
	}
	// Add headers
	for name, value := range makeRequestHeaders(headerParams) {
		req.Header.Add(name, value)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", o.config.userAgent)
	return req, nil
}

// paramValueToString converts JSON scalar tool argument values to strings
// for HTTP query, header, and cookie parameters. Non-scalar values are skipped.
func paramValueToString(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), true
	case int:
		return strconv.Itoa(v), true
	case int8:
		return strconv.FormatInt(int64(v), 10), true
	case int16:
		return strconv.FormatInt(int64(v), 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case uint:
		return strconv.FormatUint(uint64(v), 10), true
	case uint8:
		return strconv.FormatUint(uint64(v), 10), true
	case uint16:
		return strconv.FormatUint(uint64(v), 10), true
	case uint32:
		return strconv.FormatUint(uint64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case json.Number:
		return v.String(), true
	default:
		return "", false
	}
}

func makeRequestURL(endpoint *operationEndpoint, pathParams, queryParams map[string]any) (string, error) {
	path := endpoint.path
	for arg, value := range pathParams {
		path = strings.ReplaceAll(path, fmt.Sprintf("{%s}", arg), fmt.Sprintf("%v", value))
	}
	endpointURL, err := url.Parse(endpoint.baseURL + path)
	if err != nil {
		return "", err
	}

	endpointQuery := url.Values{}
	for arg, value := range queryParams {
		if v, ok := paramValueToString(value); ok {
			endpointQuery.Set(arg, v)
		}
	}
	endpointURL.RawQuery = endpointQuery.Encode()
	return endpointURL.String(), nil
}

func makeRequestBody(operation *Operation, params map[string]any) io.Reader {
	requestBody := operation.originOperation.RequestBody
	if requestBody == nil || requestBody.Value == nil {
		return nil
	}
	for mimeType, mediaType := range requestBody.Value.Content {
		schema := mediaType.Schema
		if schema.Value == nil {
			continue
		}
		var bodyData any
		if schema.Value.Type.Is(openapi3.TypeObject) {
			objectBodyData := make(map[string]any)
			for _, p := range operation.operationParams {
				if v, ok := params[p.OriginalName]; ok {
					objectBodyData[p.OriginalName] = v
				}
			}
			bodyData = objectBodyData
		} else if schema.Value.Type.Is(openapi3.TypeArray) {
			for _, p := range operation.operationParams {
				if p.OriginalName == openapi.TypeArray {
					bodyData = params[p.OriginalName]
					break
				}
			}
		} else {
			for _, p := range operation.operationParams {
				if p.OriginalName == "" {
					bodyData = params[p.OriginalName]
					break
				}
			}
		}
		if marshaler, ok := supportedMimeTypes[mimeType]; ok {
			data, err := marshaler.Marshal(bodyData)
			if err != nil {
				log.Errorf("failed to marshal request body: %v", err)
				return nil
			}
			log.Debugf("body data: %+v", string(data))
			return bytes.NewReader(data)
		}
	}
	return nil
}

type marshaler interface {
	Marshal(any) ([]byte, error)
}

type jsonMarshaler struct{}

// Marshal marshals the provided data into JSON.
func (j *jsonMarshaler) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

type xmlMarshaler struct{}

// Marshal marshals the provided data into XML.
func (j *xmlMarshaler) Marshal(v any) ([]byte, error) {
	return xml.Marshal(v)
}

var supportedMimeTypes = map[string]marshaler{
	"application/json": &jsonMarshaler{},
	"application/xml":  &xmlMarshaler{},
}

func makeRequestCookies(cookieParams map[string]any) []*http.Cookie {
	cookies := []*http.Cookie{}
	for name, value := range cookieParams {
		if v, ok := paramValueToString(value); ok {
			cookies = append(cookies, &http.Cookie{
				Name:  name,
				Value: v,
			})
		}
	}
	return cookies
}

func makeRequestHeaders(headerParams map[string]any) map[string]string {
	headers := make(map[string]string)
	for name, value := range headerParams {
		if v, ok := paramValueToString(value); ok {
			headers[name] = v
		}
	}
	return headers
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
