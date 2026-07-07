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
