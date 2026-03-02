//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skills

import (
	"errors"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillFileName = "SKILL.md"

	openClawMetadataKey = "openclaw"

	openClawBaseDirPlaceholder = "{baseDir}"
)

var errNoFrontMatter = errors.New("no yaml front matter")

type parsedFrontMatter struct {
	Name        string
	Description string
	Metadata    map[string]any
}

type openClawMetadata struct {
	Always   bool             `yaml:"always"`
	OS       []string         `yaml:"os"`
	Requires openClawRequires `yaml:"requires"`
}

type openClawRequires struct {
	Bins    []string `yaml:"bins"`
	AnyBins []string `yaml:"anyBins"`
	Env     []string `yaml:"env"`
	Config  []string `yaml:"config"`
}

func parseFrontMatterFile(path string) (parsedFrontMatter, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return parsedFrontMatter{}, err
	}
	return parseFrontMatter(string(b))
}

func parseFrontMatter(content string) (parsedFrontMatter, error) {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return parsedFrontMatter{}, errNoFrontMatter
	}

	idx := strings.Index(text[4:], "\n---\n")
	if idx < 0 {
		return parsedFrontMatter{}, errNoFrontMatter
	}

	raw := text[4 : 4+idx]
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &m); err != nil {
		return parsedFrontMatter{}, err
	}

	out := parsedFrontMatter{
		Name:        strings.TrimSpace(asString(m["name"])),
		Description: strings.TrimSpace(asString(m["description"])),
		Metadata:    normalizeStringAnyMap(m["metadata"]),
	}
	return out, nil
}

func parseOpenClawMetadata(
	fm parsedFrontMatter,
) (openClawMetadata, bool, error) {
	if len(fm.Metadata) == 0 {
		return openClawMetadata{}, false, nil
	}
	raw, ok := fm.Metadata[openClawMetadataKey]
	if !ok {
		return openClawMetadata{}, false, nil
	}

	b, err := yaml.Marshal(raw)
	if err != nil {
		return openClawMetadata{}, false, err
	}
	var meta openClawMetadata
	if err := yaml.Unmarshal(b, &meta); err != nil {
		return openClawMetadata{}, false, err
	}
	return meta, true, nil
}

func asString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func normalizeStringAnyMap(v any) map[string]any {
	switch typed := v.(type) {
	case map[string]any:
		return typed
	case map[any]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			out[ks] = val
		}
		return out
	default:
		return nil
	}
}
