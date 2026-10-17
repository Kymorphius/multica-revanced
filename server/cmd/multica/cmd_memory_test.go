package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func executeMemoryTest(t *testing.T, handler http.HandlerFunc, args ...string) (string, error) {
	t.Helper()
	redirectTestHome(t, t.TempDir())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_TOKEN", "memory-test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-memory-test")
	cmd := newMemoryCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestMemoryCLIListKeepsLiteralChineseQueryAndWorkspace(t *testing.T) {
	out, err := executeMemoryTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/memories" || r.URL.Query().Get("q") != "中文 100%_&" || r.URL.Query().Get("project_id") != "project-test" || r.URL.Query().Get("limit") != "20" || r.URL.Query().Get("offset") != "40" {
			t.Errorf("unexpected memory list request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("X-Workspace-ID") != "workspace-memory-test" || r.Header.Get("Authorization") != "Bearer memory-test-token" {
			t.Error("memory list lost workspace or authentication context")
		}
		_, _ = w.Write([]byte(`[{"id":"memory-a","title":"中文偏好","content":"使用中文","revision":2}]`))
	}, "list", "--query", "中文 100%_&", "--project-id", "project-test", "--limit", "20", "--offset", "40")
	if err != nil || !strings.Contains(out, "中文偏好") {
		t.Fatalf("list output = %q, err = %v", out, err)
	}
}

func TestMemoryCLICreateCarriesExplicitScopeAndSource(t *testing.T) {
	out, err := executeMemoryTest(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method != http.MethodPost || r.URL.Path != "/api/memories" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid create request")
		}
		if body["title"] != "测试约定" || body["content"] != "第一行\n第二行" || body["source"] != "automatic" || body["project_id"] != "project-test" {
			t.Errorf("create body = %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"memory-new","revision":1}`))
	}, "create", "--title", "测试约定", "--content", "第一行\n第二行", "--source", "automatic", "--project-id", "project-test")
	if err != nil || !strings.Contains(out, "memory-new") {
		t.Fatalf("create output = %q, err = %v", out, err)
	}
}

func TestMemoryCLIConflictDoesNotClaimSuccessfulSave(t *testing.T) {
	out, err := executeMemoryTest(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPut || r.URL.Path != "/api/memories/memory-a" || body["expected_revision"] != float64(3) || body["content"] != "修正内容" {
			t.Errorf("update = %s %s %#v", r.Method, r.URL.Path, body)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"revision conflict","current_revision":4}`))
	}, "update", "memory-a", "--expected-revision", "3", "--content", "修正内容")
	if err == nil || !strings.Contains(err.Error(), "409") || out != "" {
		t.Fatalf("conflict output = %q, err = %v", out, err)
	}
}

func TestMemoryCLIForgetUsesRevisionAndConfirmation(t *testing.T) {
	out, err := executeMemoryTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/memories/memory-a" || r.URL.Query().Get("expected_revision") != "4" {
			t.Errorf("delete = %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusNoContent)
	}, "delete", "memory-a", "--expected-revision", "4", "--yes")
	if err != nil || !strings.Contains(out, `"deleted": true`) {
		t.Fatalf("delete output = %q, err = %v", out, err)
	}
}

func TestMemoryCLISettingsCanExplicitlyDisableExtraction(t *testing.T) {
	out, err := executeMemoryTest(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPut || r.URL.Path != "/api/memories/settings" || body["automatic_extraction"] != false {
			t.Errorf("settings = %s %s %#v", r.Method, r.URL.Path, body)
		}
		_, _ = w.Write([]byte(`{"automatic_extraction":false}`))
	}, "settings", "update", "--automatic-extraction=false")
	if err != nil || !strings.Contains(out, `"automatic_extraction": false`) {
		t.Fatalf("settings output = %q, err = %v", out, err)
	}
}

func TestMemoryCLIValidatesMutationsBeforeSendingRequests(t *testing.T) {
	for _, args := range [][]string{
		{"delete", "memory-a", "--yes"},
		{"delete", "memory-a", "--expected-revision", "1"},
		{"update", "memory-a", "--expected-revision", "1"},
		{"create", "--title", "缺少内容"},
		{"settings", "update"},
		{"list", "--limit", "101"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := executeMemoryTest(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid command sent a request")
				w.WriteHeader(http.StatusInternalServerError)
			}, args...)
			if err == nil {
				t.Fatal("invalid command should fail before making a request")
			}
		})
	}
}
