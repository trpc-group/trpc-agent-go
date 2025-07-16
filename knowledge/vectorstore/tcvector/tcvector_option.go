package tcvector

import (
	"fmt"
	"net/url"

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
	url, username, password, err := parseVectorDBURL(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse vector db url: %w", err)
	}
	return tcvectordb.NewClient(url, username, password, nil)
}

// parseVectorDBURL parses the vector db url and returns the username, password and key.
// the url format is like: tcvectordb://username:key@host:port
func parseVectorDBURL(urlStr string) (string, string, string, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse URL: %v", err)
	}
	if parsedURL.Scheme != "tcvectordb" {
		return "", "", "", fmt.Errorf("invalid scheme: expected 'tcvectordb', got '%s'", parsedURL.Scheme)
	}
	if parsedURL.User == nil {
		return "", "", "", fmt.Errorf("missing username and key in URL")
	}
	username := parsedURL.User.Username()
	key, _ := parsedURL.User.Password()
	if username == "" {
		return "", "", "", fmt.Errorf("missing username in URL")
	}
	if key == "" {
		return "", "", "", fmt.Errorf("missing key in URL")
	}
	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "80"
	}
	return fmt.Sprintf("http://%s:%s", host, port), username, key, nil
}

// getVectorDBURL gets the vector db url from the url string.
func getVectorDBURL(urlStr string, username string, key string) (string, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %v", err)
	}
	if parsedURL.Scheme != "http" {
		return "", fmt.Errorf("only http is supported")
	}
	return fmt.Sprintf("tcvectordb://%s:%s@%s:%s", username, key, parsedURL.Hostname(), parsedURL.Port()), nil
}

// ClientBuilderOpt is the option for the tcvectordb client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the tcvectordb client.
type ClientBuilderOpts struct {
	URL string
}

// WithClientBuilderURL sets the url for the tcvectordb client.
// the url format is like: tcvectordb://username:key@host:port
func WithClientBuilderURL(url string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.URL = url
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
