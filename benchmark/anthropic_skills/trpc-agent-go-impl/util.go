//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const defaultPreviewChars = 4000

const (
	logTruncateStringChars = 200
	logTruncateArrayItems  = 20
)

func resolveDefaultUnderParent(given string, name string) string {
	if strings.TrimSpace(given) != "" {
		return given
	}
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.Join("..", name)
	}
	return filepath.Join(cwd, "..", name)
}

func setDefaultEnv(key string, val string) {
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return
	}
	_ = os.Setenv(key, val)
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		return 0, fmt.Errorf("unexpected listener address")
	}
	return addr.Port, nil
}

func progressf(enabled bool, format string, args ...any) {
	if !enabled {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func printTextBlock(
	enabled bool,
	debug bool,
	prefix string,
	content string,
	maxChars int,
) {
	if !enabled {
		return
	}
	text := strings.TrimSpace(content)
	if text == "" {
		return
	}

	if pretty, ok := tryPrettyJSON(text, debug); ok {
		text = pretty
	}

	if !debug {
		if clipped, truncated := previewText(text, maxChars); truncated {
			for _, line := range strings.Split(clipped, "\n") {
				progressf(enabled, "%s%s", prefix, line)
			}
			progressf(
				enabled,
				"%s... (truncated, total %d chars)",
				prefix,
				len(text),
			)
			return
		}
	}

	for _, line := range strings.Split(text, "\n") {
		progressf(enabled, "%s%s", prefix, line)
	}
}

func tryPrettyJSON(s string, debug bool) (string, bool) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return "", false
	}
	if !debug {
		v = truncateJSONValue(v)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", false
	}
	return string(b), true
}

func truncateJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = truncateJSONValue(vv)
		}
		return out
	case []any:
		if len(t) <= logTruncateArrayItems {
			out := make([]any, 0, len(t))
			for _, vv := range t {
				out = append(out, truncateJSONValue(vv))
			}
			return out
		}
		out := make([]any, 0, logTruncateArrayItems+1)
		for i := 0; i < logTruncateArrayItems; i++ {
			out = append(out, truncateJSONValue(t[i]))
		}
		out = append(out, fmt.Sprintf(
			"... (truncated, total %d items)",
			len(t),
		))
		return out
	case string:
		if len(t) <= logTruncateStringChars {
			return t
		}
		return fmt.Sprintf(
			"%s... (truncated, total %d chars)",
			t[:logTruncateStringChars],
			len(t),
		)
	default:
		return v
	}
}

func previewText(s string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(s) <= maxChars {
		return s, false
	}
	return s[:maxChars], true
}

func must(err error, what string) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
