//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gwclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

// StreamMessage sends one message to the streaming gateway handler.
func (c *Client) StreamMessage(
	ctx context.Context,
	req MessageRequest,
) (<-chan StreamEvent, error) {
	return c.streamMessage(ctx, req, nil)
}

// StreamMessageWithOptions sends one streaming message with opt-in
// streaming behavior controls.
func (c *Client) StreamMessageWithOptions(
	ctx context.Context,
	req MessageRequest,
	opts *MessageStreamOptions,
) (<-chan StreamEvent, error) {
	return c.streamMessage(ctx, req, opts)
}

func (c *Client) streamMessage(
	ctx context.Context,
	req MessageRequest,
	opts *MessageStreamOptions,
) (<-chan StreamEvent, error) {
	if strings.TrimSpace(c.streamPath) == "" {
		return nil, errors.New("gwclient: empty stream path")
	}

	body, err := json.Marshal(streamMessageRequest{
		MessageRequest: req,
		StreamOptions:  opts,
	})
	if err != nil {
		return nil, fmt.Errorf("gwclient: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		methodPost,
		c.streamPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("gwclient: new request: %w", err)
	}
	httpReq.Header.Set(headerContentType, contentTypeJSON)

	rr := newStreamResponseRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.handler.ServeHTTP(rr, httpReq)
		rr.finish(nil)
	}()

	if err := rr.waitHeader(ctx); err != nil {
		return nil, fmt.Errorf("gwclient: wait stream header: %w", err)
	}

	if rr.Code() != http.StatusOK {
		<-done
		return nil, streamStatusError(rr.Code(), rr.BodyBytes())
	}
	if contentType := rr.Header().Get(headerContentType); !strings.HasPrefix(
		contentType,
		gwproto.SSEContentType,
	) {
		rr.closeReader()
		<-done
		return nil, fmt.Errorf(
			"gwclient: unexpected stream Content-Type %q, want %q",
			contentType,
			gwproto.SSEContentType,
		)
	}

	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		if err := parseSSEStream(ctx, rr.reader(), out); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return
			}
			select {
			case out <- StreamEvent{
				Type: gwproto.StreamEventTypeRunError,
				Error: &APIError{
					Type:    "internal_error",
					Message: err.Error(),
				},
			}:
			case <-ctx.Done():
			}
		}
	}()

	return out, nil
}

type streamMessageRequest struct {
	MessageRequest
	StreamOptions *MessageStreamOptions `json:"stream_options,omitempty"`
}

func streamStatusError(status int, body []byte) error {
	var rsp errorResponse
	_ = json.Unmarshal(body, &rsp)
	if rsp.Error == nil {
		return fmt.Errorf("gwclient: status %d", status)
	}
	return fmt.Errorf(
		"gwclient: status %d: %s: %s",
		status,
		rsp.Error.Type,
		rsp.Error.Message,
	)
}

func parseSSEStream(
	ctx context.Context,
	reader io.Reader,
	out chan<- StreamEvent,
) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	var (
		eventType string
		dataLines []string
	)
	flush := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		var evt StreamEvent
		if err := json.Unmarshal(
			[]byte(strings.Join(dataLines, "\n")),
			&evt,
		); err != nil {
			return fmt.Errorf("gwclient: decode stream event: %w", err)
		}
		if evt.Type == "" && eventType != "" {
			evt.Type = gwproto.StreamEventType(eventType)
		}
		select {
		case out <- evt:
		case <-ctx.Done():
			return ctx.Err()
		}
		eventType = ""
		dataLines = nil
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, gwproto.SSEEventPrefix) {
			eventType = strings.TrimSpace(
				strings.TrimPrefix(line, gwproto.SSEEventPrefix),
			)
			continue
		}
		if strings.HasPrefix(line, gwproto.SSEDataPrefix) {
			dataLines = append(
				dataLines,
				strings.TrimSpace(
					strings.TrimPrefix(line, gwproto.SSEDataPrefix),
				),
			)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

type streamResponseRecorder struct {
	header http.Header

	mu       sync.Mutex
	code     int
	wroteHdr bool

	headerReady chan struct{}
	pipeReader  *io.PipeReader
	pipeWriter  *io.PipeWriter
	body        bytes.Buffer
}

func newStreamResponseRecorder() *streamResponseRecorder {
	reader, writer := io.Pipe()
	return &streamResponseRecorder{
		header:      make(http.Header),
		code:        http.StatusOK,
		headerReady: make(chan struct{}),
		pipeReader:  reader,
		pipeWriter:  writer,
	}
}

func (r *streamResponseRecorder) Header() http.Header {
	return r.header
}

func (r *streamResponseRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wroteHdr {
		return
	}
	r.code = statusCode
	r.wroteHdr = true
	close(r.headerReady)
}

func (r *streamResponseRecorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	if r.Code() == http.StatusOK {
		return r.pipeWriter.Write(b)
	}
	return r.body.Write(b)
}

func (r *streamResponseRecorder) Flush() {}

func (r *streamResponseRecorder) Code() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.code
}

func (r *streamResponseRecorder) BodyBytes() []byte {
	return r.body.Bytes()
}

func (r *streamResponseRecorder) reader() io.Reader {
	return r.pipeReader
}

func (r *streamResponseRecorder) closeReader() {
	_ = r.pipeReader.Close()
}

func (r *streamResponseRecorder) waitHeader(ctx context.Context) error {
	select {
	case <-r.headerReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *streamResponseRecorder) finish(err error) {
	r.WriteHeader(http.StatusOK)
	if r.Code() != http.StatusOK {
		return
	}
	if err != nil {
		_ = r.pipeWriter.CloseWithError(err)
		return
	}
	_ = r.pipeWriter.Close()
}
