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
	"fmt"

	"github.com/go-sql-driver/mysql"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

// BuildClient builds a MySQL client with either DSN or a registered instance name.
func BuildClient(dsn, instanceName string, extraOptions []any) (storage.Client, error) {
	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderDSN(dsn),
		storage.WithExtraOptions(extraOptions...),
	}
	// Priority: dsn > instanceName.
	if dsn == "" && instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetMySQLInstance(instanceName); !ok {
			return nil, fmt.Errorf("mysql instance %s not found", instanceName)
		}
	}
	return storage.GetClientBuilder()(builderOpts...)
}

// IsDuplicateEntry reports whether the error is a MySQL duplicate entry error.
func IsDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == sqldb.MySQLErrDuplicateEntry
}
