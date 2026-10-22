//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
)

func queuedUserMessageFromContentParts(
	messageID string,
	message model.Message,
) (aguitypes.Message, error) {
	return multimodal.UserMessageFromModel(messageID, message)
}
