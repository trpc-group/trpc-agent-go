//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package searchfilter provides search and filter functionality for trpc-agent-go.
package searchfilter

// PGConverter converts a filter condition to an Elasticsearch query.
type PGConverter struct{}

// Convert converts a filter condition to an Elasticsearch query filter.
func (c *PGConverter) Convert(filter *UniversalFilterCondition) (any, error) {
	if filter == nil {
		return nil, nil
	}

	return nil, nil
}
