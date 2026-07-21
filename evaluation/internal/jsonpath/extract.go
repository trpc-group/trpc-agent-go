//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package jsonpath extracts values with a restricted JSON path subset.
package jsonpath

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type segment struct {
	key   *string
	index *int
}

// Extract extracts a rendered value from raw JSON using a restricted path subset.
func Extract(rawValue, path string) (string, error) {
	var value any
	if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
		return "", fmt.Errorf("parse source json for path %q: %w", path, err)
	}
	segments, err := parse(path)
	if err != nil {
		return "", err
	}
	current := value
	for _, segment := range segments {
		if segment.key != nil {
			object, ok := current.(map[string]any)
			if !ok {
				return "", fmt.Errorf("json path %q expects object before key %q", path, *segment.key)
			}
			next, ok := object[*segment.key]
			if !ok {
				return "", fmt.Errorf("json path %q key %q not found", path, *segment.key)
			}
			current = next
			continue
		}
		array, ok := current.([]any)
		if !ok {
			return "", fmt.Errorf("json path %q expects array before index %d", path, *segment.index)
		}
		if *segment.index < 0 || *segment.index >= len(array) {
			return "", fmt.Errorf("json path %q index %d out of range", path, *segment.index)
		}
		current = array[*segment.index]
	}
	return renderValue(current)
}

func parse(path string) ([]segment, error) {
	if path == "" {
		return nil, nil
	}
	i := 0
	if path[0] == '$' {
		i = 1
		if len(path) > 1 && path[1] != '.' && path[1] != '[' {
			return nil, fmt.Errorf("json path %q has invalid root selector", path)
		}
	}
	segments := make([]segment, 0)
	for i < len(path) {
		switch path[i] {
		case '.':
			i++
			key, next, err := parseKey(path, i)
			if err != nil {
				return nil, err
			}
			segments = append(segments, segment{key: &key})
			i = next
		case '[':
			index, next, err := parseIndex(path, i)
			if err != nil {
				return nil, err
			}
			segments = append(segments, segment{index: &index})
			i = next
		default:
			key, next, err := parseKey(path, i)
			if err != nil {
				return nil, err
			}
			segments = append(segments, segment{key: &key})
			i = next
		}
	}
	return segments, nil
}

func parseKey(path string, start int) (string, int, error) {
	if start >= len(path) {
		return "", start, fmt.Errorf("json path %q missing key", path)
	}
	i := start
	for i < len(path) && path[i] != '.' && path[i] != '[' {
		if path[i] == ']' {
			return "", i, fmt.Errorf("json path %q has unexpected ]", path)
		}
		i++
	}
	if i == start {
		return "", i, fmt.Errorf("json path %q missing key", path)
	}
	key := path[start:i]
	if strings.Contains(key, "*") {
		return "", i, fmt.Errorf("json path %q contains unsupported wildcard", path)
	}
	return key, i, nil
}

func parseIndex(path string, start int) (int, int, error) {
	end := strings.IndexByte(path[start:], ']')
	if end < 0 {
		return 0, start, fmt.Errorf("json path %q missing ]", path)
	}
	rawIndex := path[start+1 : start+end]
	if rawIndex == "" {
		return 0, start, fmt.Errorf("json path %q missing index", path)
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil {
		return 0, start, fmt.Errorf("json path %q has invalid index %q", path, rawIndex)
	}
	return index, start + end + 1, nil
}

func renderValue(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case nil:
		return "null", nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal json path value: %w", err)
		}
		return string(raw), nil
	}
}
