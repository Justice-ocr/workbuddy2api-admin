package main

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"workbuddy2api/internal/server"
)

var adminZone = time.FixedZone("UTC+8", 8*3600)

type usageQuery struct {
	Page, PageSize     int
	UID, Model, Result string
	From, To           time.Time
	HasFrom, HasTo     bool
}

type usageSummary struct {
	Total         int     `json:"total"`
	Success       int     `json:"success"`
	Error         int     `json:"error"`
	Interrupted   int     `json:"interrupted"`
	SuccessRate   float64 `json:"success_rate"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	TotalTokens   int     `json:"total_tokens"`
	KnownCredit   float64 `json:"known_credit"`
	UnknownCredit int     `json:"unknown_credit"`
	AvgDurationMS int64   `json:"avg_duration_ms"`
	AvgTTFBMS     int64   `json:"avg_ttfb_ms"`
}

type usageBucket struct {
	Key      string  `json:"key"`
	Requests int     `json:"requests"`
	Success  int     `json:"success"`
	Errors   int     `json:"errors"`
	Tokens   int     `json:"tokens"`
	Credit   float64 `json:"credit"`
}

type usageGroup struct {
	Key      string  `json:"key"`
	Requests int     `json:"requests"`
	Success  int     `json:"success"`
	Tokens   int     `json:"tokens"`
	Credit   float64 `json:"credit"`
}

func parseUsageQuery(r *http.Request) (usageQuery, error) {
	q := r.URL.Query()
	out := usageQuery{Page: 1, PageSize: 50, UID: q.Get("uid"), Model: q.Get("model"), Result: q.Get("result")}
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		out.Page = v
	}
	if raw := q.Get("page_size"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 100 {
			return out, fmt.Errorf("分页大小必须在 1 到 100 之间")
		}
		out.PageSize = v
	}
	if out.Result != "" && out.Result != "success" && out.Result != "error" && out.Result != "interrupted" {
		return out, fmt.Errorf("结果筛选无效")
	}
	from, to := q.Get("from"), q.Get("to")
	if date := q.Get("date"); date != "" {
		from, to = date, date
	}
	if from != "" {
		v, err := time.ParseInLocation("2006-01-02", from, adminZone)
		if err != nil {
			return out, fmt.Errorf("开始日期格式无效")
		}
		out.From, out.HasFrom = v, true
	}
	if to != "" {
		v, err := time.ParseInLocation("2006-01-02", to, adminZone)
		if err != nil {
			return out, fmt.Errorf("结束日期格式无效")
		}
		out.To, out.HasTo = v.AddDate(0, 0, 1), true
	}
	if out.HasFrom && out.HasTo {
		if !out.From.Before(out.To) {
			return out, fmt.Errorf("开始日期不能晚于结束日期")
		}
		if out.To.Sub(out.From) > 31*24*time.Hour {
			return out, fmt.Errorf("单次最多查询 31 天")
		}
	}
	return out, nil
}

func filterUsage(rows []server.UsageRecord, q usageQuery) []server.UsageRecord {
	out := make([]server.UsageRecord, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if q.HasFrom && row.Time.Before(q.From) || q.HasTo && !row.Time.Before(q.To) {
			continue
		}
		if q.UID != "" && row.UID != q.UID {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Model), strings.ToLower(q.Model)) {
			continue
		}
		success := row.Status >= 200 && row.Status < 300 && !row.Interrupted
		if q.Result == "success" && !success || q.Result == "error" && success || q.Result == "interrupted" && !row.Interrupted {
			continue
		}
		out = append(out, row)
	}
	return out
}

func usageTokenCount(row server.UsageRecord) int {
	total := 0
	if row.Input != nil {
		total += *row.Input
	}
	if row.Output != nil {
		total += *row.Output
	}
	return total
}

func summarizeUsage(rows []server.UsageRecord) usageSummary {
	var summary usageSummary
	var duration, ttfb int64
	var ttfbCount int64
	for _, row := range rows {
		summary.Total++
		if row.Interrupted {
			summary.Interrupted++
		}
		if row.Status >= 200 && row.Status < 300 && !row.Interrupted {
			summary.Success++
		} else {
			summary.Error++
		}
		if row.Input != nil {
			summary.InputTokens += *row.Input
		}
		if row.Output != nil {
			summary.OutputTokens += *row.Output
		}
		if row.Credit != nil {
			summary.KnownCredit += *row.Credit
		} else {
			summary.UnknownCredit++
		}
		duration += row.DurationMS
		if row.TTFBMS > 0 {
			ttfb += row.TTFBMS
			ttfbCount++
		}
	}
	summary.TotalTokens = summary.InputTokens + summary.OutputTokens
	if summary.Total > 0 {
		summary.SuccessRate = float64(summary.Success) * 100 / float64(summary.Total)
		summary.AvgDurationMS = duration / int64(summary.Total)
	}
	if ttfbCount > 0 {
		summary.AvgTTFBMS = ttfb / ttfbCount
	}
	return summary
}

func aggregateUsage(rows []server.UsageRecord, hourly bool) ([]usageBucket, []usageGroup, []usageGroup) {
	buckets := map[string]*usageBucket{}
	models := map[string]*usageGroup{}
	accounts := map[string]*usageGroup{}
	for _, row := range rows {
		key := row.Time.In(adminZone).Format("2006-01-02")
		if hourly {
			key = row.Time.In(adminZone).Format("2006-01-02 15:00")
		}
		bucket := buckets[key]
		if bucket == nil {
			bucket = &usageBucket{Key: key}
			buckets[key] = bucket
		}
		bucket.Requests++
		bucket.Tokens += usageTokenCount(row)
		if row.Status >= 200 && row.Status < 300 && !row.Interrupted {
			bucket.Success++
		} else {
			bucket.Errors++
		}
		if row.Credit != nil {
			bucket.Credit += *row.Credit
		}
		addGroup := func(groups map[string]*usageGroup, groupKey string) {
			if groupKey == "" {
				groupKey = "未知"
			}
			group := groups[groupKey]
			if group == nil {
				group = &usageGroup{Key: groupKey}
				groups[groupKey] = group
			}
			group.Requests++
			group.Tokens += usageTokenCount(row)
			if row.Status >= 200 && row.Status < 300 && !row.Interrupted {
				group.Success++
			}
			if row.Credit != nil {
				group.Credit += *row.Credit
			}
		}
		addGroup(models, row.Model)
		addGroup(accounts, row.UID)
	}
	trend := make([]usageBucket, 0, len(buckets))
	for _, value := range buckets {
		trend = append(trend, *value)
	}
	sort.Slice(trend, func(i, j int) bool { return trend[i].Key < trend[j].Key })
	top := func(source map[string]*usageGroup) []usageGroup {
		items := make([]usageGroup, 0, len(source))
		for _, value := range source {
			items = append(items, *value)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Requests == items[j].Requests {
				return items[i].Key < items[j].Key
			}
			return items[i].Requests > items[j].Requests
		})
		if len(items) > 10 {
			items = items[:10]
		}
		return items
	}
	return trend, top(models), top(accounts)
}

func (a *adminServer) usageRows(w http.ResponseWriter, r *http.Request) ([]server.UsageRecord, usageQuery, bool, bool) {
	if a.usage == nil {
		adminError(w, 503, "用量记录未启用")
		return nil, usageQuery{}, false, false
	}
	query, err := parseUsageQuery(r)
	if err != nil {
		adminError(w, 400, err.Error())
		return nil, query, false, false
	}
	rows, persisted := a.usage.List()
	return filterUsage(rows, query), query, persisted, true
}

func (a *adminServer) usageList(w http.ResponseWriter, r *http.Request) {
	filtered, query, persisted, ok := a.usageRows(w, r)
	if !ok {
		return
	}
	total := len(filtered)
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	hourly := query.HasFrom && query.HasTo && query.To.Sub(query.From) <= 2*24*time.Hour
	trend, byModel, byAccount := aggregateUsage(filtered, hourly)
	summary := summarizeUsage(filtered)
	adminJSON(w, map[string]any{
		"rows": filtered[start:end], "page": query.Page, "page_size": query.PageSize,
		"summary": summary, "total": summary.Total, "tokens": summary.TotalTokens, "credit": summary.KnownCredit, "unknown_credit": summary.UnknownCredit,
		"trend": trend, "by_model": byModel, "by_account": byAccount,
		"trend_granularity": map[bool]string{true: "hour", false: "day"}[hourly], "persisted": persisted, "timezone": "UTC+8",
	})
}

func csvCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func (a *adminServer) usageExport(w http.ResponseWriter, r *http.Request) {
	rows, _, _, ok := a.usageRows(w, r)
	if !ok {
		return
	}
	if len(rows) > 5000 {
		rows = rows[:5000]
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="workbuddy-usage.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"时间(UTC+8)", "模型", "账号UID", "模式", "状态码", "是否中断", "输入Token", "输出Token", "积分", "首帧毫秒", "总耗时毫秒"})
	for _, row := range rows {
		value := func(v any) string {
			if v == nil {
				return ""
			}
			return fmt.Sprint(v)
		}
		var input, output, credit any
		if row.Input != nil {
			input = *row.Input
		}
		if row.Output != nil {
			output = *row.Output
		}
		if row.Credit != nil {
			credit = *row.Credit
		}
		_ = writer.Write([]string{row.Time.In(adminZone).Format("2006-01-02 15:04:05"), csvCell(row.Model), csvCell(row.UID), csvCell(row.Mode), strconv.Itoa(row.Status), strconv.FormatBool(row.Interrupted), value(input), value(output), value(credit), strconv.FormatInt(row.TTFBMS, 10), strconv.FormatInt(row.DurationMS, 10)})
	}
	writer.Flush()
}
