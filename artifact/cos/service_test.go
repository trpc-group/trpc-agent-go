//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cos

import (
	"bytes"
	"context"
	"encoding/xml"
	"hash/crc64"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tencentyun/cos-go-sdk-v5"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

// MockTransport implements http.RoundTripper to mock COS HTTP requests
type MockTransport struct {
	objects map[string][]byte            // objectName -> data
	headers map[string]map[string]string // objectName -> headers
}

func NewMockTransport() *MockTransport {
	return &MockTransport{
		objects: make(map[string][]byte),
		headers: make(map[string]map[string]string),
	}
}

// ListBucketResult represents the XML structure for COS list bucket response
type ListBucketResult struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	Name        string   `xml:"Name"`
	Prefix      string   `xml:"Prefix"`
	MaxKeys     int      `xml:"MaxKeys"`
	IsTruncated bool     `xml:"IsTruncated"`
	Contents    []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	} `xml:"Contents"`
}

func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case "PUT":
		// Object upload
		objectKey := strings.TrimPrefix(req.URL.Path, "/")

		// Read request body
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}

		// Store object data
		m.objects[objectKey] = data

		// Store headers
		if m.headers[objectKey] == nil {
			m.headers[objectKey] = make(map[string]string)
		}
		if contentType := req.Header.Get("Content-Type"); contentType != "" {
			m.headers[objectKey]["Content-Type"] = contentType
		}

		// Calculate CRC64 for the data to match COS SDK expectations
		crc64Table := crc64.MakeTable(crc64.ECMA)
		crc64Value := crc64.Checksum(data, crc64Table)

		// Create response with required headers for COS SDK
		header := make(http.Header)
		header.Set("x-cos-hash-crc64ecma", strconv.FormatUint(crc64Value, 10))
		header.Set("ETag", `"mocketagvalue"`)

		return &http.Response{
			StatusCode: 200,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil

	case "GET":
		if req.URL.RawQuery != "" {
			// List objects request
			params, _ := url.ParseQuery(req.URL.RawQuery)
			prefix := params.Get("prefix")

			result := ListBucketResult{
				Name:        "test-bucket",
				Prefix:      prefix,
				MaxKeys:     1000,
				IsTruncated: false,
			}

			for key := range m.objects {
				if prefix == "" || strings.HasPrefix(key, prefix) {
					result.Contents = append(result.Contents, struct {
						Key  string `xml:"Key"`
						Size int64  `xml:"Size"`
					}{
						Key:  key,
						Size: int64(len(m.objects[key])),
					})
				}
			}

			xmlData, err := xml.Marshal(result)
			if err != nil {
				return nil, err
			}

			return &http.Response{
				StatusCode: 200,
				Header:     map[string][]string{"Content-Type": {"application/xml"}},
				Body:       io.NopCloser(bytes.NewReader(xmlData)),
			}, nil
		} else {
			// Object download
			objectKey := strings.TrimPrefix(req.URL.Path, "/")

			if data, exists := m.objects[objectKey]; exists {
				header := make(http.Header)

				// Set stored headers
				if headers, hasHeaders := m.headers[objectKey]; hasHeaders {
					for k, v := range headers {
						header.Set(k, v)
					}
				}
				// Set default content type if not set
				if header.Get("Content-Type") == "" {
					header.Set("Content-Type", "application/octet-stream")
				}

				return &http.Response{
					StatusCode: 200,
					Header:     header,
					Body:       io.NopCloser(bytes.NewReader(data)),
				}, nil
			}

			// Object not found
			return &http.Response{
				StatusCode: 404,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code></Error>`)),
			}, nil
		}

	case "DELETE":
		// Object deletion
		objectKey := strings.TrimPrefix(req.URL.Path, "/")
		delete(m.objects, objectKey)
		delete(m.headers, objectKey)

		return &http.Response{
			StatusCode: 204,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	case "HEAD":
		// Object HEAD
		objectKey := strings.TrimPrefix(req.URL.Path, "/")
		if data, exists := m.objects[objectKey]; exists {
			header := make(http.Header)
			if headers, hasHeaders := m.headers[objectKey]; hasHeaders {
				for k, v := range headers {
					header.Set(k, v)
				}
			}
			if header.Get("Content-Type") == "" {
				header.Set("Content-Type", "application/octet-stream")
			}
			header.Set("Content-Length", strconv.Itoa(len(data)))
			return &http.Response{
				StatusCode: 200,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}
		return &http.Response{
			StatusCode: 404,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code></Error>`)),
		}, nil
	}

	return &http.Response{
		StatusCode: 405,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("Method not allowed")),
	}, nil
}

// createMockService creates a Service instance using mock COS client
func createMockService(t *testing.T) (*Service, *MockTransport) {
	t.Helper()

	mockTransport := NewMockTransport()

	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Create a mock COS client with the mock transport
	mockBucketURL, _ := url.Parse("https://test-bucket-1234567890.cos.ap-guangzhou.myqcloud.com")
	mockCosClient := cos.NewClient(&cos.BaseURL{BucketURL: mockBucketURL}, mockClient)

	// Use WithClient option to inject the mock COS client
	service, err := NewService("cos-service", "", WithClient(mockCosClient))
	require.NoError(t, err)

	return service, mockTransport
}

func TestCOSService_PutHeadOpenVersionsListDelete(t *testing.T) {
	s, transport := createMockService(t)
	ctx := context.Background()

	key := artifact.Key{
		AppName:   "testapp",
		UserID:    "user1",
		SessionID: "session1",
		Scope:     artifact.ScopeSession,
		Name:      "test.txt",
	}

	desc1, err := s.Put(ctx, key, bytes.NewReader([]byte("v1")), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	desc2, err := s.Put(ctx, key, bytes.NewReader([]byte("v2")), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)
	require.NotEqual(t, desc1.Version, desc2.Version)

	for objectKey := range transport.objects {
		require.True(t, strings.HasPrefix(objectKey, "artifact/"), "objectKey=%s", objectKey)
	}

	h, err := s.Head(ctx, key, nil)
	require.NoError(t, err)
	require.Equal(t, desc2.Version, h.Version)

	rc, od, err := s.Open(ctx, key, &desc1.Version)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, []byte("v1"), b)
	require.Equal(t, desc1.Version, od.Version)

	vers, err := s.Versions(ctx, key)
	require.NoError(t, err)
	require.Len(t, vers, 2)

	items, next, err := s.List(ctx, artifact.Key{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Scope:     key.Scope,
	}, artifact.WithListLimit(10))
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, items, 1)
	require.Equal(t, "test.txt", items[0].Key.Name)

	require.NoError(t, s.Delete(ctx, key, artifact.DeleteAllOpt()))
	_, err = s.Head(ctx, key, nil)
	require.ErrorIs(t, err, artifact.ErrNotFound)
}

func TestCOSService_UserScopeIgnoresSessionID(t *testing.T) {
	s, _ := createMockService(t)
	ctx := context.Background()

	putKey := artifact.Key{AppName: "testapp", UserID: "user1", SessionID: "s1", Scope: artifact.ScopeUser, Name: "profile.txt"}
	_, err := s.Put(ctx, putKey, bytes.NewReader([]byte("u")), artifact.WithPutMimeType("text/plain"))
	require.NoError(t, err)

	getKey := artifact.Key{AppName: "testapp", UserID: "user1", SessionID: "s2", Scope: artifact.ScopeUser, Name: "profile.txt"}
	rc, _, err := s.Open(ctx, getKey, nil)
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, []byte("u"), b)
}

func TestNewServiceWithOptions(t *testing.T) {
	tests := []struct {
		name      string
		bucketURL string
		options   []Option
	}{
		{
			name:      "with secret credentials",
			bucketURL: "https://test-bucket-1234567890.cos.ap-guangzhou.myqcloud.com",
			options: []Option{
				WithSecretID("test-id"),
				WithSecretKey("test-key"),
			},
		},
		{
			name:      "with custom timeout",
			bucketURL: "https://test-bucket-1234567890.cos.ap-guangzhou.myqcloud.com",
			options: []Option{
				WithTimeout(30 * 1000000000), // 30 seconds in nanoseconds
			},
		},
		{
			name:      "with custom http client",
			bucketURL: "https://test-bucket-1234567890.cos.ap-guangzhou.myqcloud.com",
			options: []Option{
				WithHTTPClient(&http.Client{}),
			},
		},
		{
			name:      "with pre-configured COS client",
			bucketURL: "", // bucketURL is ignored when using WithClient
			options: []Option{
				WithClient(cos.NewClient(&cos.BaseURL{
					BucketURL: mustParseURL(t, "https://test-bucket-1234567890.cos.ap-guangzhou.myqcloud.com"),
				}, &http.Client{})),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(tt.name, tt.bucketURL, tt.options...)
			require.NoError(t, err)
			require.NotNil(t, service)
			require.NotNil(t, service.cosClient)
		})
	}
}

// mustParseURL is a helper function for testing
func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
