//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides the redis instance info management.
package redis

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func init() {
	redisRegistry = make(map[string][]ClientBuilderOpt)
}

var redisRegistry map[string][]ClientBuilderOpt

type clientBuilder func(builderOpts ...ClientBuilderOpt) (redis.UniversalClient, error)

var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the redis client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the redis client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// defaultClientBuilder is the default redis client builder.
func defaultClientBuilder(builderOpts ...ClientBuilderOpt) (redis.UniversalClient, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.URL == "" {
		return nil, errors.New("redis: url is empty")
	}

	opts, err := redis.ParseURL(o.URL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url %s: %w", o.URL, err)
	}
	universalOpts := &redis.UniversalOptions{
		Addrs:                 []string{opts.Addr},
		DB:                    opts.DB,
		Username:              opts.Username,
		Password:              opts.Password,
		Protocol:              opts.Protocol,
		ClientName:            opts.ClientName,
		TLSConfig:             opts.TLSConfig,
		MaxRetries:            opts.MaxRetries,
		MinRetryBackoff:       opts.MinRetryBackoff,
		MaxRetryBackoff:       opts.MaxRetryBackoff,
		DialTimeout:           opts.DialTimeout,
		ReadTimeout:           opts.ReadTimeout,
		WriteTimeout:          opts.WriteTimeout,
		ContextTimeoutEnabled: opts.ContextTimeoutEnabled,
		PoolFIFO:              opts.PoolFIFO,
		PoolSize:              opts.PoolSize,
		PoolTimeout:           opts.PoolTimeout,
		MinIdleConns:          opts.MinIdleConns,
		MaxIdleConns:          opts.MaxIdleConns,
		MaxActiveConns:        opts.MaxActiveConns,
		ConnMaxIdleTime:       opts.ConnMaxIdleTime,
		ConnMaxLifetime:       opts.ConnMaxLifetime,
	}
	return redis.NewUniversalClient(universalOpts), nil
}

// ClientBuilderOpt is the option for the redis client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the redis client.
type ClientBuilderOpts struct {
	// URL is the redis client url for clientBuilder.
	URL string

	// ExtraOptions is the extra options for the redis client.
	ExtraOptions []any
}

// WithClientBuilderURL sets the redis client url for clientBuilder.
func WithClientBuilderURL(url string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.URL = url
	}
}

// WithExtraOptions sets the redis client extra options for clientBuilder.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// RegisterRedisInstance registers a redis instance options.
func RegisterRedisInstance(name string, opts ...ClientBuilderOpt) {
	redisRegistry[name] = append(redisRegistry[name], opts...)
}

// GetRedisInstance gets the redis instance options.
func GetRedisInstance(name string) ([]ClientBuilderOpt, bool) {
	instance, ok := redisRegistry[name]
	return instance, ok
}
