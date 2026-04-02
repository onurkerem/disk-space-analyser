package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"disk-space-analyser/internal/db"
)

// seedDB inserts directory entries into the in-memory database.
func seedDB(t *testing.T, database *db.DB, entries []db.DirEntry) {
	t.Helper()
	ctx := context.Background()
	for _, e := range entries {
		err := database.UpsertDir(ctx, e.Path, e.ParentPath, e.Name, e.Size, e.Mtime, 1000, e.Shallow)
		if err != nil {
			t.Fatalf("seed upsert %s: %v", e.Path, err)
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, 0, "/", "")
}

func TestSummaryDefault(t *testing.T) {
	srv := newTestServer(t)
	entries := []db.DirEntry{
		{Path: "/a", ParentPath: "/", Name: "a", Size: 100},
		{Path: "/b", ParentPath: "/", Name: "b", Size: 500},
		{Path: "/c", ParentPath: "/", Name: "c", Size: 300},
		{Path: "/d", ParentPath: "/", Name: "d", Size: 200},
		{Path: "/e", ParentPath: "/", Name: "e", Size: 400},
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/summary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 5 {
		t.Fatalf("len = %d, want 5", len(dirs))
	}
	// Verify sorted by size descending.
	for i := 1; i < len(dirs); i++ {
		if dirs[i].Size > dirs[i-1].Size {
			t.Errorf("dirs[%d].size=%d > dirs[%d].size=%d, want descending", i, dirs[i].Size, i-1, dirs[i-1].Size)
		}
	}
}

func TestSummaryTopParam(t *testing.T) {
	srv := newTestServer(t)
	var entries []db.DirEntry
	for i := 0; i < 10; i++ {
		entries = append(entries, db.DirEntry{
			Path: fmt.Sprintf("/dir%d", i), ParentPath: "/", Name: fmt.Sprintf("dir%d", i), Size: int64((i + 1) * 100),
		})
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/summary?top=3")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 3 {
		t.Fatalf("len = %d, want 3", len(dirs))
	}
}

func TestSummaryInvalidTop(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/summary?top=abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Error != "invalid top parameter" {
		t.Errorf("error = %q, want %q", errResp.Error, "invalid top parameter")
	}
}

func TestSummaryTopCapped(t *testing.T) {
	srv := newTestServer(t)
	// Seed 101 non-shallow directories.
	var entries []db.DirEntry
	for i := 0; i < 101; i++ {
		entries = append(entries, db.DirEntry{
			Path: fmt.Sprintf("/dir%d", i), ParentPath: "/", Name: fmt.Sprintf("dir%d", i), Size: int64(i + 1),
		})
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/summary?top=999")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) > 100 {
		t.Fatalf("len = %d, want <= 100", len(dirs))
	}
}

func TestSummaryEmptyDB(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/summary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("len = %d, want 0", len(dirs))
	}
}

func TestTreeRoot(t *testing.T) {
	srv := newTestServer(t)
	entries := []db.DirEntry{
		{Path: "/Users", ParentPath: "/", Name: "Users", Size: 1000},
		{Path: "/Applications", ParentPath: "/", Name: "Applications", Size: 2000},
		{Path: "/System", ParentPath: "/", Name: "System", Size: 500},
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/tree")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 3 {
		t.Fatalf("len = %d, want 3", len(dirs))
	}
}

func TestTreeWithPath(t *testing.T) {
	srv := newTestServer(t)
	entries := []db.DirEntry{
		{Path: "/Users/kerem", ParentPath: "/Users", Name: "kerem", Size: 5000},
		{Path: "/Users/shared", ParentPath: "/Users", Name: "shared", Size: 3000},
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/tree?path=/Users")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("len = %d, want 2", len(dirs))
	}
	// Verify sorted by size desc.
	if dirs[0].Name != "kerem" {
		t.Errorf("first = %q, want kerem (largest)", dirs[0].Name)
	}
}

func TestTreePagination(t *testing.T) {
	srv := newTestServer(t)
	var entries []db.DirEntry
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("child%d", i)
		entries = append(entries, db.DirEntry{
			Path: fmt.Sprintf("/%s", name), ParentPath: "/", Name: name, Size: int64((i + 1) * 100),
		})
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Get 3 items starting at offset 2.
	// Sorted by size desc: child10(1000), child9(900), ..., child1(100)
	// Offset 2 → skip child10 and child9 → child8, child7, child6
	resp, err := http.Get(ts.URL + "/api/tree?path=/&limit=3&offset=2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 3 {
		t.Fatalf("len = %d, want 3", len(dirs))
	}
	// Offset 2 means skip the 2 largest. Sizes: 1000,900 are skipped.
	// Remaining top 3: 800,700,600
	if dirs[0].Size != 800 {
		t.Errorf("dirs[0].size = %d, want 800", dirs[0].Size)
	}
}

func TestTreeEmptyPath(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/tree?path=/nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var dirs []DirResponse
	if err := json.NewDecoder(resp.Body).Decode(&dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("len = %d, want 0", len(dirs))
	}
}

func TestRootRedirect(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Don't follow redirects.
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/report" {
		t.Errorf("Location = %q, want /report", loc)
	}
}

func TestReportPlaceholder(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/report")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}

	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	bodyStr := string(body[:n])
	if !containsSubstring(bodyStr, "<!DOCTYPE") && !containsSubstring(bodyStr, "<html") {
		t.Errorf("response body does not contain HTML doctype or html tag; got: %s", bodyStr[:min(100, len(bodyStr))])
	}
}

func TestJSONContentType(t *testing.T) {
	srv := newTestServer(t)
	entries := []db.DirEntry{
		{Path: "/a", ParentPath: "/", Name: "a", Size: 100},
	}
	seedDB(t, srv.database, entries)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Check /api/summary
	resp, err := http.Get(ts.URL + "/api/summary")
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("summary Content-Type = %q, want application/json", ct)
	}
	resp.Body.Close()

	// Check /api/tree
	resp, err = http.Get(ts.URL + "/api/tree")
	if err != nil {
		t.Fatalf("get tree: %v", err)
	}
	ct = resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("tree Content-Type = %q, want application/json", ct)
	}
	resp.Body.Close()
}

func TestMeta(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/meta")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["root"] != "/" {
		t.Errorf("root = %q, want /", result["root"])
	}
}

func TestMetaCustomRoot(t *testing.T) {
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer database.Close()

	srv := New(database, 0, "/Users/kerem", "")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/meta")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["root"] != "/Users/kerem" {
		t.Errorf("root = %q, want /Users/kerem", result["root"])
	}
}

func TestStaticAssets(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Test CSS
	resp, err := http.Get(ts.URL + "/static/style.css")
	if err != nil {
		t.Fatalf("get css: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("css status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !containsSubstring(ct, "text/css") {
		t.Errorf("css Content-Type = %q, want text/css", ct)
	}

	// Test JS
	resp, err = http.Get(ts.URL + "/static/app.js")
	if err != nil {
		t.Fatalf("get js: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("js status = %d, want 200", resp.StatusCode)
	}
	ct = resp.Header.Get("Content-Type")
	if !containsSubstring(ct, "javascript") && !containsSubstring(ct, "text/plain") {
		t.Errorf("js Content-Type = %q, want javascript or text/plain", ct)
	}
}

func TestMetaMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/meta", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// --- helpers ---

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
