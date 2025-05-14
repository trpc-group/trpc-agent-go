package memories

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"encoding/json"
	"io"
	"net/url"
	"strings"
)

const (
	defaultOpenAIModel = "text-embedding-ada-002"
	defaultTimeout     = 30 * time.Second
	defaultDimensions  = 1536  // For text-embedding-ada-002
)

var (
	ErrEmptyAPIKey   = errors.New("OpenAI API key is required")
	ErrOpenAIRequest = errors.New("error making request to OpenAI")
	ErrEmptyText     = errors.New("text to embed cannot be empty")
)

// OpenAIEmbeddingProvider implements EmbeddingProvider using OpenAI.
type OpenAIEmbeddingProvider struct {
	apiKey       string
	model        string
	baseURL      string
	httpClient   *http.Client
	dimensions   int
	requestsLock sync.Mutex
	rateLimiter  *time.Ticker
}

// OpenAIEmbeddingOptions configures the OpenAI embedding provider.
type OpenAIEmbeddingOptions struct {
	Model       string
	BaseURL     string
	Timeout     time.Duration
	Dimensions  int
	RateLimit   time.Duration // Time between requests, used for rate limiting
}

// NewOpenAIEmbeddingProvider creates a new OpenAI embedding provider.
func NewOpenAIEmbeddingProvider(apiKey string, options *OpenAIEmbeddingOptions) (*OpenAIEmbeddingProvider, error) {
	if apiKey == "" {
		return nil, ErrEmptyAPIKey
	}

	if options == nil {
		options = &OpenAIEmbeddingOptions{
			Model:   defaultOpenAIModel,
			BaseURL: "https://api.openai.com",
			Timeout: defaultTimeout,
		}
	}

	if options.Model == "" {
		options.Model = defaultOpenAIModel
	}

	if options.BaseURL == "" {
		options.BaseURL = "https://api.openai.com"
	}

	if options.Timeout <= 0 {
		options.Timeout = defaultTimeout
	}

	dimensions := options.Dimensions
	if dimensions <= 0 {
		dimensions = defaultDimensions
	}

	client := &http.Client{
		Timeout: options.Timeout,
	}

	var rateLimiter *time.Ticker
	if options.RateLimit > 0 {
		rateLimiter = time.NewTicker(options.RateLimit)
	}

	return &OpenAIEmbeddingProvider{
		apiKey:      apiKey,
		model:       options.Model,
		baseURL:     options.BaseURL,
		httpClient:  client,
		dimensions:  dimensions,
		rateLimiter: rateLimiter,
	}, nil
}

// Dimensions returns the dimensionality of the embedding vectors.
func (p *OpenAIEmbeddingProvider) Dimensions() int {
	return p.dimensions
}

// Embed converts text to a vector embedding.
func (p *OpenAIEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, ErrEmptyText
	}

	// Wait for rate limiter if enabled
	if p.rateLimiter != nil {
		p.requestsLock.Lock()
		<-p.rateLimiter.C
		p.requestsLock.Unlock()
	}

	reqURL, err := url.JoinPath(p.baseURL, "/v1/embeddings")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	// Create request body
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": p.model,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Make request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: status code %d, response: %s", 
			ErrOpenAIRequest, resp.StatusCode, string(body))
	}

	// Parse response
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("%w: no embedding returned", ErrOpenAIRequest)
	}

	return result.Data[0].Embedding, nil
}

// BatchEmbed converts multiple texts to vector embeddings.
func (p *OpenAIEmbeddingProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	// Filter out empty texts
	var validTexts []string
	for _, text := range texts {
		if text != "" {
			validTexts = append(validTexts, text)
		}
	}

	if len(validTexts) == 0 {
		return [][]float32{}, nil
	}

	// Wait for rate limiter if enabled
	if p.rateLimiter != nil {
		p.requestsLock.Lock()
		<-p.rateLimiter.C
		p.requestsLock.Unlock()
	}

	reqURL, err := url.JoinPath(p.baseURL, "/v1/embeddings")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	// Create request body
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": p.model,
		"input": validTexts,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Make request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: status code %d, response: %s", 
			ErrOpenAIRequest, resp.StatusCode, string(body))
	}

	// Parse response
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpenAIRequest, err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("%w: no embeddings returned", ErrOpenAIRequest)
	}

	// Extract all embeddings
	embeddings := make([][]float32, len(result.Data))
	for i, data := range result.Data {
		embeddings[i] = data.Embedding
	}

	return embeddings, nil
} 