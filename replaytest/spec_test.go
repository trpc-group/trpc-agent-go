// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"os"
	"strings"
	"testing"
)

func TestValidate_RejectsUnknownOp(t *testing.T) {
	s := validSpec()
	s.Operations[0].Op = "bad_op"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("expected unknown op error, got: %v", err)
	}
}

func TestValidate_RejectsUnknownWhat(t *testing.T) {
	s := validSpec()
	s.Verifies[0].What = "bad_what"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown what") {
		t.Errorf("expected unknown what error, got: %v", err)
	}
}

func TestValidate_RejectsUnknownBackend(t *testing.T) {
	s := validSpec()
	s.Operations[0].Backend = "database"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("expected unknown backend error, got: %v", err)
	}
}

func TestValidate_RejectsZeroOperations(t *testing.T) {
	s := validSpec()
	s.Operations = nil
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one operation") {
		t.Errorf("expected zero operations error, got: %v", err)
	}
}

func TestValidate_RejectsZeroVerifies(t *testing.T) {
	s := validSpec()
	s.Verifies = nil
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one verify") {
		t.Errorf("expected zero verifies error, got: %v", err)
	}
}

func validSpec() *Spec {
	return &Spec{
		Name:        "test",
		Description: "desc",
		Backends:    BackendConfig{Session: []string{"inmemory"}, Memory: []string{"inmemory"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: nil}},
		Verifies:    []VerifySpec{{What: "events"}},
	}
}

func TestValidate_Success(t *testing.T) {
	s := validSpec()
	err := s.Validate()
	if err != nil {
		t.Errorf("valid spec should pass validation, got: %v", err)
	}
}

func TestValidate_RejectsEmptyName(t *testing.T) {
	s := validSpec()
	s.Name = ""
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected 'name is required', got: %v", err)
	}
}

func TestValidate_RejectsEmptyDescription(t *testing.T) {
	s := validSpec()
	s.Description = ""
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "description is required") {
		t.Errorf("expected 'description is required', got: %v", err)
	}
}

func TestValidate_RejectsEmptySessionBackends(t *testing.T) {
	s := validSpec()
	s.Backends.Session = nil
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one session backend") {
		t.Errorf("expected 'at least one session backend', got: %v", err)
	}
}

func TestValidate_RejectsEmptyMemoryBackends(t *testing.T) {
	s := validSpec()
	s.Backends.Memory = nil
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one memory backend") {
		t.Errorf("expected 'at least one memory backend', got: %v", err)
	}
}

func TestValidate_RejectsEmptyAppName(t *testing.T) {
	s := validSpec()
	s.Setup.AppName = ""
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "setup.app_name is required") {
		t.Errorf("expected 'setup.app_name is required', got: %v", err)
	}
}

func TestValidate_RejectsEmptyUserID(t *testing.T) {
	s := validSpec()
	s.Setup.UserID = ""
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "setup.user_id is required") {
		t.Errorf("expected 'setup.user_id is required', got: %v", err)
	}
}

func TestValidate_RejectsEmptySessionID(t *testing.T) {
	s := validSpec()
	s.Setup.SessionID = ""
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "setup.session_id is required") {
		t.Errorf("expected 'setup.session_id is required', got: %v", err)
	}
}

func TestHasTag_Positive(t *testing.T) {
	s := validSpec()
	s.Tags = []string{"lightweight", "concurrent"}
	if !s.HasTag("lightweight") {
		t.Error("HasTag should return true for existing tag")
	}
	if !s.HasTag("concurrent") {
		t.Error("HasTag should return true for second tag")
	}
}

func TestHasTag_Negative(t *testing.T) {
	s := validSpec()
	s.Tags = []string{"lightweight"}
	if s.HasTag("integration") {
		t.Error("HasTag should return false for missing tag")
	}
}

func TestHasTag_EmptyTags(t *testing.T) {
	s := validSpec()
	s.Tags = nil
	if s.HasTag("anything") {
		t.Error("HasTag should return false when tags are nil")
	}
}

func TestLoadSpec_Success(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test_spec.json"
	content := `{
		"name": "test-case",
		"description": "a test case",
		"backends": {"session": ["inmemory"], "memory": ["inmemory"]},
		"setup": {"app_name": "app", "user_id": "u1", "session_id": "s1"},
		"operations": [{"op": "create_session", "backend": "session", "params": null}],
		"verifies": [{"what": "events"}]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	spec, err := LoadSpec(path)
	if err != nil {
		t.Fatalf("LoadSpec should succeed: %v", err)
	}
	if spec.Name != "test-case" {
		t.Errorf("Name = %q, want 'test-case'", spec.Name)
	}
}

func TestLoadSpec_FileNotFound(t *testing.T) {
	_, err := LoadSpec("/nonexistent/path/spec.json")
	if err == nil {
		t.Error("LoadSpec should fail for nonexistent file")
	}
}

func TestLoadSpec_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bad.json"
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSpec(path)
	if err == nil {
		t.Error("LoadSpec should fail for invalid JSON")
	}
}

func TestLoadSpec_InvalidSpec(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/invalid_spec.json"
	content := `{
		"name": "",
		"description": "missing backends",
		"backends": {"session": [], "memory": []},
		"setup": {"app_name": "", "user_id": ""},
		"operations": [],
		"verifies": []
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSpec(path)
	if err == nil {
		t.Error("LoadSpec should fail for invalid spec")
	}
}

func TestLoadSpecsFromDir(t *testing.T) {
	dir := t.TempDir()
	spec1 := `{"name":"a","description":"d","backends":{"session":["inmemory"],"memory":["inmemory"]},"setup":{"app_name":"app","user_id":"u","session_id":"s"},"operations":[{"op":"create_session","backend":"session","params":null}],"verifies":[{"what":"events"}]}`
	spec2 := `{"name":"b","description":"d","backends":{"session":["inmemory"],"memory":["inmemory"]},"setup":{"app_name":"app","user_id":"u","session_id":"s"},"operations":[{"op":"get_session","backend":"session","params":null}],"verifies":[{"what":"state"}]}`

	if err := os.WriteFile(dir+"/spec_a.json", []byte(spec1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/spec_b.json", []byte(spec2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/readme.txt", []byte("not a spec"), 0644); err != nil {
		t.Fatal(err)
	}

	specs, err := LoadSpecsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadSpecsFromDir: %v", err)
	}
	if len(specs) != 2 {
		t.Errorf("expected 2 specs, got %d", len(specs))
	}
}

func TestLoadSpecsFromDir_InvalidSpecFails(t *testing.T) {
	dir := t.TempDir()
	valid := `{"name":"ok","description":"d","backends":{"session":["inmemory"],"memory":["inmemory"]},"setup":{"app_name":"app","user_id":"u","session_id":"s"},"operations":[{"op":"create_session","backend":"session","params":null}],"verifies":[{"what":"events"}]}`
	invalid := `{"name":""}`

	if err := os.WriteFile(dir+"/ok.json", []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/bad.json", []byte(invalid), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSpecsFromDir(dir)
	if err == nil {
		t.Error("LoadSpecsFromDir should fail when any spec is invalid")
	}
}
