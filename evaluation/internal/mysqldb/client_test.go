//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqldb

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

func TestBuildClient_UsesDSNWhenProvided(t *testing.T) {
	db, _, err := sqlmock.New()
	assert.NoError(t, err)

	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		o := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(o)
		}
		assert.Equal(t, "dsn", o.DSN)
		return storage.WrapSQLDB(db), nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(oldBuilder) })

	c, err := BuildClient("dsn", "instance", []any{"x"})
	assert.NoError(t, err)
	assert.NotNil(t, c)
}

func TestBuildClient_UsesInstanceWhenDSNEmpty(t *testing.T) {
	db, _, err := sqlmock.New()
	assert.NoError(t, err)

	storage.RegisterMySQLInstance("inst", storage.WithClientBuilderDSN("dsn-from-registry"))

	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		o := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(o)
		}
		assert.Equal(t, "dsn-from-registry", o.DSN)
		return storage.WrapSQLDB(db), nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(oldBuilder) })

	c, err := BuildClient("", "inst", nil)
	assert.NoError(t, err)
	assert.NotNil(t, c)
}

func TestBuildClient_InstanceNotFound(t *testing.T) {
	_, err := BuildClient("", "missing", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestIsDuplicateEntry(t *testing.T) {
	assert.False(t, IsDuplicateEntry(errors.New("boom")))
	assert.False(t, IsDuplicateEntry(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName}))
	assert.True(t, IsDuplicateEntry(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry}))
}
