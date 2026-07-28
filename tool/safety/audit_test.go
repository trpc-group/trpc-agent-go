//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingAuditWriter struct {
	err error
}

func (w failingAuditWriter) WriteAuditEvent(context.Context, AuditEvent) error {
	if w.err != nil {
		return w.err
	}
	return errors.New("audit write failed")
}

func TestNormalizeReportText_PreservesRecommendationProse(t *testing.T) {
	report := normalizeReportText(Report{
		Recommendation: "do not read credential or secret files; remove /etc/passwd",
		Findings: []Finding{{
			Recommendation: "do not collect credentials from ~/.ssh/id_rsa",
		}},
	}, DefaultPolicy().DeniedPaths)

	require.Equal(
		t,
		"do not read credential or secret files; remove <redacted>",
		report.Recommendation,
	)
	require.Equal(
		t,
		"do not collect credentials from <redacted>",
		report.Findings[0].Recommendation,
	)
	require.True(t, report.Redacted)
	require.True(t, report.Findings[0].Redacted)
}
