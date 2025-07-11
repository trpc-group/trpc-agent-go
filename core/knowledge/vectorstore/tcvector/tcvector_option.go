package tcvector

type Options struct {
	username       string
	password       string
	url            string
	database       string
	collection     string
	indexDimension int
	replicas       uint32
	sharding       uint32
	// Hybrid search scoring weights
	vectorWeight float64 // Default: Vector similarity weight 70%
	textWeight   float64 // Default: Text relevance weight 30%
}

var defaultOptions = Options{
	indexDimension: 1536,
	database:       "trpc-agent-go",
	collection:     "documents",
	replicas:       2,
	sharding:       3,
	vectorWeight:   0.7,
	textWeight:     0.3,
}

type Option func(*Options)

// WithURL sets the vector database URL
func WithURL(url string) Option {
	return func(o *Options) {
		o.url = url
	}
}

// WithUsername sets the username for authentication
func WithUsername(username string) Option {
	return func(o *Options) {
		o.username = username
	}
}

// WithPassword sets the password for authentication
func WithPassword(password string) Option {
	return func(o *Options) {
		o.password = password
	}
}

// WithDatabase sets the database name
func WithDatabase(database string) Option {
	return func(o *Options) {
		o.database = database
	}
}

// WithCollection sets the collection name
func WithCollection(collection string) Option {
	return func(o *Options) {
		o.collection = collection
	}
}

// WithIndexDimension sets the vector dimension for the index
func WithIndexDimension(dimension int) Option {
	return func(o *Options) {
		o.indexDimension = dimension
	}
}

// WithReplicas sets the number of replicas
func WithReplicas(replicas uint32) Option {
	return func(o *Options) {
		o.replicas = replicas
	}
}

// WithSharding sets the number of shards
func WithSharding(sharding uint32) Option {
	return func(o *Options) {
		o.sharding = sharding
	}
}

// WithHybridSearchWeights sets the weights for hybrid search scoring
// vectorWeight: Weight for vector similarity (0.0-1.0)
// textWeight: Weight for text relevance (0.0-1.0)
// Note: weights will be normalized to sum to 1.0
func WithHybridSearchWeights(vectorWeight, textWeight float64) Option {
	return func(o *Options) {
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
