//go:build integration

// Copyright (c) 2025 GoatFlow Team
// SPDX-License-Identifier: MIT

package kb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------------------
// Helper types – mirror the shapes we get from the GoatFlow API
// ------------------------------------------------------------

type pluginListResp struct {
	Plugins []pluginInfo `json:"plugins"`
}

type pluginInfo struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Loaded  bool        `json:"loaded"`
	Enabled bool        `json:"enabled"`
	Routes  []routeSpec `json:"routes"`
}

type routeSpec struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Handler     string   `json:"handler"`
	Middleware  []string `json:"middleware"`
	Description string   `json:"description"`
}

type loginResp struct {
	AccessToken string `json:"access_token"`
	Token       string `json:"token"`
}

type uploadResp struct {
	Message string `json:"message"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Runtime string `json:"runtime"`
}

// ------------------------------------------------------------
// Test configuration – read from env (fallback to sane defaults)
// ------------------------------------------------------------

func testEnv(t *testing.T, key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func baseURL(t *testing.T) string {
	return testEnv(t, "GOATFLOW_URL", "http://localhost:8080")
}

func adminUser(t *testing.T) string {
	return testEnv(t, "GOATFLOW_ADMIN_USER", "root@localhost")
}

func adminPass(t *testing.T) string {
	return testEnv(t, "GOATFLOW_ADMIN_PASS", "5hipsC@t")
}

func adminAPIKey(t *testing.T) string {
	return testEnv(t, "ADMIN_API_KEY", "")
}

// getAuthToken returns a bearer token: the ADMIN_API_KEY if set, otherwise
// it logs in and extracts the access token from the response.
func getAuthToken(t *testing.T) string {
	// Try the ADMIN_API_KEY first — if the host accepts it, use it.
	if key := adminAPIKey(t); key != "" {
		resp, data := doRequest(t, "GET", "/api/v1/plugins", nil,
			http.Header{"Authorization": {"Bearer " + key}})
		if resp.StatusCode == http.StatusOK {
			return key
		}
		t.Logf("ADMIN_API_KEY rejected (%d %s), falling back to login", resp.StatusCode, string(data))
	}
	// Fall back to a JWT via /api/auth/login.
	payload := map[string]string{
		"login":    adminUser(t),
		"password": adminPass(t),
	}
	b, _ := json.Marshal(payload)
	resp, data := doRequest(t, "POST", "/api/auth/login", bytes.NewReader(b),
		http.Header{"Content-Type": {"application/json"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d %s", resp.StatusCode, string(data))
	}
	var lr loginResp
	if err := json.Unmarshal(data, &lr); err != nil {
		t.Fatalf("cannot unmarshal login response: %v", err)
	}
	if lr.AccessToken != "" {
		return lr.AccessToken
	}
	return lr.Token
}

// authHeader returns an http.Header carrying a Bearer token for the test.
func authHeader(t *testing.T) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+getAuthToken(t))
	return h
}

// ------------------------------------------------------------
// HTTP helpers
// ------------------------------------------------------------

func doRequest(t *testing.T, method, path string, body io.Reader, header http.Header) (*http.Response, []byte) {
	t.Helper()
	u := baseURL(t) + path
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	if header != nil {
		req.Header = header
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return resp, data
}

func doRequestCustom(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return resp, data
}

// ------------------------------------------------------------
// Plugin upload helper
// ------------------------------------------------------------

func uploadPlugin(t *testing.T) string {
	t.Helper()
	// Path to the built zip – assume `make package` has been run.
	zipPath := filepath.Join("..", "..", "bin", "kb.zip")
	if _, err := os.Stat(zipPath); err != nil {
		absPath, err := filepath.Abs(zipPath)
		if err != nil {
			t.Fatalf("failed to get abs path: %v", err)
		}
		dir, _ := os.Getwd()
		t.Logf("Looking for plugin zip at %s (abs: %s), current dir: %s", zipPath, absPath, dir)
		t.Fatalf("plugin zip not found at %s – run `make package` first", zipPath)
	}
	f, err := os.Open(zipPath)
	if err != nil {
		t.Fatalf("cannot open plugin zip: %v", err)
	}
	defer f.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("plugin", filepath.Base(zipPath))
	if err != nil {
		t.Fatalf("cannot create form file: %v", err)
	}
	if _, err = io.Copy(part, f); err != nil {
		t.Fatalf("cannot copy zip into request: %v", err)
	}
	w.Close()

	u := baseURL(t) + "/api/v1/plugins/upload"
	req, err := http.NewRequest("POST", u, body)
	if err != nil {
		t.Fatalf("cannot build upload request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+getAuthToken(t))

	resp, data := doRequestCustom(t, req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plugin upload failed: %d %s", resp.StatusCode, string(data))
	}
	var ur uploadResp
	if err := json.Unmarshal(data, &ur); err != nil {
		t.Fatalf("cannot unmarshal upload response: %v", err)
	}
	return ur.Name
}

// ------------------------------------------------------------
// The actual test
// ------------------------------------------------------------

func TestKBPluginIntegration(t *testing.T) {
	// 1️⃣  Upload the plugin (assumes `make package` already produced bin/kb.zip)
	pluginName := uploadPlugin(t)
	if pluginName != "kb" {
		t.Fatalf("expected plugin name 'kb', got %q", pluginName)
	}

	// 2️⃣  Wait a moment for the host to load the plugin (usually <1s)
	time.Sleep(500 * time.Millisecond)

	// 3️⃣  Verify the plugin appears in the plugin list with correct routes
	resp, data := doRequest(t, "GET", "/api/v1/plugins", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/v1/plugins failed: %d %s", resp.StatusCode, string(data))
	}
	var pl pluginListResp
	if err := json.Unmarshal(data, &pl); err != nil {
		t.Fatalf("cannot unmarshal plugin list: %v", err)
	}
	var kbPlugin *pluginInfo
	for i := range pl.Plugins {
		if pl.Plugins[i].Name == "kb" {
			kbPlugin = &pl.Plugins[i]
			break
		}
	}
	if kbPlugin == nil {
		t.Fatalf("kb plugin not found in plugin list")
	}
	if !kbPlugin.Loaded {
		t.Fatalf("kb plugin loaded flag is false")
	}
	if !kbPlugin.Enabled {
		t.Fatalf("kb plugin enabled flag is false")
	}

	expectedRoutes := []string{
		"GET /knowledge-base",
		"GET /kb/search",
		"GET /kb/article/:id",
		"POST /admin/kb/import",
		"GET /admin/kb",
		"GET /admin/kb/article/:id",
		"POST /admin/kb/article",
		"DELETE /admin/kb/article/:id",
	}
	foundRoutes := make(map[string]bool, len(kbPlugin.Routes))
	for _, r := range kbPlugin.Routes {
		key := fmt.Sprintf("%s %s", r.Method, r.Path)
		foundRoutes[key] = true
	}
	for _, exp := range expectedRoutes {
		if !foundRoutes[exp] {
			t.Fatalf("missing expected route: %s", exp)
		}
	}

	// 4a️⃣  Verify admin UI pages return HTML (not JSON)
	resp, data = doRequest(t, "GET", "/admin/kb", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/kb GET failed: %d %s", resp.StatusCode, string(data))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("/admin/kb should return HTML, got Content-Type: %s, body: %s", ct, string(data[:min(200, len(data))]))
	}
	if !strings.Contains(string(data), "Knowledge Base") {
		t.Fatalf("/admin/kb HTML should contain 'Knowledge Base', got: %s", string(data[:min(200, len(data))]))
	}

	resp, data = doRequest(t, "GET", "/admin/kb/article/new", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/kb/article/new GET failed: %d %s", resp.StatusCode, string(data))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("/admin/kb/article/new should return HTML, got Content-Type: %s", ct)
	}
	if !strings.Contains(string(data), "New KB Article") {
		t.Fatalf("/admin/kb/article/new HTML should contain 'New KB Article'")
	}

	// 4️⃣  Clean up any articles from previous test runs
	for _, id := range []int{1, 2, 3} {
		doRequest(t, "DELETE", fmt.Sprintf("/admin/kb/article/%d", id), nil, authHeader(t))
	}

	resp, data = doRequest(t, "GET", "/knowledge-base", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/knowledge-base failed: %d %s", resp.StatusCode, string(data))
	}
	var listResp struct {
		Articles []any `json:"articles"`
		Total    int   `json:"total"`
		Page     int   `json:"page"`
		PerPage  int   `json:"per_page"`
	}
	if err := json.Unmarshal(data, &listResp); err != nil {
		t.Fatalf("cannot unmarshal knowledge-base response: %v", err)
	}
	if listResp.Total != 0 {
		t.Fatalf("expected total 0, got %d", listResp.Total)
	}

	resp, data = doRequest(t, "GET", "/kb/search?query=test", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/kb/search failed: %d %s", resp.StatusCode, string(data))
	}
	var searchResp struct {
		Results []any  `json:"results"`
		Total   int    `json:"total"`
		Query   string `json:"query"`
		Page    int    `json:"page"`
		PerPage int    `json:"per_page"`
	}
	if err := json.Unmarshal(data, &searchResp); err != nil {
		t.Fatalf("cannot unmarshal search response: %v", err)
	}
	if searchResp.Total != 0 {
		t.Fatalf("expected search total 0, got %d", searchResp.Total)
	}

	// 5️⃣  Exercise the admin CRUD endpoints
	//    Create an article
	createPayload := map[string]interface{}{
		"id":         1,
		"title":      "Test Article",
		"content":    "This is a test.",
		"category":   "General",
		"visibility": "public",
		"status":     "published",
		"author":     "tester",
	}
	createB, _ := json.Marshal(createPayload)
	h := authHeader(t)
	h.Set("Content-Type", "application/json")
	resp, data = doRequest(t, "POST", "/admin/kb/article", bytes.NewReader(createB), h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/kb/article POST failed: %d %s", resp.StatusCode, string(data))
	}
	var updateResp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &updateResp); err != nil {
		t.Fatalf("cannot unmarshal create response: %v", err)
	}
	if updateResp.Status != "created" && updateResp.Status != "updated" {
		t.Fatalf("unexpected create status: %s", updateResp.Status)
	}

	//    Read the article back via the public endpoint
	resp, data = doRequest(t, "GET", "/kb/article/1", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/kb/article/1 GET failed: %d %s", resp.StatusCode, string(data))
	}
	var articleResp struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Content    string `json:"content"`
		Category   string `json:"category"`
		Visibility string `json:"visibility"`
		Author     string `json:"author"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &articleResp); err != nil {
		t.Fatalf("cannot unmarshal article response: %v", err)
	}
	if articleResp.Title != "Test Article" {
		t.Fatalf("unexpected article title: %s", articleResp.Title)
	}

	//    Update the article
	updatePayload := map[string]interface{}{
		"id":    1,
		"title": "Updated Title",
	}
	updateB, _ := json.Marshal(updatePayload)
	h2 := authHeader(t)
	h2.Set("Content-Type", "application/json")
	resp, data = doRequest(t, "POST", "/admin/kb/article", bytes.NewReader(updateB), h2)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/kb/article UPDATE failed: %d %s", resp.StatusCode, string(data))
	}
	if err := json.Unmarshal(data, &updateResp); err != nil {
		t.Fatalf("cannot unmarshal update response: %v", err)
	}
	if updateResp.Status != "updated" {
		t.Fatalf("unexpected update status: %s", updateResp.Status)
	}

	//    Delete the article
	resp, data = doRequest(t, "DELETE", "/admin/kb/article/1", nil, authHeader(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/kb/article/1 DELETE failed: %d %s", resp.StatusCode, string(data))
	}
	if err := json.Unmarshal(data, &updateResp); err != nil {
		t.Fatalf("cannot unmarshal delete response: %v", err)
	}
	if updateResp.Status != "deleted" {
		t.Fatalf("unexpected delete status: %s", updateResp.Status)
	}

	t.Logf("✅ KB plugin integration test passed")
}