package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"workbuddy2api/internal/auth"
)

// TaskSnapshot is an allowlisted view of the existing task_common.py read endpoint.
// A current snapshot is not proof that a recurring task was completed today.
type TaskSnapshot struct {
	Code     string `json:"task_code"`
	Name     string `json:"name"`
	Status   string `json:"accept_status"`
	Progress struct {
		Current int64 `json:"current"`
		Target  int64 `json:"target"`
	} `json:"progress"`
}

func (c *Client) ReadTasks(a *auth.Auth) ([]TaskSnapshot, error) {
	return c.ReadTasksContext(context.Background(), a)
}

func (c *Client) ReadTasksContext(ctx context.Context, a *auth.Auth) ([]TaskSnapshot, error) {
	if a.RealmStored() == "global" && !c.GlobalEnabled {
		return nil, errors.New("global disabled")
	}
	data, err := c.growthJSONContext(ctx, a, http.MethodGet, "/v2/activity/growth/tasks", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Tasks []TaskSnapshot `json:"tasks"`
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out.Tasks == nil {
		return nil, errors.New("task schema unavailable")
	}
	return out.Tasks, nil
}
