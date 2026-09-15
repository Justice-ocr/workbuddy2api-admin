package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminTokenIsNotAcceptedAsPublicAPIKey(t *testing.T) {
	if len(strings.TrimSpace("admin-token-example")) > 0 {
		// Configuration validation is the contract: distinct credentials are required.
		c := Default()
		c.APIKey = strings.Repeat("a", 32)
		c.AdminToken = strings.Repeat("b", 32)
		c.Global.Enabled = true
		c.AdminListen = "127.0.0.1:7864"
		if err := c.validateAdmin(); err != nil {
			t.Fatalf("distinct credentials rejected: %v", err)
		}
		c.AdminToken = c.APIKey
		if err := c.validateAdmin(); err == nil {
			t.Fatal("admin token must not equal public API key")
		}
	}
}

func TestAdminRejectsNonLoopbackHostAndMissingRequestHeader(t *testing.T) {
	a := newAdminServer(nil, nil, nil, ".", "", strings.Repeat("x", 32))
	req := httptest.NewRequest(http.MethodGet, "http://example.test/api/overview", nil)
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("host status=%d want 403", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7864/api/overview", nil)
	req.Host = "127.0.0.1:7864"
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("request marker status=%d want 403", rec.Code)
	}
}

func TestAdminServesFireflyAssetsWithoutAuthentication(t *testing.T) {
	a := newAdminServer(nil, nil, nil, ".", "", strings.Repeat("x", 32))
	paths := []string{"/", "/admin-icons.js", "/assets/wallpapers/desktop/d1.avif", "/pio/static/spine-player.min.js"}
	astroAssets, err := fs.Glob(adminAssets, "admin-dist/_astro/*")
	if err != nil || len(astroAssets) < 2 {
		t.Fatalf("embedded Astro assets unavailable: %v %v", astroAssets, err)
	}
	for _, asset := range astroAssets {
		paths = append(paths, strings.TrimPrefix(asset, "admin-dist"))
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7864"+path, nil)
		req.Host = "127.0.0.1:7864"
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("asset %s status=%d want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "media-src 'self'") || strings.Contains(got, "https:") {
			t.Fatalf("asset %s has unexpected CSP %q", path, got)
		}
	}
}
