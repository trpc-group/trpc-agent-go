//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch client interface and implementation.
package elasticsearch

import (
	ielasticsearch "trpc.group/trpc-go/trpc-agent-go/internal/storage/elasticsearch"
)

// Client is the Elasticsearch client interface.
type Client = ielasticsearch.Client
