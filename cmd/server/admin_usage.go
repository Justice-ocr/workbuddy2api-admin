package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"workbuddy2api/internal/server"
)

func (a *adminServer) usageList(w http.ResponseWriter, r *http.Request) {
	if a.usage == nil {
		adminError(w, 503, "用量记录未启用")
		return
	}
	rows, persisted := a.usage.List()
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	date := q.Get("date")
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			adminError(w, 400, "日期格式无效")
			return
		}
	}
	zone := time.FixedZone("UTC+8", 8*3600)
	filtered := []server.UsageRecord{}
	var tokens int
	var credit float64
	unknown := 0
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if date != "" && row.Time.In(zone).Format("2006-01-02") != date {
			continue
		}
		if q.Get("uid") != "" && row.UID != q.Get("uid") {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Model), strings.ToLower(q.Get("model"))) {
			continue
		}
		success := row.Status == 200 && !row.Interrupted
		if q.Get("result") == "success" && !success || q.Get("result") == "error" && success {
			continue
		}
		filtered = append(filtered, row)
		if row.Input != nil {
			tokens += *row.Input
		}
		if row.Output != nil {
			tokens += *row.Output
		}
		if row.Credit != nil {
			credit += *row.Credit
		} else {
			unknown++
		}
	}
	total := len(filtered)
	start := total
	if page <= total/50+1 {
		start = (page - 1) * 50
	}
	end := start + 50
	if end > total {
		end = total
	}
	adminJSON(w, map[string]any{"rows": filtered[start:end], "total": total, "page": page, "tokens": tokens, "credit": credit, "unknown_credit": unknown, "persisted": persisted, "timezone": "UTC+8"})
}
func (a *adminServer) taskStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UID string `json:"uid"`
	}
	if !decodeAdmin(w, r, &req) {
		return
	}
	acct := a.pool.AuthByUID(req.UID)
	if acct == nil {
		adminError(w, 404, "账号不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tasks, err := a.up.ReadTasksContext(ctx, acct)
	if err != nil {
		adminError(w, 502, "该版本任务查询暂不可用，未执行或领取任何任务")
		return
	}
	adminJSON(w, map[string]any{"tasks": tasks, "queried_at": time.Now().UTC(), "scope": "current_snapshot", "daily_verified": false})
}
