//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

type memRecord struct {
	id        string
	document  string
	embedding []float32
	metadata  map[string]any
}

type fakeClient struct {
	mu sync.Mutex

	bound      bool
	missing    bool
	records    map[string]*memRecord
	collection string
	info       storage.CollectionInfo

	lastAdd    storage.RecordBatch
	lastUpdate storage.RecordBatch
	lastUpsert storage.RecordBatch
	lastGet    storage.GetParams
	lastQuery  storage.QueryParams
	lastSearch storage.SearchParams
	lastDelete storage.DeleteParams

	getOrCreateCalls                                                                                           int
	getCollectionCalls                                                                                         int
	addCalls, getCalls, updateCalls, upsertCalls, deleteCalls, queryCalls, searchCalls, countCalls, closeCalls int

	closeErr         error
	getOrCreateErr   error
	getCollectionErr error
	upsertErr        error
	getErr           error
	updateErr        error
	queryErr         error
	searchErr        error
	deleteErr        error
	countErr         error

	getNil       bool
	fullPageGets int
	repeatPage   bool
	deleteNoop   bool
	batchSize    int
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		records: map[string]*memRecord{},
		info:    storage.CollectionInfo{Metric: "cosine"},
	}
}

var _ storage.ClientInterface = (*fakeClient)(nil)

func (f *fakeClient) Heartbeat(context.Context) error { return nil }

func (f *fakeClient) CollectionInfo() storage.CollectionInfo {
	return f.info
}

func (f *fakeClient) MaxBatchSize(context.Context) (int, error) {
	if f.batchSize > 0 {
		return f.batchSize, nil
	}
	return 1000, nil
}

func (f *fakeClient) GetOrCreateCollection(_ context.Context, name string, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getOrCreateCalls++
	if f.getOrCreateErr != nil {
		return f.getOrCreateErr
	}
	f.collection = name
	f.bound = true
	f.missing = false
	return nil
}

func (f *fakeClient) GetCollection(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCollectionCalls++
	if f.getCollectionErr != nil {
		return f.getCollectionErr
	}
	if f.missing {
		return storage.ErrCollectionNotFound
	}
	f.collection = name
	f.bound = true
	return nil
}

func (f *fakeClient) DeleteCollection(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.collection == name {
		f.bound = false
		f.collection = ""
		f.records = map[string]*memRecord{}
	}
	return nil
}

func (f *fakeClient) Add(_ context.Context, rec storage.RecordBatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls++
	f.lastAdd = rec
	f.upsertLocked(rec)
	return nil
}

func (f *fakeClient) Upsert(_ context.Context, rec storage.RecordBatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	f.lastUpsert = rec
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upsertLocked(rec)
	return nil
}

func (f *fakeClient) Update(_ context.Context, rec storage.RecordBatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	f.lastUpdate = rec
	if f.updateErr != nil {
		return f.updateErr
	}
	for i, id := range rec.IDs {
		r, ok := f.records[id]
		if !ok {
			return errNotFound
		}
		if i < len(rec.Documents) {
			r.document = rec.Documents[i]
		}
		if i < len(rec.Embeddings) && rec.Embeddings[i] != nil {
			r.embedding = rec.Embeddings[i]
		}
		if i < len(rec.Metadatas) && rec.Metadatas[i] != nil {
			r.metadata = mergeMetadata(r.metadata, rec.Metadatas[i])
		}
	}
	return nil
}

func (f *fakeClient) upsertLocked(rec storage.RecordBatch) {
	for i, id := range rec.IDs {
		r := f.records[id]
		if r == nil {
			r = &memRecord{id: id, metadata: map[string]any{}}
			f.records[id] = r
		}
		if i < len(rec.Documents) {
			r.document = rec.Documents[i]
		}
		if i < len(rec.Embeddings) {
			r.embedding = rec.Embeddings[i]
		}
		if i < len(rec.Metadatas) && rec.Metadatas[i] != nil {
			// Match Chroma /upsert: metadata is merged; null deletes a key.
			r.metadata = mergeMetadata(r.metadata, rec.Metadatas[i])
		}
	}
}

func mergeMetadata(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for key, value := range src {
		if value == nil {
			delete(dst, key)
			continue
		}
		dst[key] = value
	}
	return dst
}

func (f *fakeClient) Get(_ context.Context, p storage.GetParams) (*storage.GetResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	f.lastGet = p
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getNil {
		return nil, nil
	}
	if f.repeatPage {
		recs := make([]*memRecord, defaultMaxRequestRecords)
		for i := range recs {
			recs[i] = &memRecord{id: "repeated-page-" + strconv.Itoa(i), document: "d", metadata: map[string]any{}}
		}
		return recordsToGet(recs), nil
	}
	if f.fullPageGets > 0 {
		f.fullPageGets--
		recs := make([]*memRecord, defaultMaxRequestRecords)
		for i := range recs {
			recs[i] = &memRecord{id: "page-" + strconv.Itoa(f.getCalls) + "-" + strconv.Itoa(i), document: "d", metadata: map[string]any{}}
		}
		return recordsToGet(recs), nil
	}
	var recs []*memRecord
	if len(p.IDs) > 0 {
		for _, id := range p.IDs {
			if r, ok := f.records[id]; ok {
				recs = append(recs, r)
			}
		}
	} else {
		for _, r := range f.records {
			recs = append(recs, r)
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].id < recs[j].id })
	}
	var filtered []*memRecord
	for _, r := range recs {
		if !matchWhere(r.metadata, p.Where) {
			continue
		}
		if !matchWhereDocument(r.document, p.WhereDocument) {
			continue
		}
		filtered = append(filtered, r)
	}
	offset := 0
	if p.Offset != nil {
		offset = *p.Offset
	}
	if offset > len(filtered) {
		offset = len(filtered)
	}
	filtered = filtered[offset:]
	if p.Limit != nil && *p.Limit >= 0 && *p.Limit < len(filtered) {
		filtered = filtered[:*p.Limit]
	}
	return recordsToGet(filtered), nil
}

func (f *fakeClient) Delete(_ context.Context, p storage.DeleteParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.lastDelete = p
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.deleteNoop {
		return nil
	}
	if len(p.IDs) > 0 {
		for _, id := range p.IDs {
			delete(f.records, id)
		}
		return nil
	}
	for id, r := range f.records {
		if matchWhere(r.metadata, p.Where) && matchWhereDocument(r.document, p.WhereDocument) {
			delete(f.records, id)
		}
	}
	return nil
}

func (f *fakeClient) Query(_ context.Context, p storage.QueryParams) (*storage.QueryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queryCalls++
	f.lastQuery = p
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if len(p.QueryEmbeddings) == 0 {
		return &storage.QueryResult{}, nil
	}
	q := p.QueryEmbeddings[0]
	type scored struct {
		r    *memRecord
		dist float32
	}
	var hits []scored
	for _, r := range f.records {
		if len(p.IDs) > 0 && !containsString(p.IDs, r.id) {
			continue
		}
		if !matchWhere(r.metadata, p.Where) {
			continue
		}
		if !matchWhereDocument(r.document, p.WhereDocument) {
			continue
		}
		hits = append(hits, scored{r: r, dist: cosineDistance(q, r.embedding)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].dist < hits[j].dist })
	n := p.NResults
	if n <= 0 || n > len(hits) {
		n = len(hits)
	}
	hits = hits[:n]
	ids := make([]string, len(hits))
	docs := make([]string, len(hits))
	mds := make([]map[string]any, len(hits))
	embs := make([][]float32, len(hits))
	dists := make([]float32, len(hits))
	for i, h := range hits {
		ids[i] = h.r.id
		docs[i] = h.r.document
		mds[i] = cloneMap(h.r.metadata)
		embs[i] = h.r.embedding
		dists[i] = h.dist
	}
	return &storage.QueryResult{
		IDs:        [][]string{ids},
		Documents:  [][]string{docs},
		Metadatas:  [][]map[string]any{mds},
		Embeddings: [][][]float32{embs},
		Distances:  [][]float32{dists},
	}, nil
}

func (f *fakeClient) Search(_ context.Context, p storage.SearchParams) (*storage.SearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searchCalls++
	f.lastSearch = p
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	var recs []*memRecord
	for _, r := range f.records {
		if len(p.IDs) > 0 && !containsString(p.IDs, r.id) {
			continue
		}
		if !matchSearchFilter(r, p.Filter) {
			continue
		}
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].id < recs[j].id })
	if p.Limit > 0 && len(recs) > p.Limit {
		recs = recs[:p.Limit]
	}
	ids := make([]string, len(recs))
	documents := make([]*string, len(recs))
	metadatas := make([]map[string]any, len(recs))
	scores := make([]*float32, len(recs))
	for i, r := range recs {
		ids[i] = r.id
		score := -float32(defaultRRFOffset) / float32(defaultRRFOffset+i)
		document := r.document
		documents[i] = &document
		metadatas[i] = cloneMap(r.metadata)
		scores[i] = &score
	}
	return &storage.SearchResult{
		IDs:       [][]string{ids},
		Documents: [][]*string{documents},
		Metadatas: [][]map[string]any{metadatas},
		Scores:    [][]*float32{scores},
		Select:    [][]string{{"#document", "#metadata", "#score"}},
	}, nil
}

func (f *fakeClient) Count(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countCalls++
	if f.countErr != nil {
		return 0, f.countErr
	}
	return len(f.records), nil
}

func (f *fakeClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}

func recordsToGet(recs []*memRecord) *storage.GetResult {
	out := &storage.GetResult{
		IDs:        make([]string, len(recs)),
		Documents:  make([]string, len(recs)),
		Metadatas:  make([]map[string]any, len(recs)),
		Embeddings: make([][]float32, len(recs)),
	}
	for i, r := range recs {
		out.IDs[i] = r.id
		out.Documents[i] = r.document
		out.Metadatas[i] = cloneMap(r.metadata)
		out.Embeddings[i] = r.embedding
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func containsString(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func cosineDistance(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	sim := dot / (math.Sqrt(na) * math.Sqrt(nb))
	d := 1 - sim
	if d < 0 {
		return 0
	}
	return float32(d)
}

func matchSearchFilter(r *memRecord, filter map[string]any) bool {
	if r == nil || len(filter) == 0 {
		return true
	}
	if raw, ok := filter["$and"]; ok {
		subs, _ := raw.([]any)
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if !matchSearchFilter(r, m) {
				return false
			}
		}
		return true
	}
	if raw, ok := filter["$or"]; ok {
		subs, _ := raw.([]any)
		if len(subs) == 0 {
			return false
		}
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if matchSearchFilter(r, m) {
				return true
			}
		}
		return false
	}
	if pred, ok := filter["#document"]; ok {
		m, _ := pred.(map[string]any)
		if !matchWhereDocument(r.document, m) {
			return false
		}
		if len(filter) == 1 {
			return true
		}
		rest := make(map[string]any, len(filter)-1)
		for k, v := range filter {
			if k != "#document" {
				rest[k] = v
			}
		}
		return matchSearchFilter(r, rest)
	}
	if pred, ok := filter["#id"]; ok {
		op, _ := pred.(map[string]any)
		if ids, ok := op["$in"]; ok && !inList(r.id, ids) {
			return false
		}
		if ids, ok := op["$nin"]; ok && inList(r.id, ids) {
			return false
		}
		if len(filter) == 1 {
			return true
		}
		rest := make(map[string]any, len(filter)-1)
		for k, v := range filter {
			if k != "#id" {
				rest[k] = v
			}
		}
		return matchSearchFilter(r, rest)
	}
	return matchWhere(r.metadata, filter)
}

func matchWhereDocument(content string, where map[string]any) bool {
	if len(where) == 0 {
		return true
	}
	if raw, ok := where["$and"]; ok {
		subs, _ := raw.([]any)
		if len(subs) == 0 {
			return true
		}
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if !matchWhereDocument(content, m) {
				return false
			}
		}
		return true
	}
	if raw, ok := where["$or"]; ok {
		subs, _ := raw.([]any)
		if len(subs) == 0 {
			return false
		}
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if matchWhereDocument(content, m) {
				return true
			}
		}
		return false
	}
	if v, ok := where["$contains"]; ok {
		s, _ := v.(string)
		return strings.Contains(content, s)
	}
	if v, ok := where["$regex"]; ok {
		s, _ := v.(string)
		re, err := regexp.Compile(s)
		return err == nil && re.MatchString(content)
	}
	if v, ok := where["$not_contains"]; ok {
		s, _ := v.(string)
		return !strings.Contains(content, s)
	}
	return true
}

func matchWhere(md map[string]any, where map[string]any) bool {
	if len(where) == 0 {
		return true
	}
	if raw, ok := where["$and"]; ok {
		subs, _ := raw.([]any)
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if !matchWhere(md, m) {
				return false
			}
		}
		return true
	}
	if raw, ok := where["$or"]; ok {
		subs, _ := raw.([]any)
		for _, s := range subs {
			m, _ := s.(map[string]any)
			if matchWhere(md, m) {
				return true
			}
		}
		return false
	}
	if len(where) > 1 {
		for k, v := range where {
			if !matchWhere(md, map[string]any{k: v}) {
				return false
			}
		}
		return true
	}
	var field string
	var raw any
	for k, v := range where {
		field, raw = k, v
	}
	opMap, ok := raw.(map[string]any)
	if !ok {
		got, exists := md[field]
		return exists && equals(got, raw)
	}
	for op, val := range opMap {
		got, exists := md[field]
		if !exists {
			return false
		}
		switch op {
		case "$eq":
			if !equals(got, val) {
				return false
			}
		case "$ne":
			if equals(got, val) {
				return false
			}
		case "$gt":
			left, leftOK := numeric(got)
			right, rightOK := numeric(val)
			if !leftOK || !rightOK || left <= right {
				return false
			}
		case "$gte":
			left, leftOK := numeric(got)
			right, rightOK := numeric(val)
			if !leftOK || !rightOK || left < right {
				return false
			}
		case "$lt":
			left, leftOK := numeric(got)
			right, rightOK := numeric(val)
			if !leftOK || !rightOK || left >= right {
				return false
			}
		case "$lte":
			left, leftOK := numeric(got)
			right, rightOK := numeric(val)
			if !leftOK || !rightOK || left > right {
				return false
			}
		case "$in":
			if !inList(got, val) {
				return false
			}
		case "$nin":
			if inList(got, val) {
				return false
			}
		}
	}
	return true
}

func equals(a, b any) bool {
	if sa, ok := a.(string); ok {
		sb, _ := b.(string)
		return sa == sb
	}
	if ba, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ba == bb
	}
	left, leftOK := numeric(a)
	right, rightOK := numeric(b)
	return leftOK && rightOK && left == right
}

func inList(got, val any) bool {
	items, _ := val.([]any)
	for _, item := range items {
		if equals(got, item) {
			return true
		}
	}
	return false
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	default:
		return 0, false
	}
}
