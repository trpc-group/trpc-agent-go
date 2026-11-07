//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	filesystem "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs/spec/filesystemconnect"
)

type filesystemHandler struct {
	filesystemconnect.UnimplementedFilesystemHandler
	mu          sync.Mutex
	files       map[string][]byte
	directories map[string]bool
	calls       []string
}

func (h *filesystemHandler) Stat(_ context.Context, r *connect.Request[filesystem.StatRequest]) (*connect.Response[filesystem.StatResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.files[r.Msg.Path]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("missing file"))
	}
	return connect.NewResponse(&filesystem.StatResponse{Entry: &filesystem.EntryInfo{Path: r.Msg.Path, Size: int64(len(data)), Mode: 0o600}}), nil
}
func (h *filesystemHandler) MakeDir(_ context.Context, r *connect.Request[filesystem.MakeDirRequest]) (*connect.Response[filesystem.MakeDirResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.directories[r.Msg.Path] {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("directory exists"))
	}
	h.directories[r.Msg.Path] = true
	h.calls = append(h.calls, "mkdir:"+r.Msg.Path)
	return connect.NewResponse(&filesystem.MakeDirResponse{Entry: &filesystem.EntryInfo{Path: r.Msg.Path}}), nil
}
func (h *filesystemHandler) Move(_ context.Context, r *connect.Request[filesystem.MoveRequest]) (*connect.Response[filesystem.MoveResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, ok := h.files[r.Msg.Source]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("missing source"))
	}
	h.files[r.Msg.Destination] = data
	delete(h.files, r.Msg.Source)
	h.calls = append(h.calls, "move:"+r.Msg.Source+":"+r.Msg.Destination)
	return connect.NewResponse(&filesystem.MoveResponse{Entry: &filesystem.EntryInfo{Path: r.Msg.Destination}}), nil
}
func (h *filesystemHandler) Remove(_ context.Context, r *connect.Request[filesystem.RemoveRequest]) (*connect.Response[filesystem.RemoveResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, r.Msg.Path)
	delete(h.directories, r.Msg.Path)
	h.calls = append(h.calls, "remove:"+r.Msg.Path)
	return connect.NewResponse(&filesystem.RemoveResponse{}), nil
}
func newFilesystemServer(t *testing.T) (*Client, *filesystemHandler) {
	t.Helper()
	h := &filesystemHandler{files: map[string][]byte{}, directories: map[string]bool{}}
	_, rpc := filesystemconnect.NewFilesystemHandler(h)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "token", r.Header.Get("X-Access-Token"))
		assert.Equal(t, "Basic YWxpY2U6", r.Header.Get("Authorization"))
		if strings.HasPrefix(r.URL.Path, "/filesystem.Filesystem/") {
			rpc.ServeHTTP(w, r)
			return
		}
		assert.Equal(t, "/files", r.URL.Path)
		assert.Equal(t, "alice", r.URL.Query().Get("username"))
		path := r.URL.Query().Get("path")
		switch r.Method {
		case http.MethodPost:
			reader, err := r.MultipartReader()
			if !assert.NoError(t, err) {
				http.Error(w, "multipart", 400)
				return
			}
			part, err := reader.NextPart()
			if !assert.NoError(t, err) {
				http.Error(w, "part", 400)
				return
			}
			assert.Equal(t, "file", part.FormName())
			assert.Equal(t, "upload", part.FileName())
			data, err := io.ReadAll(part)
			assert.NoError(t, err)
			_, err = reader.NextPart()
			assert.ErrorIs(t, err, io.EOF)
			h.mu.Lock()
			h.files[path] = data
			h.mu.Unlock()
			_, _ = w.Write([]byte(`[{"path":"written"}]`))
		case http.MethodGet:
			h.mu.Lock()
			data, ok := h.files[path]
			h.mu.Unlock()
			if !ok {
				http.Error(w, "missing file", 404)
				return
			}
			http.ServeContent(w, r, "download", time.Time{}, bytes.NewReader(data))
		default:
			t.Errorf("unexpected method %s", r.Method)
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(server.Close)
	headers := http.Header{"Authorization": {"Basic YWxpY2U6"}, "X-Access-Token": {"token"}, "Content-Type": {"wrong"}, "Range": {"bytes=10-20"}, "Accept-Encoding": {"gzip"}, "Connect-Timeout-Ms": {"1"}}
	client, err := NewClient(server.URL, server.Client(), headers)
	require.NoError(t, err)
	headers.Set("X-Access-Token", "changed")
	return client, h
}
func TestFilesystemProtocolLifecycle(t *testing.T) {
	c, h := newFilesystemServer(t)
	ctx := context.Background()
	require.NoError(t, c.MakeDir(ctx, "/work"))
	require.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(c.MakeDir(ctx, "/work")))
	for _, data := range [][]byte{nil, []byte("\x00中文\n__E2B_B64_END__\n"), bytes.Repeat([]byte{0xff, 0x00, 0x0a}, 100000)} {
		path := "/work/a ?#&+\n中文.bin"
		require.NoError(t, c.Write(ctx, path, bytes.NewReader(data)))
		info, err := c.Stat(ctx, path)
		require.NoError(t, err)
		assert.Equal(t, int64(len(data)), info.Size)
		assert.EqualValues(t, 0o600, info.Mode)
		for _, limit := range []int64{1, 17, int64(len(data)) + 1} {
			got, err := c.Read(ctx, path, limit)
			require.NoError(t, err)
			assert.Equal(t, int64(len(data)), got.Size)
			assert.True(t, bytes.Equal(data[:min(int64(len(data)), limit)], got.Content))
		}
		require.NoError(t, c.Write(ctx, "/replacement", strings.NewReader("old")))
		require.NoError(t, c.Move(ctx, path, "/replacement"))
		_, err = c.Stat(ctx, path)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
		require.NoError(t, c.Remove(ctx, "/replacement"))
		require.NoError(t, c.Remove(ctx, "/replacement"))
	}
	require.NoError(t, c.Remove(ctx, "/work"))
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Empty(t, h.files)
	assert.Empty(t, h.directories)
}
func TestConcurrentFiles(t *testing.T) {
	c, _ := newFilesystemServer(t)
	for i := 0; i < 8; i++ {
		i := i
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			path := fmt.Sprintf("/file-%d", i)
			require.NoError(t, c.Write(context.Background(), path, strings.NewReader(path)))
			r, err := c.Read(context.Background(), path, 100)
			require.NoError(t, err)
			assert.Equal(t, path, string(r.Content))
		})
	}
}
func TestFilesystemErrorsAndNoRetry(t *testing.T) {
	c, _ := newFilesystemServer(t)
	_, err := c.Read(context.Background(), "/missing", 10)
	var status *HTTPError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, 404, status.StatusCode)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(c.Move(context.Background(), "/missing", "/dest")))
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = w.Write([]byte(strings.Repeat("x", 8192)))
	}))
	defer srv.Close()
	c, err = NewClient(srv.URL, srv.Client(), nil)
	require.NoError(t, err)
	err = c.Write(context.Background(), "/file", strings.NewReader("data"))
	require.ErrorAs(t, err, &status)
	assert.Equal(t, 503, status.StatusCode)
	assert.Len(t, status.Body, 4096)
	assert.EqualValues(t, 1, calls.Load())
}
func TestClientValidation(t *testing.T) {
	for _, base := range []string{"invalid", "https://u:p@example.com", "http://remote.test", "ftp://localhost", "https://example.com/path", "https://example.com?q=x", "https://example.com#x"} {
		_, err := NewClient(base, nil, nil)
		require.Error(t, err, base)
	}
	_, err := NewClient("http://localhost:1234", nil, http.Header{"X-Access-Token": {"secret"}})
	require.ErrorContains(t, err, "HTTPS")
	_, err = NewClient("https://remote.test", nil, http.Header{"Authorization": {"Basic bad"}})
	require.ErrorContains(t, err, "invalid Basic")
	c, err := NewClient("http://localhost:1234", nil, nil)
	require.NoError(t, err)
	require.Error(t, c.Write(nil, "/file", strings.NewReader("data")))
	require.Error(t, c.Write(context.Background(), "/file", nil))
	require.Error(t, c.Remove(context.Background(), ""))
	require.Error(t, c.Move(context.Background(), "/file", "bad\x00path"))
	_, err = c.Read(context.Background(), "/file", 0)
	require.Error(t, err)
	var zero Client
	require.Error(t, zero.Remove(context.Background(), "/file"))
	var absent *Client
	require.Error(t, absent.Remove(context.Background(), "/file"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.Write(ctx, "/file", strings.NewReader("x")), context.Canceled)
	require.ErrorIs(t, c.MakeDir(ctx, "/dir"), context.Canceled)
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *countedBody) Close() error { b.closed = true; return nil }
func TestReadResponseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		status                        int
		length                        int64
		cr, encoding, body, wantError string
		wantSize                      int64
	}{
		{name: "ignored range", status: 200, length: 100, body: strings.Repeat("x", 100), wantSize: 100},
		{name: "range", status: 206, length: 4, cr: "bytes 0-3/100", body: "1234", wantSize: 100},
		{name: "short response", status: 200, length: 4, body: "12", wantError: "unexpected EOF"},
		{name: "wrong offset", status: 206, length: 4, cr: "bytes 1-4/100", body: "1234", wantError: "content range"},
		{name: "missing total", status: 206, length: 4, cr: "bytes 0-3/*", body: "1234", wantError: "content range"},
		{name: "wrong size", status: 206, length: 4, cr: "bytes 0-3/3", body: "1234", wantError: "content range"},
		{name: "gzip", status: 200, length: 4, encoding: "gzip", body: "1234", wantError: "content encoding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(tc.body)}
			supplied := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, "bytes=0-3", r.Header.Get("Range"))
				assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				return &http.Response{StatusCode: tc.status, ContentLength: tc.length, Header: http.Header{"Content-Range": {tc.cr}, "Content-Encoding": {tc.encoding}}, Body: body}, nil
			})}
			c, err := NewClient("https://envd.test", supplied, nil)
			require.NoError(t, err)
			result, err := c.Read(context.Background(), "/file", 4)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantSize, result.Size)
			}
			assert.LessOrEqual(t, body.read, 4)
			assert.True(t, body.closed)
		})
	}
}
func TestReadUnknownLengthUsesStat(t *testing.T) {
	h := &filesystemHandler{files: map[string][]byte{"/file": []byte("abcdef")}}
	_, rpc := filesystemconnect.NewFilesystemHandler(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files" {
			rpc.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer srv.Close()
	transport := &http.Transport{MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	c, err := NewClient(srv.URL, &http.Client{Transport: transport, Timeout: 5 * time.Second}, nil)
	require.NoError(t, err)
	got, err := c.Read(context.Background(), "/file", 3)
	require.NoError(t, err)
	assert.EqualValues(t, 6, got.Size)
	assert.Equal(t, "abc", string(got.Content))
}

type symlinkHandler struct {
	filesystemconnect.UnimplementedFilesystemHandler
	target string
}

func (h *symlinkHandler) Stat(_ context.Context, req *connect.Request[filesystem.StatRequest]) (*connect.Response[filesystem.StatResponse], error) {
	entry := &filesystem.EntryInfo{Size: 6}
	if req.Msg.Path == "/work/link" {
		entry.Size = int64(len(h.target))
		entry.Permissions = "Lrwxrwxrwx"
		if h.target != "" {
			entry.SymlinkTarget = &h.target
		}
	}
	return connect.NewResponse(&filesystem.StatResponse{Entry: entry}), nil
}
func TestReadUnknownLengthFollowsStatSymlink(t *testing.T) {
	for _, target := range []string{"/work/target", "target", "/work/link", ""} {
		t.Run(target, func(t *testing.T) {
			_, rpc := filesystemconnect.NewFilesystemHandler(&symlinkHandler{target: target})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/files" {
					rpc.ServeHTTP(w, r)
					return
				}
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte("abcdef"))
			}))
			defer server.Close()
			c, err := NewClient(server.URL, server.Client(), nil)
			require.NoError(t, err)
			got, err := c.Read(context.Background(), "/work/link", 3)
			if target == "/work/link" || target == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.EqualValues(t, 6, got.Size)
			assert.Equal(t, "abc", string(got.Content))
		})
	}
}

func TestReadCancellationAndTimeout(t *testing.T) {
	for _, bodyPhase := range []bool{false, true} {
		t.Run(fmt.Sprint(bodyPhase), func(t *testing.T) {
			started := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bodyPhase {
					w.Header().Set("Content-Length", "10")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer srv.Close()
			c, err := NewClient(srv.URL, srv.Client(), nil)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := c.Read(ctx, "/file", 10); done <- err }()
			<-started
			cancel()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("read did not stop")
			}
		})
	}
}
func TestRedirectAndClientConfiguration(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/files", 302) }))
	defer source.Close()
	supplied := source.Client()
	supplied.Timeout = time.Second
	var policies atomic.Int32
	supplied.CheckRedirect = func(*http.Request, []*http.Request) error { policies.Add(1); return nil }
	originalTransport := supplied.Transport
	c, err := NewClient(source.URL, supplied, http.Header{"X-Access-Token": {"secret"}})
	require.NoError(t, err)
	_, err = c.Read(context.Background(), "/file", 10)
	require.ErrorContains(t, err, "outside configured origin")
	assert.Zero(t, leaked.Load())
	assert.EqualValues(t, 1, policies.Load())
	assert.Same(t, originalTransport, supplied.Transport)
	assert.Equal(t, time.Second, supplied.Timeout)
	err = c.Write(context.Background(), "/file", strings.NewReader("data"))
	require.ErrorContains(t, err, "redirect of a mutation")
	assert.Zero(t, leaked.Load())
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestWriteReaderFailureAndCancellation(t *testing.T) {
	readErr := errors.New("source read failed")
	var calls int
	c, err := NewClient("https://envd.test", &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		_, err := io.Copy(io.Discard, r.Body)
		return nil, err
	})}, nil)
	require.NoError(t, err)
	require.ErrorIs(t, c.Write(context.Background(), "/file", failingReader{readErr}), readErr)
	assert.Equal(t, 1, calls)

	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c, err = NewClient(server.URL, server.Client(), nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Write(ctx, "/file", strings.NewReader("partial write may have occurred")) }()
	<-started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not stop")
	}
}

func TestUnsupportedFilesystemHasNoFallback(t *testing.T) {
	_, rpc := filesystemconnect.NewFilesystemHandler(&filesystemconnect.UnimplementedFilesystemHandler{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/filesystem.Filesystem/MakeDir", r.URL.Path)
		rpc.ServeHTTP(w, r)
	}))
	defer server.Close()
	c, err := NewClient(server.URL, server.Client(), nil)
	require.NoError(t, err)
	err = c.MakeDir(context.Background(), "/work")
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	assert.EqualValues(t, 1, calls.Load())
}
