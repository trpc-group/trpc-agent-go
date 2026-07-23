//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestValidateCollectionMetric(t *testing.T) {
	cosine := &vectorIndexConfig{Space: "cosine"}
	l2 := &vectorIndexConfig{Space: "l2"}
	tests := []struct {
		name          string
		configuration collectionConfiguration
		wantError     string
	}{
		{name: "HNSW cosine", configuration: collectionConfiguration{HNSW: cosine}},
		{name: "SPANN cosine", configuration: collectionConfiguration{SPANN: cosine}},
		{
			name:          "multiple indexes",
			configuration: collectionConfiguration{HNSW: cosine, SPANN: cosine},
			wantError:     "multiple active",
		},
		{name: "no index", configuration: collectionConfiguration{}, wantError: "no active"},
		{name: "L2", configuration: collectionConfiguration{HNSW: l2}, wantError: "must be cosine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCollectionMetric(tt.configuration)
			if tt.wantError == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestValidateCollectionID(t *testing.T) {
	assert.NoError(t, validateCollectionID("00000000-0000-0000-0000-000000000001"))
	for _, value := range []string{
		"",
		"collection-id",
		"00000000-0000-0000-0000-00000000000z",
		"000000000000-0000-0000-000000000001",
	} {
		assert.Error(t, validateCollectionID(value), value)
	}
}

func TestNewServiceValidatesCollectionDimensionAndMarker(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeChroma)
		wantError string
	}{
		{
			name: "dimension mismatch",
			configure: func(fake *fakeChroma) {
				dimension := 4
				fake.dimension = &dimension
			},
			wantError: "dimension mismatch",
		},
		{
			name: "invalid collection dimension",
			configure: func(fake *fakeChroma) {
				dimension := -1
				fake.dimension = &dimension
			},
			wantError: "invalid dimension",
		},
		{
			name: "missing dimension field",
			configure: func(fake *fakeChroma) {
				fake.omitDimension = true
			},
			wantError: "missing dimension",
		},
		{
			name: "backend marker conflict",
			configure: func(fake *fakeChroma) {
				fake.metadata["trpc_backend"] = "another-backend"
			},
			wantError: "different backend",
		},
		{
			name: "schema marker conflict",
			configure: func(fake *fakeChroma) {
				fake.metadata[metadataSchemaVersionKey] = 2
			},
			wantError: "unsupported",
		},
		{
			name: "invalid collection ID",
			configure: func(fake *fakeChroma) {
				fake.collectionID = "not-a-uuid"
			},
			wantError: "not a UUID",
		},
		{
			name:      "unmarked collection is allowed",
			configure: func(_ *fakeChroma) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeChroma()
			defer fake.close()
			tt.configure(fake)

			service, err := NewService(
				WithBaseURL(fake.server.URL),
				WithEmbedder(&testEmbedder{dimension: 3}),
			)
			if tt.wantError == "" {
				require.NoError(t, err)
				require.NoError(t, service.Close())
				return
			}
			assert.Nil(t, service)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestNewServiceValidatesCollectionSchema(t *testing.T) {
	disabled := false
	tests := []struct {
		name      string
		schema    *collectionSchema
		wantError string
	}{
		{name: "absent schema is allowed"},
		{name: "default indexes enabled", schema: enabledCollectionSchema()},
		{
			name: "field override enables disabled default",
			schema: func() *collectionSchema {
				schema := enabledCollectionSchema()
				schema.Defaults["string"]["string_inverted_index"] = schemaIndexState{
					Enabled: &disabled,
				}
				enabled := true
				for _, key := range []string{
					metadataAppNameKey,
					metadataUserIDKey,
					metadataKindKey,
					metadataUpdateTokenKey,
				} {
					schema.Keys[key] = map[string]schemaIndexState{
						"string_inverted_index": {Enabled: &enabled},
					}
				}
				return schema
			}(),
		},
		{
			name: "field index disabled",
			schema: func() *collectionSchema {
				schema := enabledCollectionSchema()
				schema.Keys[metadataAppNameKey] = map[string]schemaIndexState{
					"string_inverted_index": {Enabled: &disabled},
				}
				return schema
			}(),
			wantError: metadataAppNameKey,
		},
		{
			name:      "missing defaults",
			schema:    &collectionSchema{Keys: map[string]map[string]schemaIndexState{}},
			wantError: "missing defaults",
		},
		{
			name: "missing type default",
			schema: &collectionSchema{
				Defaults: map[string]map[string]schemaIndexState{},
				Keys:     map[string]map[string]schemaIndexState{},
			},
			wantError: "string defaults",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCollectionSchema(tt.schema)
			if tt.wantError == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestNewServiceAutoCreatesMarkedCosineCollection(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	fake.collectionExists = false

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.True(t, fake.collectionExists)
	assert.Equal(t, "cosine", fake.metric)
	assert.Equal(t, collectionBackend, fake.metadata["trpc_backend"])
	version, err := int64Value(fake.metadata[metadataSchemaVersionKey])
	require.NoError(t, err)
	assert.Equal(t, schemaVersion, version)
}

func TestNewServiceMissingCollectionFailsWhenAutoCreateDisabled(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	fake.collectionExists = false

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
		WithAutoCreateCollection(false),
	)

	assert.Nil(t, service)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 404")
}

func TestNewServiceInfersAPIKeyScopeFromIdentity(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithAPIKey("key"),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	assert.Equal(t, "tenant-a", service.collection.tenant)
	assert.Equal(t, "database-a", service.collection.database)
}

func TestNewServiceCustomAuthUsesExplicitScope(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithHTTPHeaders(map[string]string{"Authorization": "Custom secret"}),
		WithTenant("tenant-custom"),
		WithDatabase("database-custom"),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	assert.Equal(t, "tenant-custom", service.collection.tenant)
	assert.Equal(t, "database-custom", service.collection.database)
}

func TestServiceCloseWaitsForInflightRequest(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.requestHook = func(operation string) {
		if operation == "get" {
			once.Do(func() { close(entered) })
			<-release
		}
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := service.ReadMemories(
			context.Background(),
			memory.UserKey{AppName: "app", UserID: "user"},
			0,
		)
		readDone <- readErr
	}()
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned before the in-flight request completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-readDone)
	require.NoError(t, <-closeDone)

	_, err = service.ReadMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		0,
	)
	assert.ErrorIs(t, err, errServiceClosed)
	assert.NoError(t, service.Close())
}

func TestServiceCloseDrainsWorkerBeforeClosingHTTPClient(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	extractor := &blockingExtractor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
		WithExtractor(extractor),
		WithAsyncMemoryNum(1),
	)
	require.NoError(t, err)
	sess := session.NewSession("app", "user", "session")
	sess.Events = append(sess.Events, event.Event{
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.NewUserMessage("remember this"),
		}}},
	})
	require.NoError(t, service.EnqueueAutoMemoryJob(context.Background(), sess))
	<-extractor.entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned before the worker drained")
	case <-time.After(20 * time.Millisecond):
	}
	close(extractor.release)
	require.NoError(t, <-closeDone)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Len(t, fake.records, 1)
}

func TestServiceToolsReturnsCopy(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	defer service.Close()

	first := service.Tools()
	require.NotEmpty(t, first)
	first[0] = nil
	second := service.Tools()

	assert.NotNil(t, second[0])
}

func TestNewServiceDoesNotMutateInjectedHTTPClient(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	originalRedirect := func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client := fake.server.Client()
	client.CheckRedirect = originalRedirect

	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithHTTPClient(client),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)
	require.NoError(t, service.Close())

	assert.NotSame(t, client, service.client.httpClient)
	assert.ErrorIs(t, client.CheckRedirect(&http.Request{}, nil), http.ErrUseLastResponse)
}

func TestServiceCloseDoesNotCloseInjectedTransport(t *testing.T) {
	fake := newFakeChroma()
	defer fake.close()
	transport := &idleTrackingTransport{base: fake.server.Client().Transport}
	client := &http.Client{Transport: transport}
	service, err := NewService(
		WithBaseURL(fake.server.URL),
		WithHTTPClient(client),
		WithEmbedder(&testEmbedder{dimension: 3}),
	)
	require.NoError(t, err)

	require.NoError(t, service.Close())

	assert.Zero(t, transport.closes())
}

func ExampleNewService() {
	embedder := openaiembedder.New(
		openaiembedder.WithModel("text-embedding-3-small"),
	)
	service, err := NewService(
		WithBaseURL("http://localhost:8000"),
		WithCollectionName("memories"),
		WithEmbedder(embedder),
	)
	if err != nil {
		return
	}
	defer service.Close()
}

func enabledCollectionSchema() *collectionSchema {
	enabled := true
	return &collectionSchema{
		Defaults: map[string]map[string]schemaIndexState{
			"string": {
				"string_inverted_index": {Enabled: &enabled},
			},
			"int": {
				"int_inverted_index": {Enabled: &enabled},
			},
			"bool": {
				"bool_inverted_index": {Enabled: &enabled},
			},
		},
		Keys: map[string]map[string]schemaIndexState{},
	}
}
