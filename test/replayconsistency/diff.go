//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Struct tags the comparator understands.
const (
	// tagDiffKey marks the field that identifies an element inside a slice.
	// Keyed elements are matched by that value instead of by position, so a
	// missing summary is reported as missing rather than shifting every later
	// comparison.
	tagDiffKey = "diffkey"
	// tagDiffSkip excludes a field from comparison. The tag value is the
	// reason and is required: an exclusion nobody can justify in one line is
	// usually a bug being hidden.
	tagDiffSkip = "diffskip"
)

// Divergence is one field-level difference between the baseline backend and a
// compared backend.
type Divergence struct {
	Case     string `json:"case"`
	Baseline string `json:"baseline"`
	Backend  string `json:"backend"`
	// Path locates the value, for example
	// sessions[ref="app/u1/s1"].summaries[filterKey="tool"].text
	Path          string `json:"path"`
	BaselineValue string `json:"baselineValue"`
	BackendValue  string `json:"backendValue"`
	// AllowedDiff marks a difference the backends are both entitled to have.
	// Allowed differences are still reported; they simply do not fail the run.
	AllowedDiff bool `json:"allowedDiff"`
	// Known marks a difference that is real rather than legitimate, recorded
	// with evidence instead of failing the run while the question is open.
	// A divergence that is neither allowed nor known fails the case.
	Known bool `json:"known,omitempty"`
	// Reason explains an allowed or known difference.
	Reason string `json:"reason,omitempty"`
}

// Fatal reports whether the divergence should fail the case.
func (d Divergence) Fatal() bool { return !d.AllowedDiff && !d.Known }

// String renders the divergence for a test failure message.
func (d Divergence) String() string {
	s := fmt.Sprintf("%s: %s\n  %s = %s\n  %s = %s",
		d.Case, d.Path, d.Baseline, d.BaselineValue, d.Backend, d.BackendValue)
	switch {
	case d.AllowedDiff:
		s += "\n  allowed: " + d.Reason
	case d.Known:
		s += "\n  known: " + d.Reason
	}
	return s
}

// Compare walks two observations and reports every value that differs.
//
// The walk is driven by reflection over the projection rather than by
// hand-written field comparisons. That is deliberate: a hand-written
// comparator silently stops covering any field someone later adds to
// [Observation], which would erode the guarantee without anyone noticing.
func Compare(caseName string, baseline, other *Observation) []Divergence {
	c := &comparator{
		caseName: caseName,
		baseline: baseline.Backend,
		backend:  other.Backend,
	}
	c.walk("", reflect.ValueOf(baseline).Elem(), reflect.ValueOf(other).Elem())
	sort.SliceStable(c.out, func(i, j int) bool { return c.out[i].Path < c.out[j].Path })
	return c.out
}

type comparator struct {
	caseName string
	baseline string
	backend  string
	out      []Divergence
}

func (c *comparator) report(path string, base, other any) {
	d := Divergence{
		Case:          c.caseName,
		Baseline:      c.baseline,
		Backend:       c.backend,
		Path:          path,
		BaselineValue: renderValue(base),
		BackendValue:  renderValue(other),
	}
	if reason, ok := allowedDiff(path); ok {
		d.AllowedDiff = true
		d.Reason = reason
	} else if note, ok := knownDivergence(path, c.backend); ok {
		d.Known = true
		d.Reason = note
	}
	c.out = append(c.out, d)
}

func (c *comparator) walk(path string, base, other reflect.Value) {
	switch base.Kind() {
	case reflect.Ptr:
		c.walkPtr(path, base, other)
	case reflect.Struct:
		c.walkStruct(path, base, other)
	case reflect.Slice:
		c.walkSlice(path, base, other)
	default:
		if !reflect.DeepEqual(base.Interface(), other.Interface()) {
			c.report(path, base.Interface(), other.Interface())
		}
	}
}

func (c *comparator) walkPtr(path string, base, other reflect.Value) {
	switch {
	case base.IsNil() && other.IsNil():
		return
	case base.IsNil():
		c.report(path, nil, other.Elem().Interface())
	case other.IsNil():
		c.report(path, base.Elem().Interface(), nil)
	default:
		c.walk(path, base.Elem(), other.Elem())
	}
}

func (c *comparator) walkStruct(path string, base, other reflect.Value) {
	t := base.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			// Unexported fields never reach the projection.
			continue
		}
		if _, skip := field.Tag.Lookup(tagDiffSkip); skip {
			continue
		}
		c.walk(joinPath(path, fieldName(field)), base.Field(i), other.Field(i))
	}
}

func (c *comparator) walkSlice(path string, base, other reflect.Value) {
	elemType := base.Type().Elem()
	if keyIdx, ok := diffKeyIndex(elemType); ok {
		c.walkKeyedSlice(path, base, other, elemType, keyIdx)
		return
	}
	c.walkIndexedSlice(path, base, other)
}

// walkIndexedSlice compares position by position, which is the right model
// when order is part of the contract, as it is for events and track entries.
func (c *comparator) walkIndexedSlice(path string, base, other reflect.Value) {
	if base.Len() != other.Len() {
		c.report(joinPath(path, "length"), base.Len(), other.Len())
	}
	n := base.Len()
	if other.Len() < n {
		n = other.Len()
	}
	for i := 0; i < n; i++ {
		c.walk(fmt.Sprintf("%s[%d]", path, i), base.Index(i), other.Index(i))
	}
}

// walkKeyedSlice matches elements by their key field so that a missing or
// extra element is reported as such instead of shifting every later element.
func (c *comparator) walkKeyedSlice(path string, base, other reflect.Value, elemType reflect.Type, keyIdx int) {
	keyName := fieldName(elemType.Field(keyIdx))
	baseByKey := indexByKey(base, keyIdx)
	otherByKey := indexByKey(other, keyIdx)

	for _, key := range unionKeys(baseByKey, otherByKey) {
		elemPath := fmt.Sprintf("%s[%s=%q]", path, keyName, key)
		baseElem, inBase := baseByKey[key]
		otherElem, inOther := otherByKey[key]
		switch {
		case !inOther:
			c.report(elemPath, baseElem.Interface(), nil)
		case !inBase:
			c.report(elemPath, nil, otherElem.Interface())
		default:
			c.walk(elemPath, baseElem, otherElem)
		}
	}
}

func indexByKey(slice reflect.Value, keyIdx int) map[string]reflect.Value {
	out := make(map[string]reflect.Value, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		elem := slice.Index(i)
		out[fmt.Sprint(elem.Field(keyIdx).Interface())] = elem
	}
	return out
}

func unionKeys(a, b map[string]reflect.Value) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// diffKeyIndex returns the index of the element field marked as the diff key.
func diffKeyIndex(t reflect.Type) (int, bool) {
	if t.Kind() != reflect.Struct {
		return 0, false
	}
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get(tagDiffKey) == "true" {
			return i, true
		}
	}
	return 0, false
}

// fieldName prefers the JSON name so that paths in the report match the field
// names in the serialized observations.
func fieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}

func joinPath(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}
