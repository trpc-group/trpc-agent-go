//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadPolicy reads a safety policy from path.  The file format is
// determined by the extension: .yaml/.yml → YAML, .json → JSON.
// Any other extension defaults to YAML.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("safety: read policy file %q: %w", path, err)
	}

	policy := &Policy{}
	ext := strings.ToLower(path)
	switch {
	case strings.HasSuffix(ext, ".json"):
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		if err := d.Decode(policy); err != nil {
			return nil, fmt.Errorf("safety: parse JSON policy: %w", err)
		}
	default: // .yaml, .yml, or anything else
		if err := yaml.Unmarshal(data, policy); err != nil {
			return nil, fmt.Errorf("safety: parse YAML policy: %w", err)
		}
	}

	if err := policy.validate(); err != nil {
		return nil, fmt.Errorf("safety: invalid policy: %w", err)
	}

	return policy, nil
}

// validate checks the policy for internal consistency.
func (p *Policy) validate() error {
	switch p.DefaultVerdict {
	case "", VerdictAllow, VerdictDeny, VerdictAsk:
		// Empty defaults to allow.
	default:
		return fmt.Errorf("invalid default_verdict %q", p.DefaultVerdict)
	}
	return nil
}
