package jsonschema

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type Address struct {
	Street string `json:"street"`
	City   string `json:"city"`
	Zip    string `json:"zip"`
}

type User struct {
	ID        int            `json:"id"`
	Name      string         `json:"name" description:"user full name"`
	Email     string         `json:"email,omitempty"`
	Active    bool           `json:"active"`
	Score     float64        `json:"score"`
	CreatedAt time.Time      `json:"created_at"`
	Tags      []string       `json:"tags"`
	Meta      map[string]int `json:"meta"`
	Address   Address        `json:"address"`
}

func TestGenerator_SimpleStruct(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(User{}))
	if schema["type"] != "object" {
		t.Fatalf("expected root type object, got %v", schema["type"])
	}
	// For non-recursive structs, we inline schemas and omit $defs.
	if _, ok := schema["$defs"]; ok {
		t.Fatalf("did not expect $defs for simple non-recursive struct")
	}
}

func TestGenerator_JSONMarshalling(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(&User{}))
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema error: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal schema error: %v", err)
	}
}

type RecursiveNode struct {
	Name string         `json:"name"`
	Next *RecursiveNode `json:"next,omitempty"`
}

func TestGenerator_Recursive(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(&RecursiveNode{}))
	if _, ok := schema["$defs"]; !ok {
		t.Fatalf("expected $defs for recursive type")
	}
}

type Mixed struct {
	OptStr *string        `json:"opt_str,omitempty"`
	Arr    []int          `json:"arr"`
	MapStr map[string]int `json:"map_str"`
	AnyMap map[string]any `json:"any_map"`
}

func TestGenerator_MixedKinds(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(Mixed{}))
	if schema["type"] != "object" {
		t.Fatalf("expected object root")
	}
}
