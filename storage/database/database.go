//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package database provides the Database instance info management.
package database

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	databaseRegistry = make(map[string][]ClientBuilderOpt)
}

var databaseRegistry map[string][]ClientBuilderOpt

type clientBuilder func(builderOpts ...ClientBuilderOpt) (*gorm.DB, error)

var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the Database client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the Database client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// defaultClientBuilder is the default Database client builder.
func defaultClientBuilder(builderOpts ...ClientBuilderOpt) (*gorm.DB, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.DSN == "" {
		return nil, errors.New("database: DSN is empty")
	}

	// Default to MySQL if driver type not specified
	if o.DriverType == "" {
		o.DriverType = DriverMySQL
	}

	// Set default GORM config if not provided
	if o.Config == nil {
		o.Config = &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		}
	}

	// Select appropriate driver based on driver type
	var dialector gorm.Dialector
	switch o.DriverType {
	case DriverMySQL:
		dialector = mysql.Open(o.DSN)
	case DriverPostgreSQL:
		// Note: requires "gorm.io/driver/postgres" to be imported
		return nil, fmt.Errorf("database: PostgreSQL driver not imported, please use custom builder with postgres.Open()")
	case DriverSQLite:
		// Note: requires "gorm.io/driver/sqlite" to be imported
		return nil, fmt.Errorf("database: SQLite driver not imported, please use custom builder with sqlite.Open()")
	default:
		return nil, fmt.Errorf("database: unsupported driver type: %s", o.DriverType)
	}

	db, err := gorm.Open(dialector, o.Config)
	if err != nil {
		return nil, fmt.Errorf("database: open connection: %w", err)
	}

	// Get underlying sql.DB to configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database: get underlying sql.DB: %w", err)
	}

	// Set connection pool parameters
	if o.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(o.MaxIdleConns)
	}
	if o.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(o.MaxOpenConns)
	}
	if o.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(o.ConnMaxLifetime)
	}
	if o.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(o.ConnMaxIdleTime)
	}

	return db, nil
}

// ClientBuilderOpt is the option for the Database client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// DriverType represents the database driver type
type DriverType string

const (
	// DriverMySQL represents MySQL database
	DriverMySQL DriverType = "mysql"
	// DriverPostgreSQL represents PostgreSQL database
	DriverPostgreSQL DriverType = "postgres"
	// DriverSQLite represents SQLite database (for testing)
	DriverSQLite DriverType = "sqlite"
)

// ClientBuilderOpts is the options for the Database client.
type ClientBuilderOpts struct {
	// DSN is the Database data source name.
	// MySQL format: username:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	// PostgreSQL format: host=localhost user=postgres password=pass dbname=db port=5432 sslmode=disable
	// SQLite format: file:test.db?cache=shared or :memory:
	DSN string

	// DriverType specifies which database driver to use (mysql, postgres, sqlite)
	// If not specified, defaults to mysql for backward compatibility
	DriverType DriverType

	// Config is the GORM configuration.
	Config *gorm.Config

	// Connection pool settings
	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	// ExtraOptions is the extra options for the Database client.
	ExtraOptions []any
}

// WithClientBuilderDSN sets the Database DSN for clientBuilder.
func WithClientBuilderDSN(dsn string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.DSN = dsn
	}
}

// WithDriverType sets the database driver type (mysql, postgres, sqlite).
// If not set, defaults to mysql for backward compatibility.
func WithDriverType(driverType DriverType) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.DriverType = driverType
	}
}

// WithGormConfig sets the GORM configuration.
func WithGormConfig(config *gorm.Config) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.Config = config
	}
}

// WithMaxIdleConns sets the maximum number of idle connections in the pool.
func WithMaxIdleConns(n int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.MaxIdleConns = n
	}
}

// WithMaxOpenConns sets the maximum number of open connections to the database.
func WithMaxOpenConns(n int) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.MaxOpenConns = n
	}
}

// WithExtraOptions sets the Database client extra options for clientBuilder.
// this option mainly used for the customized Database client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// RegisterDatabaseInstance registers a database instance options.
func RegisterDatabaseInstance(name string, opts ...ClientBuilderOpt) {
	databaseRegistry[name] = append(databaseRegistry[name], opts...)
}

// GetDatabaseInstance gets the database instance options.
func GetDatabaseInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := databaseRegistry[name]; !ok {
		return nil, false
	}
	return databaseRegistry[name], true
}
