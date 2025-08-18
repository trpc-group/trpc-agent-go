package openai

import (
	"context"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/pagination"
	"github.com/openai/openai-go/packages/param"
)

// CreateBatch creates a new batch job for processing multiple requests.
func (m *Model) CreateBatch(
	ctx context.Context,
	request openai.BatchNewParams,
) (*openai.Batch, error) {
	return m.client.Batches.New(ctx, request)
}

// RetrieveBatch retrieves a batch job by ID.
func (m *Model) RetrieveBatch(ctx context.Context, batchID string) (*openai.Batch, error) {
	return m.client.Batches.Get(ctx, batchID)
}

// CancelBatch cancels an in-progress batch job.
func (m *Model) CancelBatch(ctx context.Context, batchID string) (*openai.Batch, error) {
	return m.client.Batches.Cancel(ctx, batchID)
}

// ListBatches lists batch jobs with pagination.
func (m *Model) ListBatches(
	ctx context.Context,
	after string,
	limit int64,
) (*pagination.CursorPage[openai.Batch], error) {
	params := openai.BatchListParams{}

	if after != "" {
		params.After = param.NewOpt(after)
	}
	if limit > 0 {
		params.Limit = param.NewOpt(limit)
	}

	return m.client.Batches.List(ctx, params)
}
