//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReviewStoreContractIsConsumerOwned(t *testing.T) {
	var store ReviewStore = contractStore{}
	require.NotNil(t, store)

	completion := Completion{
		TaskID:               "task-1",
		UpdatedAt:            time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC),
		PublicationArtifacts: []ArtifactRecord{{Name: "review_report.json"}},
		Report: ReportMetadata{
			SchemaVersion:             SchemaVersion,
			TaskID:                    "task-1",
			Digest:                    "sha256:report",
			JSONArtifactReference:     "artifact://review_report.json/1",
			MarkdownArtifactReference: "artifact://review_report.md/1",
		},
	}
	require.Equal(t, completion.TaskID, completion.Report.TaskID)
	require.Equal(t, "review_report.json", completion.PublicationArtifacts[0].Name)
	stored := StoredReview{PublicationArtifacts: completion.PublicationArtifacts}
	require.Equal(t, completion.PublicationArtifacts, stored.PublicationArtifacts)
}

type contractStore struct{}

func (contractStore) CreateTask(context.Context, Task) error { return nil }

func (contractStore) TransitionPhase(context.Context, string, Phase, time.Time) error { return nil }

func (contractStore) RecordSandboxRun(context.Context, SandboxRun) error { return nil }

func (contractStore) RecordGovernanceDecision(context.Context, GovernanceDecision) error { return nil }

func (contractStore) FailTask(context.Context, string, Phase, string, time.Time) error { return nil }

func (contractStore) CancelTask(context.Context, string, Phase, string, time.Time) error { return nil }

func (contractStore) CompleteTask(context.Context, Completion) error { return nil }

func (contractStore) GetReview(context.Context, string) (StoredReview, error) {
	return StoredReview{}, nil
}
