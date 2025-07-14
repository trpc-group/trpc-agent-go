package redis

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

var clientBuilder func(*ClientBuilderOpts) (redis.UniversalClient, error) = DefaultClientBuilder

// SetClientBuilder sets the redis client builder.
func SetClientBuilder(builder func(redisOpts *ClientBuilderOpts) (redis.UniversalClient, error)) {
	clientBuilder = builder
}

// DefaultClientBuilder is the default redis client builder.
func DefaultClientBuilder(redisOpts *ClientBuilderOpts) (redis.UniversalClient, error) {
	if redisOpts.url == "" {
		return nil, fmt.Errorf("redis: url is empty")
	}

	opts, err := redis.ParseURL(redisOpts.url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url %s: %w", redisOpts.url, err)
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
	url string
}

// WithURL sets the redis client url for RedisClientOptions.
// scheme: redis://<username>:<password>@<host>:<port>/<db>?<options>
// options: refer goredis.ParseURL
func WithClientBuilderURL(url string) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.url = url
	}
}

// ServiceOpts is the options for the redis session service.
type ServiceOpts struct {
	sessionEventLimit int
	url               string
}

// ServiceOption is the option for the redis session service.
type ServiceOption func(*ServiceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) func(*ServiceOpts) {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithRedisClientURL creates a redis client from URL and sets it to the service.
func WithRedisClientURL(url string) ServiceOption {
	return func(opts *ServiceOpts) {
		opts.url = url
	}
}
