//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package age

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

const (
	defaultGraphName = "knowledge_graph"
)

type options struct {
	dsn          string
	instanceName string

	graphName string
}

var defaultOptions = options{
	graphName: defaultGraphName,
}

// Option configures an AGE graph store.
type Option func(*options)

// WithGraphName sets the Apache AGE graph name.
func WithGraphName(name string) Option {
	return func(o *options) {
		o.graphName = name
	}
}

// WithClientDSN sets the PostgreSQL DSN used for the AGE backend.
func WithClientDSN(dsn string) Option {
	return func(o *options) {
		o.dsn = dsn
	}
}

// WithPostgresInstance uses a registered postgres instance from storage/postgres.
func WithPostgresInstance(instanceName string) Option {
	return func(o *options) {
		o.instanceName = instanceName
	}
}

func (o options) builderOptions() ([]postgres.ClientBuilderOpt, error) {
	if o.instanceName != "" {
		builderOpts, ok := postgres.GetPostgresInstance(o.instanceName)
		if !ok {
			return nil, fmt.Errorf("postgres instance %s not found", o.instanceName)
		}
		return builderOpts, nil
	}
	if o.dsn != "" {
		return []postgres.ClientBuilderOpt{postgres.WithClientConnString(o.dsn)}, nil
	}
	return nil, fmt.Errorf("age: graph store requires WithClientDSN or WithPostgresInstance")
}
