// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

// yamlUnmarshal decodes YAML into the given value.
func yamlUnmarshal(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}

// jsonUnmarshal decodes JSON into the given value.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
