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
	"reflect"
	"sort"
	"strings"
	"testing"

	openapi "github.com/getkin/kin-openapi/openapi3"
)

func Test_makeRequestURL(t *testing.T) {
	type args struct {
		endpoint    *operationEndpoint
		pathParams  map[string]any
		queryParams map[string]any
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "basic path without parameters",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/users",
					method:  "GET",
				},
				pathParams:  map[string]any{},
				queryParams: map[string]any{},
			},
			want:    "https://api.example.com/users",
			wantErr: false,
		},
		{
			name: "path with single parameter replacement",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/users/{id}",
					method:  "GET",
				},
				pathParams: map[string]any{
					"id": "123",
				},
				queryParams: map[string]any{},
			},
			want:    "https://api.example.com/users/123",
			wantErr: false,
		},
		{
			name: "path with multiple parameter replacements",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/users/{userId}/posts/{postId}",
					method:  "GET",
				},
				pathParams: map[string]any{
					"userId": "456",
					"postId": "789",
				},
				queryParams: map[string]any{},
			},
			want:    "https://api.example.com/users/456/posts/789",
			wantErr: false,
		},
		{
			name: "query parameters only",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/search",
					method:  "GET",
				},
				pathParams: map[string]any{},
				queryParams: map[string]any{
					"q":     "golang",
					"limit": "10",
				},
			},
			want:    "https://api.example.com/search?limit=10&q=golang",
			wantErr: false,
		},
		{
			name: "path parameters and query parameters combined",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/users/{id}/posts",
					method:  "GET",
				},
				pathParams: map[string]any{
					"id": "123",
				},
				queryParams: map[string]any{
					"status": "published",
					"sort":   "date",
				},
			},
			want:    "https://api.example.com/users/123/posts?sort=date&status=published",
			wantErr: false,
		},
		{
			name: "invalid URL should return error",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "://invalid-url",
					path:    "/test",
					method:  "GET",
				},
				pathParams:  map[string]any{},
				queryParams: map[string]any{},
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "non-string query parameters should be ignored",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/test",
					method:  "GET",
				},
				pathParams: map[string]any{},
				queryParams: map[string]any{
					"stringParam": "value",
					"intParam":    123,
					"boolParam":   true,
				},
			},
			want:    "https://api.example.com/test?stringParam=value",
			wantErr: false,
		},
		{
			name: "path with special characters in parameters",
			args: args{
				endpoint: &operationEndpoint{
					baseURL: "https://api.example.com",
					path:    "/search/{query}",
					method:  "GET",
				},
				pathParams: map[string]any{
					"query": "hello world",
				},
				queryParams: map[string]any{},
			},
			want:    "https://api.example.com/search/hello%20world",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := makeRequestURL(tt.args.endpoint, tt.args.pathParams, tt.args.queryParams)
			if (err != nil) != tt.wantErr {
				t.Errorf("makeRequestURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("makeRequestURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_makeRequestBody(t *testing.T) {
	type args struct {
		operation *Operation
		bodyParam map[string]any
	}
	tests := []struct {
		name string
		args args
		want []byte
	}{
		{
			name: "operation without request body",
			args: args{
				operation: &Operation{
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				bodyParam: map[string]any{},
			},
			want: nil,
		},
		{
			name: "operation with nil request body value",
			args: args{
				operation: &Operation{
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: nil,
						},
					},
				},
				bodyParam: map[string]any{"key": "value"},
			},
			want: nil,
		},
		{
			name: "operation with valid request body but no body parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "name",
							Description:  "name",
							Location:     BodyParameter,
						},
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type:        &openapi.Types{openapi.TypeString},
												Description: "name",
											},
										},
									},
								},
							},
						},
					},
				},
				bodyParam: map[string]any{},
			},
			want: []byte("null"),
		},
		{
			name: "operation with valid request body and body parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "name",
							Description:  "name",
							Location:     BodyParameter,
						},
						{
							OriginalName: "value",
							Description:  "value",
							Location:     BodyParameter,
						},
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type:        &openapi.Types{openapi.TypeObject},
												Description: "abc",
												Properties: map[string]*openapi.SchemaRef{
													"name": {
														Value: &openapi.Schema{
															Type:        &openapi.Types{openapi.TypeString},
															Description: "name",
														},
													},
													"value": {
														Value: &openapi.Schema{
															Type:        &openapi.Types{openapi.TypeInteger},
															Description: "value",
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
				bodyParam: map[string]any{
					"name":  "test",
					"value": 123,
				},
			},
			want: []byte(`{"name":"test","value":123}`),
		},
		{
			name: "operation with array type request body",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "array",
							Description:  "array data",
							Location:     BodyParameter,
						},
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type:        &openapi.Types{openapi.TypeArray},
												Description: "array data",
											},
										},
									},
								},
							},
						},
					},
				},
				bodyParam: map[string]any{
					"array": []any{"item1", "item2", "item3"},
				},
			},
			want: []byte(`["item1","item2","item3"]`),
		},
		{
			name: "operation with array type request body and multiple parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "array",
							Description:  "array data",
							Location:     BodyParameter,
						},
						{
							OriginalName: "name",
							Description:  "name",
							Location:     BodyParameter,
						},
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type:        &openapi.Types{openapi.TypeArray},
												Description: "array data",
											},
										},
									},
								},
							},
						},
					},
				},
				bodyParam: map[string]any{
					"array": []any{"item1", "item2"},
					"name":  "test",
				},
			},
			want: []byte(`["item1","item2"]`),
		},
		{
			name: "operation with array type request body but no array parameter",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "name",
							Description:  "name",
							Location:     BodyParameter,
						},
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type:        &openapi.Types{openapi.TypeArray},
												Description: "array data",
											},
										},
									},
								},
							},
						},
					},
				},
				bodyParam: map[string]any{
					"name": "test",
				},
			},
			want: []byte("null"),
		},
		{
			name: "operation with array type request body and empty array",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "array",
							Description:  "array data",
							Location:     BodyParameter,
						},
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type:        &openapi.Types{openapi.TypeArray},
												Description: "array data",
											},
										},
									},
								},
							},
						},
					},
				},
				bodyParam: map[string]any{
					"array": []any{},
				},
			},
			want: []byte(`[]`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRequestBody(tt.args.operation, tt.args.bodyParam)
			if got == nil && tt.want == nil {
				return
			}
			buf := make([]byte, 1024)
			n, err := got.Read(buf)
			if err != nil && err != io.EOF {
				t.Errorf("makeRequestBody() read error = %v", err)
				return
			}
			if string(buf[:n]) != string(tt.want) {
				t.Errorf("makeRequestBody() = %v, want %v", string(buf[:n]), string(tt.want))
			}
		})
	}
}

func Test_makeRequestCookies(t *testing.T) {
	tests := []struct {
		name         string
		cookieParams map[string]any
		want         []*http.Cookie // sort by name
	}{
		{
			name:         "empty cookie parameters",
			cookieParams: map[string]any{},
			want:         []*http.Cookie{},
		},
		{
			name: "single string cookie parameter",
			cookieParams: map[string]any{
				"session": "abc123",
			},
			want: []*http.Cookie{
				{Name: "session", Value: "abc123"},
			},
		},
		{
			name: "multiple string cookie parameters",
			cookieParams: map[string]any{
				"session": "abc123",
				"user":    "john",
				"lang":    "en",
			},
			want: []*http.Cookie{
				{Name: "lang", Value: "en"},
				{Name: "session", Value: "abc123"},
				{Name: "user", Value: "john"},
			},
		},
		{
			name: "mixed types - only strings are included",
			cookieParams: map[string]any{
				"session": "abc123",
				"user":    "john",
				"count":   123,
				"active":  true,
			},
			want: []*http.Cookie{
				{Name: "session", Value: "abc123"},
				{Name: "user", Value: "john"},
			},
		},
		{
			name: "special characters in cookie values",
			cookieParams: map[string]any{
				"token": "abc=123;def=456",
				"data":  "value with spaces",
			},
			want: []*http.Cookie{
				{Name: "data", Value: "value with spaces"},
				{Name: "token", Value: "abc=123;def=456"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRequestCookies(tt.cookieParams)
			if len(got) != len(tt.want) {
				t.Errorf("makeRequestCookies() returned %d cookies, want %d", len(got), len(tt.want))
				return
			}
			// sort by name
			sort.Slice(got, func(i, j int) bool {
				return got[i].Name < got[j].Name
			})
			for i, cookie := range got {
				if cookie.Name != tt.want[i].Name || cookie.Value != tt.want[i].Value {
					t.Errorf("makeRequestCookies() cookie[%d] = %+v, want %+v", i, cookie, tt.want[i])
				}
			}
		})
	}
}

func Test_makeRequestHeaders(t *testing.T) {
	tests := []struct {
		name         string
		headerParams map[string]any
		want         map[string]string
	}{
		{
			name:         "empty header parameters",
			headerParams: map[string]any{},
			want:         map[string]string{},
		},
		{
			name: "single string header parameter",
			headerParams: map[string]any{
				"Authorization": "Bearer token123",
			},
			want: map[string]string{
				"Authorization": "Bearer token123",
			},
		},
		{
			name: "multiple string header parameters",
			headerParams: map[string]any{
				"Authorization": "Bearer token123",
				"Content-Type":  "application/json",
				"User-Agent":    "MyApp/1.0",
			},
			want: map[string]string{
				"Authorization": "Bearer token123",
				"Content-Type":  "application/json",
				"User-Agent":    "MyApp/1.0",
			},
		},
		{
			name: "mixed types - only strings are included",
			headerParams: map[string]any{
				"Authorization": "Bearer token123",
				"Content-Type":  "application/json",
				"Retry-Count":   3,
				"Is-Valid":      true,
			},
			want: map[string]string{
				"Authorization": "Bearer token123",
				"Content-Type":  "application/json",
			},
		},
		{
			name: "case sensitive header names",
			headerParams: map[string]any{
				"Authorization":   "Bearer token123",
				"authorization":   "Basic auth456",
				"X-Custom-Header": "custom value",
			},
			want: map[string]string{
				"Authorization":   "Bearer token123",
				"authorization":   "Basic auth456",
				"X-Custom-Header": "custom value",
			},
		},
		{
			name: "special characters in header values",
			headerParams: map[string]any{
				"X-Custom-Data": "value with spaces and = signs",
				"X-Another":     "line1\nline2",
			},
			want: map[string]string{
				"X-Custom-Data": "value with spaces and = signs",
				"X-Another":     "line1\nline2",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRequestHeaders(tt.headerParams)
			if len(got) != len(tt.want) {
				t.Errorf("makeRequestHeaders() returned %d headers, want %d", len(got), len(tt.want))
				return
			}
			for key, value := range got {
				if wantValue, ok := tt.want[key]; !ok || value != wantValue {
					t.Errorf("makeRequestHeaders() header[%s] = %s, want %s", key, value, wantValue)
				}
			}
			// Also check that all expected headers are present
			for key, wantValue := range tt.want {
				if gotValue, ok := got[key]; !ok || gotValue != wantValue {
					t.Errorf("makeRequestHeaders() missing header[%s] = %s", key, wantValue)
				}
			}
		})
	}
}

func Test_prepareRequest(t *testing.T) {
	type args struct {
		operation *Operation
		args      map[string]any
	}
	tests := []struct {
		name    string
		args    args
		want    *http.Request
		wantErr bool
	}{
		{
			name: "basic GET request without parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{},
			},
			wantErr: false,
		},
		{
			name: "GET request with path parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "userId",
							Location:     PathParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users/{userId}",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{
					"userId": "123",
				},
			},
			wantErr: false,
		},
		{
			name: "GET request with query parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "limit",
							Location:     QueryParameter,
						},
						{
							OriginalName: "offset",
							Location:     QueryParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{
					"limit":  "10",
					"offset": "0",
				},
			},
			wantErr: false,
		},
		{
			name: "POST request with body parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "name",
							Location:     BodyParameter,
						},
						{
							OriginalName: "email",
							Location:     BodyParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users",
						method:  "POST",
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type: &openapi.Types{openapi.TypeObject},
											},
										},
									},
								},
							},
						},
					},
				},
				args: map[string]any{
					"name":  "John Doe",
					"email": "john@example.com",
				},
			},
			wantErr: false,
		},
		{
			name: "request with headers and cookies",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "Authorization",
							Location:     HeaderParameter,
						},
						{
							OriginalName: "session",
							Location:     CookieParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/profile",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{
					"Authorization": "Bearer token123",
					"session":       "abc123",
				},
			},
			wantErr: false,
		},
		{
			name: "request with all parameter types",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "userId",
							Location:     PathParameter,
						},
						{
							OriginalName: "limit",
							Location:     QueryParameter,
						},
						{
							OriginalName: "Authorization",
							Location:     HeaderParameter,
						},
						{
							OriginalName: "session",
							Location:     CookieParameter,
						},
						{
							OriginalName: "name",
							Location:     BodyParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users/{userId}/posts",
						method:  "POST",
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type: &openapi.Types{openapi.TypeObject},
											},
										},
									},
								},
							},
						},
					},
				},
				args: map[string]any{
					"userId":        "456",
					"limit":         "10",
					"Authorization": "Bearer token123",
					"session":       "abc123",
					"name":          "Test Post",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid URL should return error",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "://invalid-url",
						path:    "/test",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{},
			},
			wantErr: true,
		},
		{
			name: "non-string parameters should be handled correctly",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "id",
							Location:     PathParameter,
						},
						{
							OriginalName: "count",
							Location:     QueryParameter,
						},
						{
							OriginalName: "active",
							Location:     HeaderParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/items/{id}",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{
					"id":     123,
					"count":  5,
					"active": true,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock openAPITool instance
			o := &openAPITool{
				operation: tt.args.operation,
				config: &config{
					userAgent: "TestAgent/1.0",
				},
			}

			got, err := o.prepareRequest(context.Background(), tt.args.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("prepareRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				// If we expect an error, we're done
				return
			}

			// Validate the request object
			if got == nil {
				t.Error("prepareRequest() returned nil request")
				return
			}

			// Check method
			if got.Method != tt.args.operation.endpoint.method {
				t.Errorf("prepareRequest() method = %v, want %v", got.Method, tt.args.operation.endpoint.method)
			}

			// Check URL contains expected base URL and path
			if !strings.Contains(got.URL.String(), tt.args.operation.endpoint.baseURL) {
				t.Errorf("prepareRequest() URL = %v, should contain base URL %v", got.URL.String(), tt.args.operation.endpoint.baseURL)
			}

			// Check Content-Type header
			contentType := got.Header.Get("Content-Type")
			if tt.args.operation.endpoint.method == "POST" || tt.args.operation.endpoint.method == "PUT" || tt.args.operation.endpoint.method == "PATCH" {
				if contentType != "application/json" {
					t.Errorf("prepareRequest() Content-Type = %v, want application/json", contentType)
				}
			}

			// Check User-Agent header
			userAgent := got.Header.Get("User-Agent")
			if userAgent != "TestAgent/1.0" {
				t.Errorf("prepareRequest() User-Agent = %v, want TestAgent/1.0", userAgent)
			}
		})
	}
}

func Test_openAPITool_Call(t *testing.T) {
	type args struct {
		operation *Operation
		args      map[string]any
	}
	tests := []struct {
		name        string
		args        args
		setupMock   func(*http.Client)
		want        any
		wantErr     bool
		errContains string
	}{
		{
			name: "successful GET request with JSON response",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "userId",
							Location:     PathParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users/{userId}",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{
					"userId": "123",
				},
			},
			setupMock: func(client *http.Client) {
				// Mock successful response with JSON body
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader(`{"id":"123","name":"John Doe"}`)),
							Header:     make(http.Header),
						}, nil
					},
				}
			},
			want: map[string]any{
				"id":   "123",
				"name": "John Doe",
			},
			wantErr: false,
		},
		{
			name: "successful POST request with body parameters",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "name",
							Location:     BodyParameter,
						},
						{
							OriginalName: "email",
							Location:     BodyParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users",
						method:  "POST",
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type: &openapi.Types{openapi.TypeObject},
											},
										},
									},
								},
							},
						},
					},
				},
				args: map[string]any{
					"email": "jane@example.com",
					"name":  "Jane Smith",
				},
			},
			setupMock: func(client *http.Client) {
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						// Verify request body
						body, _ := io.ReadAll(req.Body)
						expectedBody := `{"email":"jane@example.com","name":"Jane Smith"}`
						if string(body) != expectedBody {
							return nil, fmt.Errorf("unexpected request body: %s, expect: %v", string(body), expectedBody)
						}
						return &http.Response{
							StatusCode: 201,
							Body:       io.NopCloser(strings.NewReader(`{"id":"456","status":"created"}`)),
							Header:     make(http.Header),
						}, nil
					},
				}
			},
			want: map[string]any{
				"id":     "456",
				"status": "created",
			},
			wantErr: false,
		},
		{
			name: "HTTP error response",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/error",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{},
			},
			setupMock: func(client *http.Client) {
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: 404,
							Body:       io.NopCloser(strings.NewReader(`{"error":"Not Found"}`)),
							Header:     make(http.Header),
						}, nil
					},
				}
			},
			wantErr:     true,
			errContains: "HTTP 404",
		},
		{
			name: "network error",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/test",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{},
			},
			setupMock: func(client *http.Client) {
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return nil, fmt.Errorf("connection refused")
					},
				}
			},
			wantErr:     true,
			errContains: "connection refused",
		},
		{
			name: "non-JSON response returns string",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/text",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{},
			},
			setupMock: func(client *http.Client) {
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader("plain text response")),
							Header:     make(http.Header),
						}, nil
					},
				}
			},
			want:    "plain text response",
			wantErr: false,
		},
		{
			name: "request with all parameter types",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{
						{
							OriginalName: "userId",
							Location:     PathParameter,
						},
						{
							OriginalName: "limit",
							Location:     QueryParameter,
						},
						{
							OriginalName: "Authorization",
							Location:     HeaderParameter,
						},
						{
							OriginalName: "session",
							Location:     CookieParameter,
						},
						{
							OriginalName: "title",
							Location:     BodyParameter,
						},
					},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/users/{userId}/posts",
						method:  "POST",
					},
					originOperation: &openapi.Operation{
						RequestBody: &openapi.RequestBodyRef{
							Value: &openapi.RequestBody{
								Content: openapi.Content{
									"application/json": &openapi.MediaType{
										Schema: &openapi.SchemaRef{
											Value: &openapi.Schema{
												Type: &openapi.Types{openapi.TypeObject},
											},
										},
									},
								},
							},
						},
					},
				},
				args: map[string]any{
					"userId":        "789",
					"limit":         "20",
					"Authorization": "Bearer token789",
					"session":       "xyz789",
					"title":         "Test Post Title",
				},
			},
			setupMock: func(client *http.Client) {
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						// Verify headers
						if req.Header.Get("Authorization") != "Bearer token789" {
							return nil, fmt.Errorf("missing Authorization header")
						}

						// Verify cookies
						cookie, err := req.Cookie("session")
						if err != nil || cookie.Value != "xyz789" {
							return nil, fmt.Errorf("missing session cookie")
						}

						// Verify URL contains path parameter
						if !strings.Contains(req.URL.String(), "/users/789/posts") {
							return nil, fmt.Errorf("URL missing path parameter")
						}

						// Verify query parameter
						if req.URL.Query().Get("limit") != "20" {
							return nil, fmt.Errorf("missing limit query parameter")
						}

						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
							Header:     make(http.Header),
						}, nil
					},
				}
			},
			want: map[string]any{
				"success": true,
			},
			wantErr: false,
		},
		{
			name: "invalid JSON arguments should return error",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/test",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: nil, // This will cause JSON unmarshal error
			},
			setupMock: func(client *http.Client) {
				// No mock needed as error occurs before HTTP call
			},
			wantErr:     true,
			errContains: "unexpected end of JSON input",
		},
		{
			name: "response body read error",
			args: args{
				operation: &Operation{
					operationParams: []*APIParameter{},
					endpoint: &operationEndpoint{
						baseURL: "https://api.example.com",
						path:    "/test",
						method:  "GET",
					},
					originOperation: &openapi.Operation{
						RequestBody: nil,
					},
				},
				args: map[string]any{},
			},
			setupMock: func(client *http.Client) {
				http.DefaultTransport = &mockTransport{
					handler: func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: 200,
							Body:       &errorReader{},
							Header:     make(http.Header),
						}, nil
					},
				}
			},
			wantErr:     true,
			errContains: "failed to read response body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock transport
			if tt.setupMock != nil {
				originalTransport := http.DefaultTransport
				defer func() {
					http.DefaultTransport = originalTransport
				}()
				tt.setupMock(nil)
			}

			// Create openAPITool instance
			o := &openAPITool{
				operation: tt.args.operation,
				config: &config{
					userAgent:  "TestAgent/1.0",
					httpClient: http.DefaultClient,
				},
			}

			// Prepare JSON arguments
			var jsonArgs []byte
			if tt.args.args != nil {
				var err error
				jsonArgs, err = json.Marshal(tt.args.args)
				if err != nil {
					t.Fatalf("Failed to marshal args: %v", err)
				}
			}

			// Call the method
			got, err := o.Call(context.Background(), jsonArgs)

			// Check error conditions
			if (err != nil) != tt.wantErr {
				t.Errorf("Call() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Call() error = %v, should contain %v", err, tt.errContains)
				}
				return
			}

			// Check result
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Call() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Mock transport for testing
type mockTransport struct {
	handler func(*http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.handler(req)
}

// Error reader for testing read errors
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("mock read error")
}

func (e *errorReader) Close() error {
	return nil
}
