//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bufio"
	"bytes"
	"io"

	"github.com/openai/openai-go/packages/ssestream"
)

func init() {
	ssestream.RegisterDecoder("text/event-stream", newTolerantEventStreamDecoder)
	ssestream.RegisterDecoder("text/event-stream; charset=utf-8", newTolerantEventStreamDecoder)
	ssestream.RegisterDecoder("text/event-stream;charset=utf-8", newTolerantEventStreamDecoder)
}

func newTolerantEventStreamDecoder(rc io.ReadCloser) ssestream.Decoder {
	scn := bufio.NewScanner(rc)
	scn.Buffer(nil, bufio.MaxScanTokenSize<<9)
	return &tolerantEventStreamDecoder{rc: rc, scn: scn}
}

// tolerantEventStreamDecoder implements SSE parsing for OpenAI-compatible
// streams while ignoring provider-specific keep-alive or progress events that
// do not carry JSON payloads. Some OpenAI-compatible vendors emit events such
// as "data: : keep-alive" or "data: : OPENROUTER PROCESSING"; those events are
// not chat chunks and should not terminate the stream.
type tolerantEventStreamDecoder struct {
	evt ssestream.Event
	rc  io.ReadCloser
	scn *bufio.Scanner
	err error
}

func (s *tolerantEventStreamDecoder) Next() bool {
	if s.err != nil {
		return false
	}

	event := ""
	data := bytes.NewBuffer(nil)

	for s.scn.Scan() {
		txt := s.scn.Bytes()

		// Dispatch event on an empty line.
		if len(txt) == 0 {
			if shouldSkipSSEPayload(data.Bytes()) {
				event = ""
				data.Reset()
				continue
			}
			payload := bytes.TrimSuffix(data.Bytes(), []byte("\n"))
			payload = append([]byte(nil), payload...)
			s.evt = ssestream.Event{
				Type: event,
				Data: payload,
			}
			return true
		}

		// Split a string like "event: bar" into name="event" and value=" bar".
		name, value, _ := bytes.Cut(txt, []byte(":"))

		// Consume an optional space after the colon if it exists.
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}

		switch string(name) {
		case "":
			// A line starting with ":" is an SSE comment.
			continue
		case "event":
			event = string(value)
		case "data":
			_, s.err = data.Write(value)
			if s.err != nil {
				break
			}
			_, s.err = data.WriteRune('\n')
			if s.err != nil {
				break
			}
		}
	}

	if s.scn.Err() != nil {
		s.err = s.scn.Err()
	}

	return false
}

func (s *tolerantEventStreamDecoder) Event() ssestream.Event {
	return s.evt
}

func (s *tolerantEventStreamDecoder) Close() error {
	return s.rc.Close()
}

func (s *tolerantEventStreamDecoder) Err() error {
	return s.err
}

func shouldSkipSSEPayload(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return true
	}
	if bytes.HasPrefix(trimmed, []byte(":")) {
		return true
	}
	if bytes.HasPrefix(trimmed, []byte("[DONE]")) {
		return false
	}
	switch trimmed[0] {
	case '{', '[':
		return false
	default:
		return true
	}
}
