//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LoadCases reads every *.json replay case in dir (including *.faulty.json),
// returning them sorted by file name for deterministic ordering.
func LoadCases(dir string) ([]*ReplayCase, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	cases := make([]*ReplayCase, 0, len(matches))
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read case %s: %w", path, err)
		}
		var c ReplayCase
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("parse case %s: %w", path, err)
		}
		cases = append(cases, &c)
	}
	return cases, nil
}
