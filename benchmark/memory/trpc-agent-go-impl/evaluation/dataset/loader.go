//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package dataset provides LoCoMo dataset loading utilities.
package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// LoCoMoSample represents a single sample in the LoCoMo dataset.
type LoCoMoSample struct {
	SampleID     string            `json:"sample_id"`
	Speakers     []string          `json:"speakers"`
	Conversation []Session         `json:"conversation"`
	QA           []QAItem          `json:"qa"`
	EventSummary map[string]string `json:"event_summary,omitempty"`
}

// Session represents a conversation session.
type Session struct {
	SessionID   string `json:"session_id"`
	SessionDate string `json:"session_date,omitempty"`
	Turns       []Turn `json:"turns"`
	Observation string `json:"observation,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// Turn represents a single conversation turn.
type Turn struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// QAItem represents a question-answer pair.
type QAItem struct {
	QuestionID string   `json:"question_id"`
	Question   string   `json:"question"`
	Answer     string   `json:"answer"`
	Category   string   `json:"category"`
	Evidence   []string `json:"evidence,omitempty"`
}

// Loader loads LoCoMo datasets.
type Loader struct {
	baseDir string
}

// NewLoader creates a new dataset loader.
func NewLoader(baseDir string) *Loader {
	return &Loader{baseDir: baseDir}
}

// LoadSamples loads all samples from a JSON file.
func (l *Loader) LoadSamples(filename string) ([]*LoCoMoSample, error) {
	path := filepath.Join(l.baseDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	// Try parsing as array first.
	var samples []*LoCoMoSample
	if err := json.Unmarshal(data, &samples); err == nil {
		return samples, nil
	}
	// Try parsing as LoCoMo-10 raw format.
	if converted, err := parseLoCoMo10Samples(data); err == nil {
		return converted, nil
	}
	// Try parsing as single object.
	var single LoCoMoSample
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	return []*LoCoMoSample{&single}, nil
}

// LoadSample loads a single sample by ID from a JSON file.
func (l *Loader) LoadSample(filename, sampleID string) (*LoCoMoSample, error) {
	samples, err := l.LoadSamples(filename)
	if err != nil {
		return nil, err
	}
	for _, s := range samples {
		if s.SampleID == sampleID {
			return s, nil
		}
	}
	return nil, fmt.Errorf("sample %s not found", sampleID)
}

// BuildFullConversation builds the full conversation text from all sessions.
func (s *LoCoMoSample) BuildFullConversation() string {
	var b strings.Builder
	for _, sess := range s.Conversation {
		if sess.SessionDate != "" {
			fmt.Fprintf(&b, "[Session %s - %s]\n", sess.SessionID, sess.SessionDate)
		} else {
			fmt.Fprintf(&b, "[Session %s]\n", sess.SessionID)
		}
		for _, turn := range sess.Turns {
			fmt.Fprintf(&b, "%s: %s\n", turn.Speaker, turn.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// BuildObservations builds the concatenated observations from all sessions.
func (s *LoCoMoSample) BuildObservations() string {
	var b strings.Builder
	for _, sess := range s.Conversation {
		if sess.Observation != "" {
			fmt.Fprintf(&b, "[Session %s] %s\n", sess.SessionID, sess.Observation)
		}
	}
	return b.String()
}

// BuildSummaries builds the concatenated summaries from all sessions.
func (s *LoCoMoSample) BuildSummaries() string {
	var b strings.Builder
	for _, sess := range s.Conversation {
		if sess.Summary != "" {
			fmt.Fprintf(&b, "[Session %s] %s\n", sess.SessionID, sess.Summary)
		}
	}
	return b.String()
}

// GetQAByCategory filters QA items by category.
func (s *LoCoMoSample) GetQAByCategory(category string) []QAItem {
	var result []QAItem
	for _, qa := range s.QA {
		if qa.Category == category {
			result = append(result, qa)
		}
	}
	return result
}

// GetEvidenceSessions returns the sessions referenced by evidence IDs.
func (s *LoCoMoSample) GetEvidenceSessions(evidence []string) []Session {
	evidenceSet := make(map[string]bool)
	for _, e := range evidence {
		evidenceSet[e] = true
	}
	var result []Session
	for _, sess := range s.Conversation {
		if evidenceSet[sess.SessionID] {
			result = append(result, sess)
		}
	}
	return result
}

type locomo10QA struct {
	Question          string          `json:"question"`
	Answer            json.RawMessage `json:"answer"`
	AdversarialAnswer json.RawMessage `json:"adversarial_answer"`
	Evidence          []string        `json:"evidence"`
	Category          int             `json:"category"`
}

type locomo10Turn struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	DiaID   string `json:"dia_id"`
}

type locomo10Observation map[string]map[string][][]string

const adversarialAnswerFallback = "The information is not available."

func parseLoCoMo10Samples(data []byte) ([]*LoCoMoSample, error) {
	var rawSamples []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSamples); err != nil {
		return nil, err
	}
	results := make([]*LoCoMoSample, 0, len(rawSamples))
	for idx, raw := range rawSamples {
		sampleID := fmt.Sprintf("locomo10_%d", idx+1)

		conversationRaw := extractConversationRaw(raw)
		sessionSummaryRaw := extractSessionSummaryRaw(raw)

		speakers := parseSpeakers(raw)
		if len(speakers) == 0 {
			speakers = inferSpeakersFromConversationRaw(conversationRaw)
		}

		observations := parseObservations(raw)
		sessions, err := parseSessions(
			conversationRaw,
			speakers,
			observations,
			sessionSummaryRaw,
		)
		if err != nil {
			return nil, fmt.Errorf("parse sessions for %s: %w", sampleID, err)
		}
		qaItems, err := parseQAItems(raw, sampleID)
		if err != nil {
			return nil, fmt.Errorf("parse QA for %s: %w", sampleID, err)
		}
		results = append(results, &LoCoMoSample{
			SampleID:     sampleID,
			Speakers:     speakers,
			Conversation: sessions,
			QA:           qaItems,
		})
	}
	return results, nil
}

// extractConversationRaw returns the raw map containing session_* keys.
// Some LoCoMo-10 exports nest them under the "conversation" field.
func extractConversationRaw(raw map[string]json.RawMessage) map[string]json.RawMessage {
	conv, ok := raw["conversation"]
	if !ok {
		return raw
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(conv, &nested); err != nil {
		return raw
	}
	if len(nested) == 0 {
		return raw
	}
	return nested
}

// extractSessionSummaryRaw returns the raw map containing session_*_summary keys.
// Some LoCoMo-10 exports store summaries under the "session_summary" field.
func extractSessionSummaryRaw(raw map[string]json.RawMessage) map[string]json.RawMessage {
	summary, ok := raw["session_summary"]
	if !ok {
		return raw
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(summary, &nested); err != nil {
		return raw
	}
	if len(nested) == 0 {
		return raw
	}
	return nested
}

// inferSpeakersFromConversationRaw infers speakers from parsed session turns.
func inferSpeakersFromConversationRaw(raw map[string]json.RawMessage) []string {
	indexes := extractSessionIndexes(raw)
	if len(indexes) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 2)
	for _, idx := range indexes {
		key := fmt.Sprintf("session_%d", idx)
		v, ok := raw[key]
		if !ok {
			continue
		}
		var turnsRaw []locomo10Turn
		if err := json.Unmarshal(v, &turnsRaw); err != nil {
			continue
		}
		for _, t := range turnsRaw {
			if t.Speaker == "" {
				continue
			}
			if _, ok := seen[t.Speaker]; ok {
				continue
			}
			seen[t.Speaker] = struct{}{}
			out = append(out, t.Speaker)
		}
	}
	return out
}

func parseSpeakers(raw map[string]json.RawMessage) []string {
	var speakers []string
	if a := parseString(raw, "speaker_a"); a != "" {
		speakers = append(speakers, a)
	}
	if b := parseString(raw, "speaker_b"); b != "" {
		speakers = append(speakers, b)
	}
	return speakers
}

func parseObservations(raw map[string]json.RawMessage) locomo10Observation {
	var obs locomo10Observation
	rawObs, ok := raw["observation"]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(rawObs, &obs); err != nil {
		return nil
	}
	return obs
}

func parseSessions(
	raw map[string]json.RawMessage,
	speakers []string,
	observations locomo10Observation,
	sessionSummaryRaw map[string]json.RawMessage,
) ([]Session, error) {
	indexes := extractSessionIndexes(raw)
	sessions := make([]Session, 0, len(indexes))
	for _, idx := range indexes {
		key := fmt.Sprintf("session_%d", idx)
		v, ok := raw[key]
		if !ok {
			continue
		}
		var turnsRaw []locomo10Turn
		if err := json.Unmarshal(v, &turnsRaw); err != nil {
			return nil, fmt.Errorf("unmarshal %s: %w", key, err)
		}
		turns := make([]Turn, 0, len(turnsRaw))
		for _, t := range turnsRaw {
			if t.Text == "" {
				continue
			}
			turns = append(turns, Turn{
				Speaker: t.Speaker,
				Text:    t.Text,
			})
		}
		if len(turns) == 0 {
			continue
		}
		sessionID := inferSessionID(turnsRaw, idx)
		sessionDate := parseString(raw, fmt.Sprintf("session_%d_date_time", idx))
		summary := parseString(sessionSummaryRaw, fmt.Sprintf("session_%d_summary", idx))
		observation := buildObservation(
			observations,
			idx,
			speakers,
		)
		sessions = append(sessions, Session{
			SessionID:   sessionID,
			SessionDate: sessionDate,
			Turns:       turns,
			Observation: observation,
			Summary:     summary,
		})
	}
	return sessions, nil
}

func parseQAItems(
	raw map[string]json.RawMessage,
	sampleID string,
) ([]QAItem, error) {
	var qaRaw []locomo10QA
	if err := json.Unmarshal(raw["qa"], &qaRaw); err != nil {
		return nil, fmt.Errorf("unmarshal qa: %w", err)
	}
	qaItems := make([]QAItem, 0, len(qaRaw))
	for idx, qa := range qaRaw {
		category := mapCategory(qa.Category)
		answer := decodeAnswer(qa.Answer, qa.AdversarialAnswer, category)
		evidence := normalizeEvidence(qa.Evidence)
		qaItems = append(qaItems, QAItem{
			QuestionID: fmt.Sprintf("%s_q_%d", sampleID, idx+1),
			Question:   qa.Question,
			Answer:     answer,
			Category:   category,
			Evidence:   evidence,
		})
	}
	return qaItems, nil
}

func extractSessionIndexes(raw map[string]json.RawMessage) []int {
	indexes := make([]int, 0)
	for key := range raw {
		if idx, ok := parseSessionIndex(key); ok {
			indexes = append(indexes, idx)
		}
	}
	sort.Ints(indexes)
	return indexes
}

func parseSessionIndex(key string) (int, bool) {
	if !strings.HasPrefix(key, "session_") {
		return 0, false
	}
	rest := strings.TrimPrefix(key, "session_")
	if strings.Contains(rest, "_") {
		return 0, false
	}
	idx, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return idx, true
}

func inferSessionID(turns []locomo10Turn, idx int) string {
	for _, t := range turns {
		if t.DiaID == "" {
			continue
		}
		parts := strings.SplitN(t.DiaID, ":", 2)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	return fmt.Sprintf("session_%d", idx)
}

func buildObservation(
	observations locomo10Observation,
	sessionIdx int,
	speakers []string,
) string {
	if observations == nil {
		return ""
	}
	key := fmt.Sprintf("session_%d_observation", sessionIdx)
	sessionObs, ok := observations[key]
	if !ok {
		return ""
	}
	var b strings.Builder
	seen := make(map[string]bool)
	for _, speaker := range speakers {
		appendObservationLines(&b, speaker, sessionObs[speaker])
		seen[speaker] = true
	}
	for speaker, entries := range sessionObs {
		if seen[speaker] {
			continue
		}
		appendObservationLines(&b, speaker, entries)
	}
	return strings.TrimSpace(b.String())
}

func appendObservationLines(
	b *strings.Builder,
	speaker string,
	entries [][]string,
) {
	for _, entry := range entries {
		if len(entry) == 0 || entry[0] == "" {
			continue
		}
		if speaker != "" {
			fmt.Fprintf(b, "%s: %s\n", speaker, entry[0])
		} else {
			fmt.Fprintf(b, "%s\n", entry[0])
		}
	}
}

func normalizeEvidence(evidence []string) []string {
	if len(evidence) == 0 {
		return nil
	}
	result := make([]string, 0, len(evidence))
	for _, item := range evidence {
		parts := strings.SplitN(item, ":", 2)
		if len(parts) > 0 && parts[0] != "" {
			result = append(result, parts[0])
			continue
		}
		result = append(result, item)
	}
	return result
}

func decodeAnswer(
	answer json.RawMessage,
	adversarial json.RawMessage,
	category string,
) string {
	if category == "adversarial" {
		decoded := decodeString(answer)
		if decoded != "" {
			return decoded
		}
		return adversarialAnswerFallback
	}
	if decoded := decodeString(answer); decoded != "" {
		return decoded
	}
	return decodeString(adversarial)
}

func decodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	var i int
	if err := json.Unmarshal(raw, &i); err == nil {
		return strconv.Itoa(i)
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			return "true"
		}
		return "false"
	}
	return ""
}

func mapCategory(category int) string {
	switch category {
	case 1:
		return "single-hop"
	case 2:
		return "multi-hop"
	case 3:
		return "temporal"
	case 4:
		return "open-domain"
	case 5:
		return "adversarial"
	default:
		return fmt.Sprintf("category_%d", category)
	}
}

func parseString(raw map[string]json.RawMessage, key string) string {
	value, ok := raw[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return ""
	}
	return s
}
