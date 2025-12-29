//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"time"
)

const (
	// RuntimeStateKeyTransferController is the RunOptions.RuntimeState key used
	// to install a transfer controller for a single run.
	RuntimeStateKeyTransferController = "transfer_controller"
)

// TransferController can enforce limits and policies for transfer_to_agent.
//
// If a TransferController is present in RunOptions.RuntimeState, the
// framework calls OnTransfer before running the target agent.
type TransferController interface {
	// OnTransfer is called right before the framework runs the target agent.
	//
	// If err is non-nil, the transfer is rejected.
	// If targetTimeout is > 0, the target agent run is bounded by that timeout.
	OnTransfer(
		ctx context.Context,
		fromAgent string,
		toAgent string,
	) (targetTimeout time.Duration, err error)
}
