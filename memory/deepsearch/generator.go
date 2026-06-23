//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deepsearch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultMaxCues   = 12
	defaultMaxTags   = 8
	defaultBatchSize = 80
)

var errOutputValidation = errors.New("deepsearch output validation failed")

// Option 配置 DeepSearch 索引生成。
type Option func(*options)

type options struct {
	maxCues   int
	maxTags   int
	batchSize int
}

// WithLimits 限制每条记忆生成的 cue 和 tag 数量。
func WithLimits(maxCues, maxTags int) Option {
	return func(opts *options) {
		if maxCues > 0 {
			opts.maxCues = maxCues
		}
		if maxTags > 0 {
			opts.maxTags = maxTags
		}
	}
}

// WithBatchSize 设置单次 LLM 索引请求包含的记忆数量。
func WithBatchSize(size int) Option {
	return func(opts *options) {
		if size > 0 {
			opts.batchSize = size
		}
	}
}

// BuildDocuments 使用 LLM 为记忆条目生成完整的 DeepSearch 文档。
func BuildDocuments(
	ctx context.Context,
	indexModel model.Model,
	entries []*memory.Entry,
	opts ...Option,
) ([]Document, error) {
	if indexModel == nil {
		return nil, errors.New("deepsearch model is required")
	}
	baseDocuments, err := documentsFromEntries(entries)
	if err != nil {
		return nil, err
	}
	if len(baseDocuments) == 0 {
		return nil, nil
	}

	cfg := resolveOptions(opts)
	documents := make([]Document, 0, len(baseDocuments))
	for start := 0; start < len(baseDocuments); start += cfg.batchSize {
		end := min(start+cfg.batchSize, len(baseDocuments))
		batch, err := generateBatchWithSplit(ctx, indexModel, baseDocuments[start:end], cfg)
		if err != nil {
			return nil, err
		}
		documents = append(documents, batch...)
	}
	return documents, nil
}

func generateBatchWithSplit(
	ctx context.Context,
	indexModel model.Model,
	documents []Document,
	cfg options,
) ([]Document, error) {
	result, err := generateBatch(ctx, indexModel, documents, cfg)
	if err == nil {
		return result, nil
	}
	if len(documents) <= 1 || !errors.Is(err, errOutputValidation) {
		return nil, err
	}
	mid := len(documents) / 2
	left, leftErr := generateBatchWithSplit(ctx, indexModel, documents[:mid], cfg)
	if leftErr != nil {
		return nil, leftErr
	}
	right, rightErr := generateBatchWithSplit(ctx, indexModel, documents[mid:], cfg)
	if rightErr != nil {
		return nil, rightErr
	}
	return append(left, right...), nil
}

func resolveOptions(opts []Option) options {
	cfg := options{
		maxCues:   defaultMaxCues,
		maxTags:   defaultMaxTags,
		batchSize: defaultBatchSize,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func documentsFromEntries(entries []*memory.Entry) ([]Document, error) {
	documents := make([]Document, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			return nil, errors.New("deepsearch memory entry is nil")
		}
		text := strings.TrimSpace(entry.Memory.Memory)
		if entry.ID == "" || text == "" {
			return nil, errors.New("deepsearch memory id and text are required")
		}
		documents = append(documents, Document{
			ID:   entry.ID,
			Text: text,
			Ref: ContentRef{
				Kind:     RefKindMemoryEntry,
				AppName:  entry.AppName,
				UserID:   entry.UserID,
				SourceID: entry.ID,
			},
			Metadata: Metadata{
				SourceFingerprint: SourceFingerprint(entry),
				Topics:            entry.Memory.Topics,
				EventTime:         timeValue(entry.Memory.EventTime),
				Participants:      entry.Memory.Participants,
				Location:          entry.Memory.Location,
				Kind:              entry.Memory.Kind,
			},
			Created: entry.CreatedAt,
		})
	}
	return documents, nil
}

// SourceFingerprint 返回用于判断 DeepSearch 索引是否过期的摘要。
func SourceFingerprint(entry *memory.Entry) string {
	if entry == nil || entry.Memory == nil {
		return ""
	}
	payload, _ := json.Marshal(struct {
		ID        string    `json:"id"`
		UpdatedAt time.Time `json:"updated_at"`
		Topics    []string  `json:"topics"`
	}{
		ID:        entry.ID,
		UpdatedAt: entry.UpdatedAt.UTC(),
		Topics:    entry.Memory.Topics,
	})
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

type llmInputEntry struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Topics       []string `json:"topics,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	EventTime    string   `json:"event_time,omitempty"`
	Participants []string `json:"participants,omitempty"`
	Location     string   `json:"location,omitempty"`
}

type llmOutput struct {
	Memories []llmOutputEntry `json:"memories"`
}

type llmOutputEntry struct {
	ID   string   `json:"id"`
	Cues []string `json:"cues"`
	Tags []string `json:"tags"`
}

func generateBatch(
	ctx context.Context,
	indexModel model.Model,
	documents []Document,
	cfg options,
) ([]Document, error) {
	input := make([]llmInputEntry, 0, len(documents))
	outputIDs := make([]string, 0, len(documents))
	for i, document := range documents {
		outputID := "m" + strconv.Itoa(i+1)
		outputIDs = append(outputIDs, outputID)
		input = append(input, llmInputEntry{
			ID:           outputID,
			Text:         document.Text,
			Topics:       document.Metadata.Topics,
			Kind:         string(document.Metadata.Kind),
			EventTime:    timeString(document.Metadata.EventTime),
			Participants: document.Metadata.Participants,
			Location:     document.Metadata.Location,
		})
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal deepsearch batch: %w", err)
	}
	request := model.NewRequest(
		[]model.Message{
			model.NewSystemMessage(systemPrompt(cfg)),
			model.NewUserMessage(string(payload)),
		},
		model.WithStructuredOutputJSON(
			new(llmOutput),
			true,
			"Return cue and tag indexes for every supplied memory entry.",
		),
	)
	responses, err := indexModel.GenerateContent(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("generate deepsearch batch: %w", err)
	}
	if responses == nil {
		return nil, errors.New("generate deepsearch batch returned nil channel")
	}
	text, err := collectResponse(ctx, responses)
	if err != nil {
		return nil, err
	}
	output, err := parseOutput(text)
	if err != nil {
		return nil, err
	}
	return mergeOutput(documents, outputIDs, output, cfg)
}

func systemPrompt(cfg options) string {
	return fmt.Sprintf(
		"Generate compact cue/tag indexes for durable memory entries. "+
			"Return JSON only with shape {\"memories\":[{\"id\":\"...\",\"cues\":[...],\"tags\":[...]}]}. "+
			"Preserve every input id. Cues are short retrieval phrases a future user question may contain. "+
			"Tags are stable entities, topics, relations, dates, people, places, or aspects. "+
			"Do not invent facts. Return at least one cue and one tag, with at most %d cues and %d tags per memory.",
		cfg.maxCues,
		cfg.maxTags,
	)
}

func collectResponse(ctx context.Context, responses <-chan *model.Response) (string, error) {
	var builder strings.Builder
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("deepsearch generation canceled: %w", ctx.Err())
		case response, ok := <-responses:
			if !ok {
				return builder.String(), nil
			}
			if response == nil {
				continue
			}
			if response.Error != nil {
				return "", fmt.Errorf("deepsearch model error: %s", response.Error.Message)
			}
			if len(response.Choices) == 0 {
				continue
			}
			builder.WriteString(response.Choices[0].Message.Content)
			builder.WriteString(response.Choices[0].Delta.Content)
		}
	}
}

func parseOutput(text string) (*llmOutput, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("deepsearch model returned empty output")
	}
	var output llmOutput
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		return nil, fmt.Errorf("parse deepsearch output: %w", err)
	}
	return &output, nil
}

func mergeOutput(
	documents []Document,
	outputIDs []string,
	output *llmOutput,
	cfg options,
) ([]Document, error) {
	if output == nil {
		return nil, errors.New("deepsearch model returned nil output")
	}
	if len(documents) != len(outputIDs) {
		return nil, errors.New("deepsearch internal output id mismatch")
	}
	byID := make(map[string]llmOutputEntry, len(output.Memories))
	for _, item := range output.Memories {
		if item.ID == "" {
			return nil, fmt.Errorf("%w: memory id is required", errOutputValidation)
		}
		if _, ok := byID[item.ID]; ok {
			return nil, fmt.Errorf("%w: duplicate id %q", errOutputValidation, item.ID)
		}
		byID[item.ID] = item
	}

	result := append([]Document(nil), documents...)
	for i := range result {
		outputID := outputIDs[i]
		item, ok := byID[outputID]
		if !ok {
			return nil, fmt.Errorf("%w: missing id %q", errOutputValidation, outputID)
		}
		cues := uniqueStrings(item.Cues)
		tags := uniqueStrings(item.Tags)
		if len(cues) == 0 || len(tags) == 0 {
			return nil, fmt.Errorf("%w: id %q requires cues and tags", errOutputValidation, outputID)
		}
		result[i].Cues = limitStrings(cues, cfg.maxCues)
		result[i].Tags = limitStrings(tags, cfg.maxTags)
		delete(byID, outputID)
	}
	if len(byID) != 0 {
		return nil, fmt.Errorf("%w: contains unknown memory ids", errOutputValidation)
	}
	return result, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func limitStrings(values []string, limit int) []string {
	if limit > 0 && len(values) > limit {
		return values[:limit]
	}
	return values
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
