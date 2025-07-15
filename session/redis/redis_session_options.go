package redis

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

var clientBuilder func(builderOpts ...ClientBuilderOpt) (redis.UniversalClient, error) = DefaultClientBuilder

// SetClientBuilder sets the redis client builder.
func SetClientBuilder(builder func(redisOpts ...ClientBuilderOpt) (redis.UniversalClient, error)) {
	clientBuilder = builder
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

// ServiceOpts is the options for the redis session service.
type ServiceOpts struct {
	sessionEventLimit int
	url               string
}

// ServiceOpt is the option for the redis session service.
type ServiceOpt func(*ServiceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithRedisClientURL creates a redis client from URL and sets it to the service.
func WithRedisClientURL(url string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.url = url
	}
}
