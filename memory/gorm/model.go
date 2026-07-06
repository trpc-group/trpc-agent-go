//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gormmemory

import (
	"time"

	"gorm.io/gorm"
)

const defaultTableName = "memories"

// memoryRow is the GORM model for the memories table.
// memory_data stores a JSON-encoded memory.Entry (same contract as memory/postgres).
type memoryRow struct {
	MemoryID   string         `gorm:"column:memory_id;primaryKey;type:text"`
	AppName    string         `gorm:"column:app_name;type:text;not null;index:idx_memories_app_user"`
	UserID     string         `gorm:"column:user_id;type:text;not null;index:idx_memories_app_user"`
	MemoryData []byte         `gorm:"column:memory_data;type:jsonb;not null"`
	CreatedAt  time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt  time.Time      `gorm:"column:updated_at;not null;index:idx_memories_updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"column:deleted_at;index:idx_memories_deleted_at"`
}
