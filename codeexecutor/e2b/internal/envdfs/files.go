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
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// ReadResult contains a bounded prefix of a remote file. Size is the full file
// size reported by the download response, or by Stat if the response omits it.
// A separate Stat is not a snapshot of a concurrently modified file.
type ReadResult struct {
	Content []byte
	Size    int64
}

// Read returns at most limit bytes; limit must be positive. Range is requested
// but a server ignoring it is supported with a bounded local read. Responses
// must use identity encoding so Size and limits refer to original bytes.
// All response bodies are closed before return. On an interrupted transfer,
// partial content may accompany an error and must not be treated as success.
func (c *Client) Read(ctx context.Context, path string, limit int64) (ReadResult, error) {
	var result ReadResult
	if err := c.validate(ctx, path); err != nil {
		return result, err
	}
	if limit <= 0 {
		return result, errors.New("envd filesystem: read limit must be positive")
	}
	req, err := c.fileRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", limit-1))
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return result, operationError(ctx, "read", err)
	}
	result, err = readDownload(ctx, resp, limit)
	if err != nil {
		return result, err
	}
	// Close the download before another RPC, including with MaxConnsPerHost=1.
	if result.Size < 0 {
		result.Size, err = c.downloadStatSize(ctx, path)
	}
	return result, err
}

func readDownload(ctx context.Context, resp *http.Response, limit int64) (ReadResult, error) {
	var result ReadResult
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return result, operationError(ctx, "read", responseError(resp))
	}
	size, expected, err := downloadSize(resp, limit)
	if err != nil {
		return result, err
	}
	result.Size = size
	result.Content, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return result, operationError(ctx, "read body", err)
	}
	if expected >= 0 && int64(len(result.Content)) < min(expected, limit) {
		return result, operationError(ctx, "read body", io.ErrUnexpectedEOF)
	}
	return result, nil
}

// envd Stat reports the link inode size, whereas GET /files follows links.
// Resolve that distinction before using Stat as download size metadata.
func (c *Client) downloadStatSize(ctx context.Context, filename string) (int64, error) {
	for hops := 0; hops < 40; hops++ {
		entry, err := c.Stat(ctx, filename)
		if err != nil {
			return 0, err
		}
		if entry.SymlinkTarget == nil {
			if entry.Size < 0 || strings.HasPrefix(entry.Permissions, "L") {
				return 0, errors.New("envd filesystem: stat did not report a usable download size")
			}
			return entry.Size, nil
		}
		target := entry.GetSymlinkTarget()
		if target == "" {
			return 0, errors.New("envd filesystem: stat returned an empty symlink target")
		}
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(filename), target)
		}
		filename = target
	}
	return 0, errors.New("envd filesystem: too many symlinks while resolving download size")
}

func downloadSize(resp *http.Response, limit int64) (size, expected int64, err error) {
	encoding := resp.Header.Get("Content-Encoding")
	if encoding != "" && !strings.EqualFold(encoding, "identity") || resp.Uncompressed {
		return 0, 0, errors.New("envd filesystem: unexpected content encoding")
	}
	if resp.StatusCode == http.StatusOK {
		return resp.ContentLength, resp.ContentLength, nil
	}
	value := resp.Header.Get("Content-Range")
	prefix, total, ok := strings.Cut(value, "/")
	rangeValue, hasUnit := strings.CutPrefix(prefix, "bytes 0-")
	end, endErr := strconv.ParseInt(rangeValue, 10, 64)
	size, sizeErr := strconv.ParseInt(total, 10, 64)
	if !ok || !hasUnit || endErr != nil || sizeErr != nil || end < 0 || end >= limit || size <= end || (resp.ContentLength >= 0 && resp.ContentLength != end+1) {
		return 0, 0, errors.New("envd filesystem: invalid content range")
	}
	return size, end + 1, nil
}

// Write uploads a file using multipart HTTP, creating its parents and replacing
// existing content according to envd semantics. It does not preserve or set
// POSIX modes; callers needing specific modes must apply them separately.
// The caller owns content and must supply a reader that returns promptly when
// canceled; Write neither closes it nor starts a background reader goroutine.
// A failed write may leave partial content. Mutations are never retried.
func (c *Client) Write(ctx context.Context, path string, content io.Reader) error {
	if err := c.validate(ctx, path); err != nil {
		return err
	}
	if content == nil {
		return errors.New("envd filesystem: nil content reader")
	}
	var framing bytes.Buffer
	writer := multipart.NewWriter(&framing)
	// The full destination is a query parameter, not a multipart filename.
	if _, err := writer.CreateFormFile("file", "upload"); err != nil {
		return fmt.Errorf("envd filesystem: multipart header: %w", err)
	}
	prefix := bytes.Clone(framing.Bytes())
	framing.Reset()
	if err := writer.Close(); err != nil {
		return fmt.Errorf("envd filesystem: multipart footer: %w", err)
	}
	body := io.MultiReader(bytes.NewReader(prefix), content, bytes.NewReader(framing.Bytes()))
	req, err := c.fileRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return operationError(ctx, "write", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return operationError(ctx, "write", responseError(resp))
	}
	// Bound even a malformed success body. The endpoint acknowledges the write
	// through its status; returned path metadata is not needed by callers.
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return operationError(ctx, "write response", err)
	}
	return nil
}

func (c *Client) fileRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := *c.baseURL
	u.Path = "/files"
	query := u.Query()
	query.Set("path", path)
	if c.username != "" {
		query.Set("username", c.username)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("envd filesystem: request: %w", err)
	}
	c.addHeaders(req.Header)
	return req, nil
}

// HTTPError retains an unsuccessful file transfer's status and bounded response
// text. Use errors.As to inspect it; RPC errors retain their Connect codes.
type HTTPError struct {
	StatusCode int
	Body       string
}

// Error describes an unsuccessful native file transfer.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("envd filesystem: HTTP %d: %s", e.StatusCode, e.Body)
}
func responseError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return errors.Join(&HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}, err)
}
