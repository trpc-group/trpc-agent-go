package tcvector

import (
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

// ClientInterface is the interface for the tcvectordb client.
type ClientInterface interface {
	tcvectordb.DatabaseInterface
	tcvectordb.FlatInterface
}

// clientBuilder is the function to build the global tcvectordb client.
var clientBuilder func(builderOpts ...ClientBuilderOpt) (ClientInterface, error) = DefaultClientBuilder

// SetClientBuilder sets the client builder for tcvectordb.
func SetClientBuilder(builder func(builderOpts ...ClientBuilderOpt) (ClientInterface, error)) {
	clientBuilder = builder
}

// DefaultClientBuilder is the default client builder for tcvectordb.
func DefaultClientBuilder(builderOpts ...ClientBuilderOpt) (ClientInterface, error) {
	opts := &ClientBuilderOpts{}
	for _, opt := range builderOpts {
		opt(opts)
	}
	return tcvectordb.NewClient(opts.HTTPURL, opts.UserName, opts.Key, nil)
}

// ClientBuilderOpt is the option for the tcvectordb client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the tcvectordb client.
type ClientBuilderOpts struct {
	HTTPURL  string
	UserName string
	Key      string
}

// WithClientBuilderHTTPURL sets the http url for the tcvectordb client.
func WithClientBuilderHTTPURL(httpURL string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.HTTPURL = httpURL
	}
}

// WithClientBuilderUserName sets the username for the tcvectordb client.
func WithClientBuilderUserName(userName string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.UserName = userName
	}
}

// WithClientBuilderKey sets the key for the tcvectordb client.
func WithClientBuilderKey(key string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.Key = key
	}
}

// options contains the options for tcvectordb.
type options struct {
	username       string
	password       string
	url            string
	database       string
	collection     string
	indexDimension uint32
	replicas       uint32
	sharding       uint32
	enableTSVector bool

	// Hybrid search scoring weights
	vectorWeight float64 // Default: Vector similarity weight 70%
	textWeight   float64 // Default: Text relevance weight 30%
	language     string  // Default: zh, options: zh, en

}

var defaultOptions = options{
	indexDimension: 1536,
	database:       "trpc-agent-go",
	collection:     "documents",
	replicas:       0,
	sharding:       1,
	enableTSVector: true,
	vectorWeight:   0.7,
	textWeight:     0.3,
	language:       "en",
}

// Option is the option for tcvectordb.
type Option func(*options)

// WithURL sets the vector database URL
func WithURL(url string) Option {
	return func(o *options) {
		o.url = url
	}
}

// WithUsername sets the username for authentication
func WithUsername(username string) Option {
	return func(o *options) {
		o.username = username
	}
}

// WithPassword sets the password for authentication
func WithPassword(password string) Option {
	return func(o *options) {
		o.password = password
	}
}

// WithDatabase sets the database name
func WithDatabase(database string) Option {
	return func(o *options) {
		o.database = database
	}
}

// WithCollection sets the collection name
func WithCollection(collection string) Option {
	return func(o *options) {
		o.collection = collection
	}
}

// WithIndexDimension sets the vector dimension for the index
func WithIndexDimension(dimension uint32) Option {
	return func(o *options) {
		o.indexDimension = dimension
	}
}

// WithReplicas sets the number of replicas
func WithReplicas(replicas uint32) Option {
	return func(o *options) {
		o.replicas = replicas
	}
}

// WithSharding sets the number of shards
func WithSharding(sharding uint32) Option {
	return func(o *options) {
		o.sharding = sharding
	}
}

// WithEnableTSVector sets the enableTSVector for the vector database
func WithEnableTSVector(enableTSVector bool) Option {
	return func(o *options) {
		o.enableTSVector = enableTSVector
	}
}

// WithHybridSearchWeights sets the weights for hybrid search scoring
// vectorWeight: Weight for vector similarity (0.0-1.0)
// textWeight: Weight for text relevance (0.0-1.0)
// Note: weights will be normalized to sum to 1.0
func WithHybridSearchWeights(vectorWeight, textWeight float64) Option {
	return func(o *options) {
		// Normalize weights to sum to 1.0
		total := vectorWeight + textWeight
		if total > 0 {
			o.vectorWeight = vectorWeight / total
			o.textWeight = textWeight / total
		} else {
			// Fallback to defaults if invalid weights
			o.vectorWeight = 0.7
			o.textWeight = 0.3
		}
	}
}

// WithLanguage sets the language for the vector database
func WithLanguage(language string) Option {
	return func(o *options) {
		o.language = language
	}
}
