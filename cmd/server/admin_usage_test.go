package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workbuddy2api/internal/server"
	"workbuddy2api/internal/upstream"
)

func intPointer(value int) *int           { return &value }
func floatPointer(value float64) *float64 { return &value }

func TestUsageSummaryRangeAndAggregation(t *testing.T) {
	request := httptest.NewRequest("GET", "http://127.0.0.1:7864/api/usage?from=2026-09-14&to=2026-09-15&page_size=20", nil)
	query, err := parseUsageQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	rows := []server.UsageRecord{
		{Time: time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC), Model: "global:a", UID: "u1", Status: 200, Input: intPointer(10), Output: intPointer(4), Credit: floatPointer(0.5), DurationMS: 1000, TTFBMS: 200},
		{Time: time.Date(2026, 9, 15, 3, 0, 0, 0, time.UTC), Model: "global:a", UID: "u1", Status: 500, Interrupted: true, DurationMS: 3000},
		{Time: time.Date(2026, 9, 13, 2, 0, 0, 0, time.UTC), Model: "old", UID: "u2", Status: 200},
	}
	filtered := filterUsage(rows, query)
	if len(filtered) != 2 {
		t.Fatalf("got %d rows", len(filtered))
	}
	summary := summarizeUsage(filtered)
	if summary.Total != 2 || summary.Success != 1 || summary.Error != 1 || summary.Interrupted != 1 || summary.TotalTokens != 14 || summary.UnknownCredit != 1 || summary.AvgDurationMS != 2000 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	trend, models, accounts := aggregateUsage(filtered, false)
	if len(trend) != 2 || len(models) != 1 || len(accounts) != 1 || models[0].Requests != 2 || accounts[0].Requests != 2 {
		t.Fatalf("unexpected aggregation: %#v %#v %#v", trend, models, accounts)
	}
}

func TestUsageRangeLimitAndCSVInjection(t *testing.T) {
	bad := httptest.NewRequest("GET", "http://127.0.0.1:7864/api/usage?from=2026-08-01&to=2026-09-15", nil)
	if _, err := parseUsageQuery(bad); err == nil {
		t.Fatal("oversized range accepted")
	}
	store, err := server.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.Add(server.UsageRecord{Time: time.Now(), Model: "=formula", UID: "+uid", Status: 200})
	admin := newAdminServer(nil, nil, nil, "", "", strings.Repeat("a", 32))
	admin.usage = store
	request := httptest.NewRequest("GET", "http://127.0.0.1:7864/api/usage/export", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	request.Header.Set("X-Admin-Request", "1")
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "'=formula") || !strings.Contains(response.Body.String(), "'+uid") {
		t.Fatalf("unsafe or failed CSV export: %d %q", response.Code, response.Body.String())
	}
}

func TestTaskObservationResetsAtUTC8DateBoundary(t *testing.T) {
	store, err := newTaskObservationStore(filepath.Join(t.TempDir(), "task-observations.json"))
	if err != nil {
		t.Fatal(err)
	}
	completed := upstream.TaskSnapshot{Code: "daily", Name: "每日任务", Status: "completed"}
	incomplete := upstream.TaskSnapshot{Code: "daily", Name: "每日任务", Status: "accepted"}
	store.now = func() time.Time { return time.Date(2026, 9, 15, 15, 59, 0, 0, time.UTC) }
	first, persisted := store.observe("uid", []upstream.TaskSnapshot{completed})
	if !persisted || first[0].DailyState != "observed_complete" {
		t.Fatalf("first observation: %#v", first)
	}
	store.now = func() time.Time { return time.Date(2026, 9, 15, 16, 1, 0, 0, time.UTC) }
	second, persisted := store.observe("uid", []upstream.TaskSnapshot{incomplete})
	if !persisted || second[0].DailyState != "observed_incomplete" || second[0].FirstSeenAt.Equal(first[0].FirstSeenAt) {
		t.Fatalf("date boundary did not reset: %#v", second)
	}
}
