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
	"io"
	"net/http"
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
		want         []*http.Cookie
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
				{Name: "session", Value: "abc123"},
				{Name: "user", Value: "john"},
				{Name: "lang", Value: "en"},
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
				{Name: "token", Value: "abc=123;def=456"},
				{Name: "data", Value: "value with spaces"},
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
