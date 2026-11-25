//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestEmbeddingTextUsesVisibleNameAndSortedParameters(t *testing.T) {
	decl := &tool.Declaration{
		Name:        "billing_lookup_invoice",
		Description: "Look up an invoice.",
		InputSchema: &tool.Schema{
			Properties: map[string]*tool.Schema{
				"invoice_id": {Type: "string", Description: "Invoice identifier."},
				"archived":   {Type: "boolean", Description: "Include archived invoices."},
			},
		},
	}

	const want = "Tool: lookup_invoice\n" +
		"Description: Look up an invoice.\n" +
		"Parameters: archived (boolean): Include archived invoices., invoice_id (string): Invoice identifier."
	assert.Equal(t, want, EmbeddingText("lookup_invoice", decl))
}
