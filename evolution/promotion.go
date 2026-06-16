//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

func archiveCurrentActiveRevision(
	ctx context.Context,
	store CandidateStore,
	pointer ActivePointer,
	skillID string,
	replacingRevisionID string,
) error {
	if store == nil || pointer == nil || strings.TrimSpace(skillID) == "" {
		return nil
	}
	currentRevisionID, err := pointer.Get(ctx, skillID)
	if err != nil {
		return fmt.Errorf("get active pointer: %w", err)
	}
	if currentRevisionID == "" || currentRevisionID == replacingRevisionID {
		return nil
	}
	current, err := store.ReadRevision(ctx, skillID, currentRevisionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read active revision: %w", err)
	}
	if current.Status == RevisionArchived {
		return nil
	}
	current.Status = RevisionArchived
	if err := store.WriteRevision(ctx, current); err != nil {
		return fmt.Errorf("archive active revision: %w", err)
	}
	_ = store.AppendAudit(ctx, AuditEvent{
		Action:     AuditActionArchive,
		SkillID:    skillID,
		RevisionID: currentRevisionID,
		Status:     string(RevisionArchived),
		Reason:     "superseded by " + replacingRevisionID,
	})
	return nil
}
