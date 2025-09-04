//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/stretchr/testify/require"
)

func TestClientV7_CRUD(t *testing.T) {
	indexCreated := false
	docs := make(map[string]map[string]any)

	rt := roundTripper(func(r *http.Request) *http.Response {
		ok := func(code int, body string) *http.Response {
			resp := &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}
			resp.Header.Set("X-Elastic-Product", "Elasticsearch")
			return resp
		}
		p := r.URL.Path
		_, last := path.Split(p)

		if r.Method == http.MethodHead && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			if indexCreated {
				return ok(http.StatusOK, "")
			}
			return ok(http.StatusNotFound, "")
		}
		if r.Method == http.MethodPut && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			indexCreated = true
			return ok(http.StatusOK, `{}`)
		}
		if (r.Method == http.MethodPost || r.Method == http.MethodPut) && strings.Contains(p, "/_doc/") && !strings.Contains(p, "_update") {
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			docs[last] = m
			return ok(http.StatusOK, `{}`)
		}
		if r.Method == http.MethodGet && strings.Contains(p, "/_doc/") {
			if d, present := docs[last]; present {
				body, _ := json.Marshal(d)
				return ok(http.StatusOK, `{"found":true,"_source":`+string(body)+`}`)
			}
			return ok(http.StatusNotFound, `{"found":false}`)
		}
		if r.Method == http.MethodPost && strings.Contains(p, "_update") {
			var upd struct {
				Doc map[string]any `json:"doc"`
			}
			_ = json.NewDecoder(r.Body).Decode(&upd)
			if d, ok := docs[last]; ok {
				for k, v := range upd.Doc {
					d[k] = v
				}
				docs[last] = d
			}
			return ok(http.StatusOK, `{}`)
		}
		if r.Method == http.MethodDelete && strings.Contains(p, "/_doc/") {
			delete(docs, last)
			return ok(http.StatusOK, `{}`)
		}
		if r.Method == http.MethodDelete && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			indexCreated = false
			return ok(http.StatusOK, `{}`)
		}
		if r.Method == http.MethodPost && strings.Contains(p, "_search") {
			return ok(http.StatusOK, `{"hits":{"hits":[]}}`)
		}
		return ok(http.StatusOK, `{}`)
	})

	es, err := esv7.NewClient(esv7.Config{Addresses: []string{"http://mock"}, Transport: rt})
	require.NoError(t, err)
	c := &clientV7{esClient: es}

	ctx := context.Background()
	exists, err := c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, c.CreateIndex(ctx, "idx", []byte(`{"mappings":{}}`)))
	exists, err = c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.True(t, exists)

	require.NoError(t, c.IndexDoc(ctx, "idx", "1", []byte(`{"id":"1"}`)))
	b, err := c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "\"found\":true")
	require.NoError(t, c.UpdateDoc(ctx, "idx", "1", []byte(`{"doc":{"name":"n2"}}`)))
	_, err = c.Search(ctx, "idx", []byte(`{}`))
	require.NoError(t, err)
	require.NoError(t, c.DeleteDoc(ctx, "idx", "1"))
	_, err = c.GetDoc(ctx, "idx", "1")
	require.Error(t, err)
	require.NoError(t, c.DeleteIndex(ctx, "idx"))
	exists, err = c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestClientV7_Ping(t *testing.T) {
	rt := roundTripper(func(r *http.Request) *http.Response {
		ok := func(code int, body string) *http.Response {
			resp := &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(bytes.NewBufferString(body)), Header: make(http.Header)}
			resp.Header.Set("X-Elastic-Product", "Elasticsearch")
			return resp
		}
		// HEAD /
		if r.Method == http.MethodHead && r.URL.Path == "/" {
			return ok(http.StatusOK, "")
		}
		return ok(http.StatusOK, `{}`)
	})

	es, err := esv7.NewClient(esv7.Config{Addresses: []string{"http://mock"}, Transport: rt})
	require.NoError(t, err)
	c := &clientV7{esClient: es}

	ctx := context.Background()
	require.NoError(t, c.Ping(ctx))
}
