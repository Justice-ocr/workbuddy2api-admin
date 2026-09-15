package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"workbuddy2api/internal/upstream"
)

type taskObservation struct {
	UID             string    `json:"uid"`
	TaskCode        string    `json:"task_code"`
	Date            string    `json:"date"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	CompletedSeenAt time.Time `json:"completed_seen_at,omitempty"`
	Status          string    `json:"status"`
	Current         int64     `json:"current"`
	Target          int64     `json:"target"`
	CompletedSeen   bool      `json:"completed_seen"`
}

type taskObservationStore struct {
	mu   sync.Mutex
	path string
	rows map[string]taskObservation
	now  func() time.Time
}

type adminTaskSnapshot struct {
	Code                      string    `json:"task_code"`
	Name                      string    `json:"name"`
	Status                    string    `json:"accept_status"`
	Progress                  any       `json:"progress"`
	DailyState                string    `json:"daily_state"`
	FirstSeenAt               time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt                time.Time `json:"last_seen_at,omitempty"`
	CompletedSeenAt           time.Time `json:"completed_seen_at,omitempty"`
	Evidence                  string    `json:"evidence"`
	UpstreamHasCompletionTime bool      `json:"upstream_has_completion_time"`
}

func newTaskObservationStore(path string) (*taskObservationStore, error) {
	store := &taskObservationStore{path: path, rows: map[string]taskObservation{}, now: time.Now}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return nil, errors.New("invalid task observation file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []taskObservation
	if err = json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.UID != "" && row.TaskCode != "" && row.Date != "" {
			store.rows[taskObservationKey(row.UID, row.TaskCode, row.Date)] = row
		}
	}
	return store, nil
}

func taskObservationKey(uid, code, date string) string { return uid + "\x00" + code + "\x00" + date }
func taskComplete(status string) bool                  { return status == "completed" || status == "claimed" }

func (store *taskObservationStore) observe(uid string, tasks []upstream.TaskSnapshot) ([]adminTaskSnapshot, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.now().UTC()
	date := now.In(adminZone).Format("2006-01-02")
	cutoff := now.In(adminZone).AddDate(0, 0, -35).Format("2006-01-02")
	for key, row := range store.rows {
		if row.Date < cutoff {
			delete(store.rows, key)
		}
	}
	views := make([]adminTaskSnapshot, 0, len(tasks))
	for _, task := range tasks {
		key := taskObservationKey(uid, task.Code, date)
		observation, exists := store.rows[key]
		if !exists {
			observation = taskObservation{UID: uid, TaskCode: task.Code, Date: date, FirstSeenAt: now}
		}
		observation.LastSeenAt = now
		observation.Status = task.Status
		observation.Current = task.Progress.Current
		observation.Target = task.Progress.Target
		if taskComplete(task.Status) && !observation.CompletedSeen {
			observation.CompletedSeenAt = now
		}
		observation.CompletedSeen = observation.CompletedSeen || taskComplete(task.Status)
		store.rows[key] = observation
		dailyState := "observed_incomplete"
		if observation.CompletedSeen {
			dailyState = "observed_complete"
		}
		views = append(views, adminTaskSnapshot{Code: task.Code, Name: task.Name, Status: task.Status, Progress: task.Progress, DailyState: dailyState, FirstSeenAt: observation.FirstSeenAt, LastSeenAt: observation.LastSeenAt, CompletedSeenAt: observation.CompletedSeenAt, Evidence: "local_observation", UpstreamHasCompletionTime: false})
	}
	return views, store.flushLocked() == nil
}

func (store *taskObservationStore) flushLocked() error {
	rows := make([]taskObservation, 0, len(store.rows))
	for _, row := range store.rows {
		rows = append(rows, row)
	}
	sortTaskObservations(rows)
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(store.path), ".task-observations-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), store.path)
	}
	return err
}

func sortTaskObservations(rows []taskObservation) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && taskObservationKey(rows[j].UID, rows[j].TaskCode, rows[j].Date) < taskObservationKey(rows[j-1].UID, rows[j-1].TaskCode, rows[j-1].Date); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func (a *adminServer) taskStatus(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UID string `json:"uid"`
	}
	if !decodeAdmin(w, r, &request) {
		return
	}
	account := a.pool.AuthByUID(request.UID)
	if account == nil {
		adminError(w, 404, "账号不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tasks, err := a.up.ReadTasksContext(ctx, account)
	if err != nil {
		adminError(w, 502, "该版本任务查询暂不可用，未执行或领取任何任务")
		return
	}
	now := time.Now().UTC()
	businessDate := now.In(adminZone).Format("2006-01-02")
	if a.taskObservations == nil {
		views := make([]adminTaskSnapshot, 0, len(tasks))
		for _, task := range tasks {
			views = append(views, adminTaskSnapshot{Code: task.Code, Name: task.Name, Status: task.Status, Progress: task.Progress, DailyState: "unknown", Evidence: "upstream_snapshot_only", UpstreamHasCompletionTime: false})
		}
		adminJSON(w, map[string]any{"tasks": views, "queried_at": now, "business_date": businessDate, "timezone": "UTC+8", "observation_scope": "current_snapshot", "observation_persisted": false, "upstream_date_verified": false})
		return
	}
	views, persisted := a.taskObservations.observe(request.UID, tasks)
	adminJSON(w, map[string]any{"tasks": views, "queried_at": now, "business_date": businessDate, "timezone": "UTC+8", "observation_scope": "today", "observation_persisted": persisted, "upstream_date_verified": false})
}
