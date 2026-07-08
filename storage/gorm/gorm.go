//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gorm provides GORM instance registration and client construction.
package gorm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	gormio "gorm.io/gorm"
)

var (
	registryMu   sync.RWMutex
	gormRegistry map[string][]ClientBuilderOpt

	builderMu     sync.RWMutex
	globalBuilder clientBuilder = defaultClientBuilder
)

func init() {
	gormRegistry = make(map[string][]ClientBuilderOpt)
}

// Client exposes a shared *gorm.DB handle and lifecycle management.
type Client interface {
	// DB returns the underlying GORM handle.
	DB() *gormio.DB

	// Close closes the database connection when the client owns it.
	// Injected handles (WithDB) and dialectors opened with WithOwnsConnection(false)
	// are not closed by the client.
	Close() error
}

type gormClient struct {
	db             *gormio.DB
	ownsConnection bool
}

func (c *gormClient) DB() *gormio.DB {
	return c.db
}

func (c *gormClient) Close() error {
	if !c.ownsConnection || c.db == nil {
		return nil
	}
	sqlDB, err := c.db.DB()
	if err != nil {
		return fmt.Errorf("gorm: get sql db: %w", err)
	}
	return sqlDB.Close()
}

type clientBuilder func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error)

// SetClientBuilder sets the GORM client builder.
func SetClientBuilder(builder clientBuilder) {
	builderMu.Lock()
	defer builderMu.Unlock()
	globalBuilder = builder
}

// GetClientBuilder gets the GORM client builder.
func GetClientBuilder() clientBuilder {
	builderMu.RLock()
	defer builderMu.RUnlock()
	return globalBuilder
}

func defaultClientBuilder(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.DB != nil {
		return NewClient(o.DB, false), nil
	}

	if o.Dialector == nil {
		return nil, errors.New("gorm: dialector is required when db is not provided")
	}

	cfg := o.Config
	if cfg == nil {
		cfg = &gormio.Config{}
	}

	db, err := gormio.Open(o.Dialector, cfg)
	if err != nil {
		return nil, fmt.Errorf("gorm: open connection: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("gorm: get sql db: %w", err)
	}

	ownsConnection := o.EffectiveOwnsConnection()

	if err := sqlDB.PingContext(ctx); err != nil {
		if ownsConnection {
			_ = sqlDB.Close()
		}
		return nil, fmt.Errorf("gorm: ping database: %w", err)
	}

	return NewClient(db, ownsConnection), nil
}

// NewClient wraps an existing *gorm.DB with explicit close ownership.
// Custom builders installed via SetClientBuilder can use this helper.
func NewClient(db *gormio.DB, ownsConnection bool) Client {
	return &gormClient{db: db, ownsConnection: ownsConnection}
}

// ApplyClientBuilderOpts folds builder options into a ClientBuilderOpts value.
// Custom builders can use this to observe options such as WithOwnsConnection.
func ApplyClientBuilderOpts(opts ...ClientBuilderOpt) ClientBuilderOpts {
	o := ClientBuilderOpts{}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// ClientBuilderOpt configures GORM client construction.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts collects inputs for the GORM client builder.
type ClientBuilderOpts struct {
	DB           *gormio.DB
	Dialector    gormio.Dialector
	Config       *gormio.Config
	InstanceName string
	ExtraOptions []any

	// OwnsConnection controls whether Client.Close closes dialector-opened pools.
	// Only meaningful when OwnsConnectionSet is true.
	OwnsConnection bool

	// OwnsConnectionSet reports whether OwnsConnection was configured explicitly.
	OwnsConnectionSet bool
}

// EffectiveOwnsConnection reports whether the client should close dialector-opened pools.
// Defaults to true when OwnsConnectionSet is false.
func (o ClientBuilderOpts) EffectiveOwnsConnection() bool {
	if o.OwnsConnectionSet {
		return o.OwnsConnection
	}
	return true
}

// WithDB injects an existing *gorm.DB. The caller owns the DB lifecycle.
func WithDB(db *gormio.DB) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.DB = db
	}
}

// WithDialector sets the GORM dialector used to open a connection.
// By default Client.Close closes the opened pool (ownership transfer).
// When the dialector wraps a caller-owned ConnPool, also pass WithOwnsConnection(false).
func WithDialector(d gormio.Dialector) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.Dialector = d
	}
}

// WithOwnsConnection controls whether Client.Close closes the underlying pool
// for dialector-opened connections. Ignored when WithDB is used.
func WithOwnsConnection(owns bool) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.OwnsConnection = owns
		opts.OwnsConnectionSet = true
	}
}

// WithConfig sets the GORM config used when opening a new connection.
func WithConfig(c *gormio.Config) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.Config = c
	}
}

// WithInstanceName records the named instance for custom builders.
func WithInstanceName(name string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.InstanceName = name
	}
}

// WithExtraOptions passes opaque options to custom client builders.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// RegisterGormInstance registers a named GORM instance.
func RegisterGormInstance(name string, opts ...ClientBuilderOpt) {
	registryMu.Lock()
	defer registryMu.Unlock()
	gormRegistry[name] = append(gormRegistry[name], opts...)
}

// GetGormInstance returns builder options for a registered instance.
func GetGormInstance(name string) ([]ClientBuilderOpt, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	opts, ok := gormRegistry[name]
	if !ok {
		return nil, false
	}
	copyOpts := make([]ClientBuilderOpt, len(opts))
	copy(copyOpts, opts)
	return copyOpts, true
}
