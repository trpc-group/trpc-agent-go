//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rewindtest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/rewindtest"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestContractAgainstReferenceService(t *testing.T) {
	service := inmemory.NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	rewindtest.Run(t, service)
	rewindtest.RunAsync(t, service)
}

func TestSoftDeletedSummaryHistoryContract(t *testing.T) {
	base := inmemory.NewSessionService(
		inmemory.WithSummarizer(contractSummarizer{}),
		inmemory.WithAsyncSummaryNum(0),
	)
	t.Cleanup(func() { require.NoError(t, base.Close()) })
	service := &softDeleteHistoryService{
		SessionService: base,
		deleted:        make(map[session.Key]int),
	}

	rewindtest.RunSoftDeletedSummaryHistory(
		t,
		service,
		func(key session.Key) (int, error) {
			return service.deleted[key], nil
		},
	)
}

type softDeleteHistoryService struct {
	*inmemory.SessionService
	deleted map[session.Key]int
}

func (s *softDeleteHistoryService) DeleteSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) error {
	active, err := s.SessionService.GetSession(ctx, key)
	if err != nil {
		return err
	}
	if err := s.SessionService.DeleteSession(ctx, key, options...); err != nil {
		return err
	}
	for _, summary := range active.Summaries {
		if summary != nil {
			s.deleted[key]++
		}
	}
	return nil
}

type contractSummarizer struct{}

func (contractSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (contractSummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	return "summary", nil
}

func (contractSummarizer) SetPrompt(string) {}

func (contractSummarizer) SetModel(model.Model) {}

func (contractSummarizer) Metadata() map[string]any { return nil }
