//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- Tests for options ---

func TestWithEmbedder(t *testing.T) {
	emb := &mockEmbedder{}
	opt := WithEmbedder(emb)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, emb, opts.embedder)
}

func TestWithIndexDimension(t *testing.T) {
	opt := WithIndexDimension(768)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 768, opts.indexDimension)
}

func TestWithIndexDimension_Zero(t *testing.T) {
	opt := WithIndexDimension(0)
	opts := ServiceOpts{indexDimension: 1536}
	opt(&opts)
	// Should not change when dim <= 0.
	assert.Equal(t, 1536, opts.indexDimension)
}

func TestWithMaxResults(t *testing.T) {
	opt := WithMaxResults(10)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 10, opts.maxResults)
}

func TestWithMaxResults_Zero(t *testing.T) {
	opt := WithMaxResults(0)
	opts := ServiceOpts{maxResults: 5}
	opt(&opts)
	// Should not change when n <= 0.
	assert.Equal(t, 5, opts.maxResults)
}

func TestWithHNSWM(t *testing.T) {
	opt := WithHNSWM(32)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 32, opts.hnswM)
}

func TestWithHNSWM_Zero(t *testing.T) {
	opt := WithHNSWM(0)
	opts := ServiceOpts{hnswM: 16}
	opt(&opts)
	assert.Equal(t, 16, opts.hnswM)
}

func TestWithHNSWEfConstruction(t *testing.T) {
	opt := WithHNSWEfConstruction(400)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 400, opts.hnswEf)
}

func TestWithHNSWEfConstruction_Zero(t *testing.T) {
	opt := WithHNSWEfConstruction(0)
	opts := ServiceOpts{hnswEf: 200}
	opt(&opts)
	assert.Equal(t, 200, opts.hnswEf)
}

func TestWithPostgresClientDSN(t *testing.T) {
	const dsn = "postgres://user:pass@host:5432/db"
	opt := WithPostgresClientDSN(dsn)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, dsn, opts.dsn)
}

func TestWithHost(t *testing.T) {
	opt := WithHost("dbhost")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "dbhost", opts.host)
}

func TestWithPort(t *testing.T) {
	opt := WithPort(5433)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 5433, opts.port)
}

func TestWithDatabase(t *testing.T) {
	opt := WithDatabase("mydb")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "mydb", opts.database)
}

func TestWithSSLMode(t *testing.T) {
	opt := WithSSLMode("require")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "require", opts.sslMode)
}

func TestWithSkipDBInit(t *testing.T) {
	opt := WithSkipDBInit(true)
	opts := ServiceOpts{}
	opt(&opts)
	assert.True(t, opts.skipDBInit)
}

func TestWithSoftDelete(t *testing.T) {
	opt := WithSoftDelete(false)
	opts := ServiceOpts{softDelete: true}
	opt(&opts)
	assert.False(t, opts.softDelete)
}

// --- Tests for buildConnString ---

func TestBuildConnString_AllFields(t *testing.T) {
	opts := ServiceOpts{
		host:     "myhost",
		port:     5433,
		database: "mydb",
		user:     "admin",
		password: "secret",
		sslMode:  "require",
	}
	s := buildConnString(opts)
	assert.Contains(t, s, "host=myhost")
	assert.Contains(t, s, "port=5433")
	assert.Contains(t, s, "dbname=mydb")
	assert.Contains(t, s, "sslmode=require")
	assert.Contains(t, s, "user=admin")
	assert.Contains(t, s, "password=secret")
}

func TestBuildConnString_Defaults(t *testing.T) {
	opts := ServiceOpts{}
	s := buildConnString(opts)
	assert.Contains(t, s,
		fmt.Sprintf("host=%s", defaultHost))
	assert.Contains(t, s,
		fmt.Sprintf("port=%d", defaultPort))
	assert.Contains(t, s,
		fmt.Sprintf("dbname=%s", defaultDatabase))
	assert.Contains(t, s,
		fmt.Sprintf("sslmode=%s", defaultSSLMode))
	// No user/password.
	assert.NotContains(t, s, "user=")
	assert.NotContains(t, s, "password=")
}

func TestBuildConnString_NoCredentials(t *testing.T) {
	opts := ServiceOpts{
		host:     "host1",
		port:     5432,
		database: "db1",
		sslMode:  "disable",
	}
	s := buildConnString(opts)
	assert.NotContains(t, s, "user=")
	assert.NotContains(t, s, "password=")
}

// --- Tests for defaultOptions ---

func TestDefaultOptions(t *testing.T) {
	assert.Equal(t, defaultIndexDimension,
		defaultOptions.indexDimension)
	assert.Equal(t, defaultMaxResults,
		defaultOptions.maxResults)
	assert.Equal(t, defaultHNSWM,
		defaultOptions.hnswM)
	assert.Equal(t, defaultHNSWEf,
		defaultOptions.hnswEf)
	assert.Equal(t, defaultHybridRRFK,
		defaultOptions.hybridRRFK)
	assert.Equal(t, defaultCandidateRatio,
		defaultOptions.candidateRatio)
	assert.True(t, defaultOptions.softDelete)
}

func TestWithSyncIndexing(t *testing.T) {
	opt := WithSyncIndexing(true)
	opts := ServiceOpts{}
	opt(&opts)
	assert.True(t, opts.syncIndexing)
}

func TestWithIndexTextBuilder(t *testing.T) {
	builder := func(
		sess *session.Session,
		_ *event.Event,
		baseText string,
		role model.Role,
	) string {
		return fmt.Sprintf("%s:%s:%s", sess.ID, role, baseText)
	}
	opt := WithIndexTextBuilder(builder)
	opts := ServiceOpts{}
	opt(&opts)
	require.NotNil(t, opts.indexTextBuilder)
	got := opts.indexTextBuilder(
		&session.Session{ID: "sess-1"},
		nil,
		"hello",
		model.RoleAssistant,
	)
	assert.Equal(t, "sess-1:assistant:hello", got)
}

func TestWithHybridRRFK(t *testing.T) {
	opt := WithHybridRRFK(42)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 42, opts.hybridRRFK)
}

func TestWithHybridRRFK_Zero(t *testing.T) {
	opt := WithHybridRRFK(0)
	opts := ServiceOpts{hybridRRFK: defaultHybridRRFK}
	opt(&opts)
	assert.Equal(t, defaultHybridRRFK, opts.hybridRRFK)
}

func TestWithHybridCandidateRatio(t *testing.T) {
	opt := WithHybridCandidateRatio(4)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 4, opts.candidateRatio)
}

func TestWithHybridCandidateRatio_Zero(t *testing.T) {
	opt := WithHybridCandidateRatio(0)
	opts := ServiceOpts{candidateRatio: defaultCandidateRatio}
	opt(&opts)
	assert.Equal(t, defaultCandidateRatio, opts.candidateRatio)
}

// --- Tests for WithTablePrefix ---

func TestWithTablePrefix_AddsUnderscore(t *testing.T) {
	opt := WithTablePrefix("myapp")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "myapp_", opts.tablePrefix)
}

func TestWithTablePrefix_KeepsUnderscore(t *testing.T) {
	opt := WithTablePrefix("myapp_")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "myapp_", opts.tablePrefix)
}

func TestWithTablePrefix_Empty(t *testing.T) {
	opt := WithTablePrefix("")
	opts := ServiceOpts{tablePrefix: "old_"}
	opt(&opts)
	assert.Empty(t, opts.tablePrefix)
}

// --- Tests for WithExtraOptions ---

func TestWithExtraOptions(t *testing.T) {
	opt := WithExtraOptions("opt1", 42)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Len(t, opts.extraOptions, 2)
}

// --- Tests for remaining option functions ---

func TestWithUser(t *testing.T) {
	opt := WithUser("admin")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "admin", opts.user)
}

func TestWithPassword(t *testing.T) {
	opt := WithPassword("s3cret")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "s3cret", opts.password)
}

func TestWithPostgresInstance(t *testing.T) {
	opt := WithPostgresInstance("myinst")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "myinst", opts.instanceName)
}

func TestWithSessionEventLimit(t *testing.T) {
	opt := WithSessionEventLimit(500)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 500, opts.sessionEventLimit)
}

func TestWithSessionTTL(t *testing.T) {
	opt := WithSessionTTL(10 * time.Minute)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 10*time.Minute, opts.sessionTTL)
}

func TestWithAppStateTTL(t *testing.T) {
	opt := WithAppStateTTL(30 * time.Minute)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 30*time.Minute, opts.appStateTTL)
}

func TestWithUserStateTTL(t *testing.T) {
	opt := WithUserStateTTL(1 * time.Hour)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 1*time.Hour, opts.userStateTTL)
}

func TestWithEnableAsyncPersist(t *testing.T) {
	opt := WithEnableAsyncPersist(true)
	opts := ServiceOpts{}
	opt(&opts)
	assert.True(t, opts.enableAsyncPersist)
}

func TestWithAsyncPersisterNum(t *testing.T) {
	opt := WithAsyncPersisterNum(20)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 20, opts.asyncPersisterNum)
}

func TestWithAsyncPersisterNum_ZeroUsesDefault(
	t *testing.T,
) {
	opt := WithAsyncPersisterNum(0)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t,
		defaultAsyncPersisterNum, opts.asyncPersisterNum,
	)
}

func TestWithSummarizer(t *testing.T) {
	summ := &mockSummarizer{}
	opt := WithSummarizer(summ)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, summ, opts.summarizer)
}

func TestWithAsyncSummaryNum(t *testing.T) {
	opt := WithAsyncSummaryNum(5)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 5, opts.asyncSummaryNum)
}

func TestWithAsyncSummaryNum_ZeroUsesDefault(
	t *testing.T,
) {
	opt := WithAsyncSummaryNum(0)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t,
		defaultAsyncSummaryNum, opts.asyncSummaryNum,
	)
}

func TestWithSummaryQueueSize(t *testing.T) {
	opt := WithSummaryQueueSize(200)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 200, opts.summaryQueueSize)
}

func TestWithSummaryQueueSize_ZeroUsesDefault(
	t *testing.T,
) {
	opt := WithSummaryQueueSize(0)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t,
		defaultSummaryQueueSize, opts.summaryQueueSize,
	)
}

func TestWithSummaryJobTimeout(t *testing.T) {
	opt := WithSummaryJobTimeout(2 * time.Minute)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 2*time.Minute, opts.summaryJobTimeout)
}

func TestWithSummaryJobTimeout_ZeroIgnored(t *testing.T) {
	opt := WithSummaryJobTimeout(0)
	opts := ServiceOpts{
		summaryJobTimeout: defaultSummaryJobTimeout,
	}
	opt(&opts)
	assert.Equal(t,
		defaultSummaryJobTimeout, opts.summaryJobTimeout,
	)
}

func TestWithSummaryJobTimeout_NegativeIgnored(
	t *testing.T,
) {
	opt := WithSummaryJobTimeout(-1 * time.Second)
	opts := ServiceOpts{
		summaryJobTimeout: defaultSummaryJobTimeout,
	}
	opt(&opts)
	assert.Equal(t,
		defaultSummaryJobTimeout, opts.summaryJobTimeout,
	)
}

func TestWithCleanupInterval(t *testing.T) {
	opt := WithCleanupInterval(10 * time.Minute)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, 10*time.Minute, opts.cleanupInterval)
}

func TestWithSchema(t *testing.T) {
	opt := WithSchema("custom_schema")
	opts := ServiceOpts{}
	opt(&opts)
	assert.Equal(t, "custom_schema", opts.schema)
}

func TestWithSchema_Empty(t *testing.T) {
	opt := WithSchema("")
	opts := ServiceOpts{schema: "old"}
	opt(&opts)
	assert.Empty(t, opts.schema)
}

func TestWithAppendEventHook(t *testing.T) {
	hook := session.AppendEventHook(
		func(
			_ *session.AppendEventContext,
			next func() error,
		) error {
			return next()
		},
	)
	opt := WithAppendEventHook(hook)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Len(t, opts.appendEventHooks, 1)
}

func TestWithGetSessionHook(t *testing.T) {
	hook := session.GetSessionHook(
		func(
			_ *session.GetSessionContext,
			next func() (*session.Session, error),
		) (*session.Session, error) {
			return next()
		},
	)
	opt := WithGetSessionHook(hook)
	opts := ServiceOpts{}
	opt(&opts)
	assert.Len(t, opts.getSessionHooks, 1)
}

// --- Tests for defaultOptions completeness ---

func TestDefaultOptions_AllFields(t *testing.T) {
	assert.Equal(t, defaultSessionEventLimit,
		defaultOptions.sessionEventLimit)
	assert.Equal(t, defaultAsyncPersisterNum,
		defaultOptions.asyncPersisterNum)
	assert.False(t, defaultOptions.enableAsyncPersist)
	assert.Equal(t, defaultAsyncSummaryNum,
		defaultOptions.asyncSummaryNum)
	assert.Equal(t, defaultSummaryQueueSize,
		defaultOptions.summaryQueueSize)
	assert.Equal(t, defaultSummaryJobTimeout,
		defaultOptions.summaryJobTimeout)
	assert.True(t, defaultOptions.softDelete)
	assert.Equal(t, defaultIndexDimension,
		defaultOptions.indexDimension)
	assert.Equal(t, defaultMaxResults,
		defaultOptions.maxResults)
	assert.Equal(t, defaultHNSWM,
		defaultOptions.hnswM)
	assert.Equal(t, defaultHNSWEf,
		defaultOptions.hnswEf)
}
