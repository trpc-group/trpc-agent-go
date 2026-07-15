// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadPolicy loads a YAML or JSON policy file and validates it.
func LoadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return ParsePolicy(data, format)
}

// ParsePolicy parses a YAML or JSON policy payload and validates it.
func ParsePolicy(data []byte, format string) (Policy, error) {
	var p Policy
	switch strings.ToLower(format) {
	case "json":
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return Policy{}, err
		}
		if err := rejectTrailingJSON(dec); err != nil {
			return Policy{}, err
		}
	case "yaml", "yml", "":
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&p); err != nil {
			return Policy{}, err
		}
		if err := rejectTrailingYAML(dec); err != nil {
			return Policy{}, err
		}
	default:
		return Policy{}, fmt.Errorf("unsupported policy format %q", format)
	}
	return p.normalized()
}

func rejectTrailingJSON(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("policy contains multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectTrailingYAML(dec *yaml.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("policy contains multiple YAML documents")
		}
		return err
	}
	return nil
}
