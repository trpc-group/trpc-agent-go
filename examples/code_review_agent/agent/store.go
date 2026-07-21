package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store interface {
	Init() error
	SaveTask(ReviewTask) error
	UpdateTask(ReviewTask) error
	SavePermissionDecision(PermissionDecision) error
	SaveSandboxRun(SandboxRun) error
	SaveFinding(taskID string, finding Finding, bucket string) error
	SaveArtifact(Artifact) error
	SaveMonitoringSummary(MonitoringSummary) error
	SaveReport(taskID string, report ReviewReport) error
	GetTaskRecord(taskID string) (TaskRecord, error)
}

type JSONStore struct {
	Path string
	mu   sync.Mutex
}

type storeDB struct {
	Tasks               map[string]ReviewTask           `json:"tasks"`
	PermissionDecisions map[string][]PermissionDecision `json:"permission_decisions"`
	SandboxRuns         map[string][]SandboxRun         `json:"sandbox_runs"`
	Findings            map[string][]storedFinding      `json:"findings"`
	Artifacts           map[string][]Artifact           `json:"artifacts"`
	Monitoring          map[string]MonitoringSummary    `json:"monitoring"`
	Reports             map[string]ReviewReport         `json:"reports"`
}

type storedFinding struct {
	Bucket  string  `json:"bucket"`
	Finding Finding `json:"finding"`
}

type TaskRecord struct {
	Task                ReviewTask           `json:"task"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	Findings            []Finding            `json:"findings"`
	Warnings            []Finding            `json:"warnings"`
	NeedsHumanReview    []Finding            `json:"needs_human_review"`
	Artifacts           []Artifact           `json:"artifacts"`
	Monitoring          MonitoringSummary    `json:"monitoring"`
	Report              ReviewReport         `json:"report"`
}

func (s *JSONStore) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Path == "" {
		return fmt.Errorf("store path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(s.Path); err == nil {
		return nil
	}
	return s.writeLocked(newStoreDB())
}

func (s *JSONStore) SaveTask(task ReviewTask) error {
	return s.update(func(db *storeDB) {
		db.Tasks[task.ID] = task
	})
}

func (s *JSONStore) UpdateTask(task ReviewTask) error {
	return s.SaveTask(task)
}

func (s *JSONStore) SavePermissionDecision(d PermissionDecision) error {
	return s.update(func(db *storeDB) {
		db.PermissionDecisions[d.TaskID] = append(db.PermissionDecisions[d.TaskID], d)
	})
}

func (s *JSONStore) SaveSandboxRun(run SandboxRun) error {
	return s.update(func(db *storeDB) {
		db.SandboxRuns[run.TaskID] = append(db.SandboxRuns[run.TaskID], run)
	})
}

func (s *JSONStore) SaveFinding(taskID string, finding Finding, bucket string) error {
	return s.update(func(db *storeDB) {
		db.Findings[taskID] = append(db.Findings[taskID], storedFinding{Bucket: bucket, Finding: finding})
	})
}

func (s *JSONStore) SaveArtifact(artifact Artifact) error {
	return s.update(func(db *storeDB) {
		db.Artifacts[artifact.TaskID] = append(db.Artifacts[artifact.TaskID], artifact)
	})
}

func (s *JSONStore) SaveMonitoringSummary(summary MonitoringSummary) error {
	return s.update(func(db *storeDB) {
		db.Monitoring[summary.TaskID] = summary
	})
}

func (s *JSONStore) SaveReport(taskID string, report ReviewReport) error {
	return s.update(func(db *storeDB) {
		db.Reports[taskID] = report
	})
}

func (s *JSONStore) GetTaskRecord(taskID string) (TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.readLocked()
	if err != nil {
		return TaskRecord{}, err
	}
	task, ok := db.Tasks[taskID]
	if !ok {
		return TaskRecord{}, fmt.Errorf("task %s not found", taskID)
	}
	record := TaskRecord{
		Task:                task,
		PermissionDecisions: db.PermissionDecisions[taskID],
		SandboxRuns:         db.SandboxRuns[taskID],
		Artifacts:           db.Artifacts[taskID],
		Monitoring:          db.Monitoring[taskID],
		Report:              db.Reports[taskID],
	}
	for _, stored := range db.Findings[taskID] {
		switch stored.Bucket {
		case "finding":
			record.Findings = append(record.Findings, stored.Finding)
		case "warning":
			record.Warnings = append(record.Warnings, stored.Finding)
		case "needs_human_review":
			record.NeedsHumanReview = append(record.NeedsHumanReview, stored.Finding)
		}
	}
	return record, nil
}

func (s *JSONStore) update(fn func(*storeDB)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.readLocked()
	if err != nil {
		return err
	}
	fn(&db)
	return s.writeLocked(db)
}

func (s *JSONStore) readLocked() (storeDB, error) {
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return newStoreDB(), nil
		}
		return storeDB{}, err
	}
	if len(body) == 0 {
		return newStoreDB(), nil
	}
	var db storeDB
	if err := json.Unmarshal(body, &db); err != nil {
		return storeDB{}, err
	}
	db.ensure()
	return db, nil
}

func (s *JSONStore) writeLocked(db storeDB) error {
	db.ensure()
	body, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, body, 0o644)
}

func newStoreDB() storeDB {
	db := storeDB{}
	db.ensure()
	return db
}

func (db *storeDB) ensure() {
	if db.Tasks == nil {
		db.Tasks = map[string]ReviewTask{}
	}
	if db.PermissionDecisions == nil {
		db.PermissionDecisions = map[string][]PermissionDecision{}
	}
	if db.SandboxRuns == nil {
		db.SandboxRuns = map[string][]SandboxRun{}
	}
	if db.Findings == nil {
		db.Findings = map[string][]storedFinding{}
	}
	if db.Artifacts == nil {
		db.Artifacts = map[string][]Artifact{}
	}
	if db.Monitoring == nil {
		db.Monitoring = map[string]MonitoringSummary{}
	}
	if db.Reports == nil {
		db.Reports = map[string]ReviewReport{}
	}
}

func finalTask(task ReviewTask, status, errText string) ReviewTask {
	task.Status = status
	task.CompletedAt = time.Now().UTC()
	task.Error = errText
	return task
}
