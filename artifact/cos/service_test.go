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
	"fmt"
	"hash/crc64"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
func createMockService() (*Service, *MockTransport) {
	mockTransport := NewMockTransport()

	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Create a mock COS client with the mock transport
	mockBucketURL, _ := url.Parse("https://test-bucket-1234567890.cos.ap-guangzhou.myqcloud.com")
	mockCosClient := cos.NewClient(&cos.BaseURL{BucketURL: mockBucketURL}, mockClient)

	// Use WithClient option to inject the mock COS client
	service, _ := NewService("cos-service", "", WithClient(mockCosClient))

	return service, mockTransport
}

func TestArtifact_SessionScope(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user1",
		SessionID: "session1",
	}
	sessionScopeKey := "test.txt"

	var artifacts []*artifact.Artifact
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, &artifact.Artifact{
			ArtifactDescriptor: artifact.ArtifactDescriptor{
				MimeType: "text/plain",
				Name:     "display_name_user_scope_test.txt",
			},
			Data: []byte("Hello, World!" + strconv.Itoa(i)),
		})
	}

	// Save artifacts and verify versions
	for i, a := range artifacts {
		version, err := s.SaveArtifact(ctx, sessionInfo, sessionScopeKey, a)
		require.NoError(t, err)
		require.Equal(t, i, version)
	}

	// List versions
	versions, err := s.ListVersions(ctx, sessionInfo, sessionScopeKey)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{0, 1, 2}, versions)

	// Load latest version (should be version 2)
	data, desc, err := s.LoadArtifactBytes(ctx, sessionInfo, sessionScopeKey, nil)
	require.NoError(t, err)
	require.NotNil(t, desc)
	require.EqualValues(t, []byte("Hello, World!"+strconv.Itoa(2)), data)
	require.EqualValues(t, "text/plain", desc.MimeType)
	require.EqualValues(t, 2, desc.Version)

	// Load specific versions
	for i, wanted := range artifacts {
		gotData, gotDesc, err := s.LoadArtifactBytes(ctx, sessionInfo, sessionScopeKey, &i)
		require.NoError(t, err)
		require.NotNil(t, gotDesc)
		require.EqualValues(t, wanted.Data, gotData)
		require.EqualValues(t, wanted.MimeType, gotDesc.MimeType)
		require.EqualValues(t, sessionScopeKey, gotDesc.Name)
		require.EqualValues(t, i, gotDesc.Version)
	}

	// List artifact keys
	keys, err := s.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{sessionScopeKey}, keys)

	// Delete artifact
	err = s.DeleteArtifact(ctx, sessionInfo, sessionScopeKey)
	require.NoError(t, err)

	// Verify artifact is deleted
	keys, err = s.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	require.Empty(t, keys)

	// Verify versions are empty
	versions, err = s.ListVersions(ctx, sessionInfo, sessionScopeKey)
	require.NoError(t, err)
	require.Empty(t, versions)

	// Verify artifact cannot be loaded
	data, desc, err = s.LoadArtifactBytes(ctx, sessionInfo, sessionScopeKey, nil)
	require.NoError(t, err)
	require.Nil(t, data)
	require.Nil(t, desc)
}

func TestArtifact_UserScope(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user2",
		SessionID: "session1",
	}
	userScopeKey := "user:test.txt"

	// Save multiple versions
	for i := 0; i < 3; i++ {
		data := []byte("Hi, World!" + strconv.Itoa(i))
		version, err := s.SaveArtifact(ctx, sessionInfo, userScopeKey, &artifact.Artifact{
			ArtifactDescriptor: artifact.ArtifactDescriptor{
				MimeType: "text/plain",
				Name:     "display_name_user_scope_test.txt",
			},
			Data: data,
		})
		require.NoError(t, err)
		require.Equal(t, i, version)
	}

	// List versions
	versions, err := s.ListVersions(ctx, sessionInfo, userScopeKey)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{0, 1, 2}, versions)

	// Load latest version
	data, desc, err := s.LoadArtifactBytes(ctx, sessionInfo, userScopeKey, nil)
	require.NoError(t, err)
	require.NotNil(t, desc)
	require.EqualValues(t, []byte("Hi, World!"+strconv.Itoa(2)), data)
	require.EqualValues(t, "text/plain", desc.MimeType)
	require.EqualValues(t, 2, desc.Version)

	// Load specific versions
	for i := 0; i < 3; i++ {
		data, desc, err := s.LoadArtifactBytes(ctx, sessionInfo, userScopeKey, &i)
		require.NoError(t, err)
		require.NotNil(t, desc)
		require.EqualValues(t, []byte("Hi, World!"+strconv.Itoa(i)), data)
		require.EqualValues(t, "text/plain", desc.MimeType)
		require.EqualValues(t, i, desc.Version)
	}

	// List artifact keys
	keys, err := s.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{userScopeKey}, keys)

	// Delete artifact
	err = s.DeleteArtifact(ctx, sessionInfo, userScopeKey)
	require.NoError(t, err)

	// Verify artifact is deleted
	keys, err = s.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	require.Empty(t, keys)

	// Verify versions are empty
	versions, err = s.ListVersions(ctx, sessionInfo, userScopeKey)
	require.NoError(t, err)
	require.Empty(t, versions)

	// Verify artifact cannot be loaded
	data, desc, err = s.LoadArtifactBytes(ctx, sessionInfo, userScopeKey, nil)
	require.NoError(t, err)
	require.Nil(t, data)
	require.Nil(t, desc)
}

func TestMixedScopeArtifacts(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Save session-scoped artifact
	sessionArtifact := &artifact.Artifact{
		ArtifactDescriptor: artifact.ArtifactDescriptor{
			MimeType: "text/plain",
			Name:     "session.txt",
		},
		Data: []byte("session data"),
	}
	version, err := s.SaveArtifact(ctx, sessionInfo, "session.txt", sessionArtifact)
	require.NoError(t, err)
	assert.Equal(t, 0, version)

	// Save user-scoped artifact
	userArtifact := &artifact.Artifact{
		ArtifactDescriptor: artifact.ArtifactDescriptor{
			MimeType: "text/plain",
			Name:     "user:profile.txt",
		},
		Data: []byte("user data"),
	}
	version, err = s.SaveArtifact(ctx, sessionInfo, "user:profile.txt", userArtifact)
	require.NoError(t, err)
	assert.Equal(t, 0, version)

	// List all keys should include both
	keys, err := s.ListArtifactKeys(ctx, sessionInfo)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"session.txt", "user:profile.txt"}, keys)

	// Load both artifacts
	loadedSessionData, loadedSessionDesc, err := s.LoadArtifactBytes(ctx, sessionInfo, "session.txt", nil)
	require.NoError(t, err)
	require.NotNil(t, loadedSessionDesc)
	assert.Equal(t, sessionArtifact.Data, loadedSessionData)

	loadedUserData, loadedUserDesc, err := s.LoadArtifactBytes(ctx, sessionInfo, "user:profile.txt", nil)
	require.NoError(t, err)
	require.NotNil(t, loadedUserDesc)
	assert.Equal(t, userArtifact.Data, loadedUserData)
}

func TestLoadNonexistentArtifact(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Load non-existent artifact
	data, desc, err := s.LoadArtifactBytes(ctx, sessionInfo, "nonexistent.txt", nil)
	require.NoError(t, err)
	assert.Nil(t, data)
	assert.Nil(t, desc)

	// Load non-existent version
	invalidVersion := 999
	data, desc, err = s.LoadArtifactBytes(ctx, sessionInfo, "nonexistent.txt", &invalidVersion)
	require.NoError(t, err)
	assert.Nil(t, data)
	assert.Nil(t, desc)
}

func TestDeleteNonexistentArtifact(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// Delete non-existent artifact should not error
	err := s.DeleteArtifact(ctx, sessionInfo, "nonexistent.txt")
	require.NoError(t, err)
}

func TestListVersionsNonexistentArtifact(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	// List versions for non-existent artifact
	versions, err := s.ListVersions(ctx, sessionInfo, "nonexistent.txt")
	require.NoError(t, err)
	assert.Empty(t, versions)
}

func TestMultipleVersionsAndDeletion(t *testing.T) {
	s, _ := createMockService()
	ctx := context.Background()

	sessionInfo := artifact.SessionInfo{
		AppName:   "testapp",
		UserID:    "user123",
		SessionID: "session456",
	}

	filename := "versioned.txt"

	// Save multiple versions
	for i := 0; i < 5; i++ {
		artifact := &artifact.Artifact{
			ArtifactDescriptor: artifact.ArtifactDescriptor{
				MimeType: "text/plain",
				Name:     filename,
			},
			Data: []byte(fmt.Sprintf("version %d data", i)),
		}
		version, err := s.SaveArtifact(ctx, sessionInfo, filename, artifact)
		require.NoError(t, err)
		assert.Equal(t, i, version)
	}

	// List all versions
	versions, err := s.ListVersions(ctx, sessionInfo, filename)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{0, 1, 2, 3, 4}, versions)

	// Load specific versions
	for i := 0; i < 5; i++ {
		data, desc, err := s.LoadArtifactBytes(ctx, sessionInfo, filename, &i)
		require.NoError(t, err)
		require.NotNil(t, desc)
		expected := fmt.Sprintf("version %d data", i)
		assert.Equal(t, []byte(expected), data)
	}

	// Delete all versions
	err = s.DeleteArtifact(ctx, sessionInfo, filename)
	require.NoError(t, err)

	// Verify all versions are deleted
	versions, err = s.ListVersions(ctx, sessionInfo, filename)
	require.NoError(t, err)
	assert.Empty(t, versions)
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
			assert.NotNil(t, service)
			assert.NotNil(t, service.cosClient)
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
