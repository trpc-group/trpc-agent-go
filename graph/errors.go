package graph

import "errors"

var (
	ErrThreadIDRequired                = errors.New("thread_id is required")
	ErrThreadIDEmpty                   = errors.New("thread_id cannot be empty")
	ErrThreadIDAndCheckpointIDRequired = errors.New("thread_id and checkpoint_id are required")
	ErrCheckpointNotFound              = errors.New("checkpoint not found")
)
