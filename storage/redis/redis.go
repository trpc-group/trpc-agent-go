//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package redis provides the redis instance info management.
package redis

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

func init() {
	redisRegistry = make(map[string][]ClientBuilderOpt)
}

var redisRegistry = map[string][]ClientBuilderOpt{}

type clientBuilder func(builderOpts ...ClientBuilderOpt) (redis.UniversalClient, error)

var globalBuilder clientBuilder = DefaultClientBuilder

// SetClientBuilder sets the redis client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the redis client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// DefaultClientBuilder is the default redis client builder.
func DefaultClientBuilder(builderOpts ...ClientBuilderOpt) (redis.UniversalClient, error) {
	o := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(o)
	}

	if o.URL == "" {
		return nil, fmt.Errorf("redis: url is empty")
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
	URL string
}

// WithClientBuilderURL sets the redis client url for clientBuilder.
// scheme: redis://<username>:<password>@<host>:<port>/<db>?<options>
// options: refer goredis.ParseURL
func WithClientBuilderURL(url string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.URL = url
	}
}

// RegisterRedisInstance registers a redis instance options.
func RegisterRedisInstance(name string, opts ...ClientBuilderOpt) {
	redisRegistry[name] = append(redisRegistry[name], opts...)
}

// GetRedisInstance gets the redis instance options.
func GetRedisInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := redisRegistry[name]; !ok {
		return nil, false
	}
	return redisRegistry[name], true
}
