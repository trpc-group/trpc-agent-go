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

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const defaultTableName = "memories"

// memoryRow is the GORM model for the memories table.
// memory_data stores a JSON-encoded memory.Entry (same contract as memory/postgres).
// Column types are chosen for MySQL/PostgreSQL portability (memory_id is SHA-256 hex).
type memoryRow struct {
	MemoryID   string         `gorm:"column:memory_id;primaryKey;type:char(64);size:64"`
	AppName    string         `gorm:"column:app_name;type:varchar(255);not null"`
	UserID     string         `gorm:"column:user_id;type:varchar(255);not null"`
	MemoryData datatypes.JSON `gorm:"column:memory_data;not null"`
	CreatedAt  time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt  time.Time      `gorm:"column:updated_at;not null"`
	DeletedAt  gorm.DeletedAt `gorm:"column:deleted_at"`
}
