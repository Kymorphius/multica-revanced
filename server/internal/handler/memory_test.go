package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func createMemoryForTest(t *testing.T, body map[string]any) MemoryResponse {
	t.Helper()
	var m MemoryResponse
	testutil.Call(t, testHandler.CreateMemory, newRequest("POST", "/api/memories", body)).Want(http.StatusCreated).JSON(&m)
	dbfx.Cleanup(t, "DELETE FROM memory WHERE id = $1", m.ID)
	return m
}
func memoryRequest(method, id string, body any) *http.Request {
	return withURLParam(newRequest(method, "/api/memories/"+id, body), "id", id)
}

func TestMemoryRevisionAndLiteralSearch(t *testing.T) {
	m := createMemoryForTest(t, map[string]any{"title": "  中文偏好 %_  ", "content": "偏好使用 Chinese 说明"})
	if m.Title != "中文偏好 %_" || m.Revision != 1 || m.Source != "manual" || m.CreatedBy != testUserID {
		t.Fatalf("unexpected memory: %+v", m)
	}
	var rows []MemoryResponse
	testutil.Call(t, testHandler.ListMemories, newRequest("GET", "/api/memories?q="+url.QueryEscape("中文偏好 %_"), nil)).Want(200).JSON(&rows)
	if len(rows) != 1 || rows[0].ID != m.ID {
		t.Fatalf("literal Chinese search returned %+v", rows)
	}
	testutil.Call(t, testHandler.ListMemories, newRequest("GET", "/api/memories?q="+url.QueryEscape("not-a-match%"), nil)).Want(200).JSON(&rows)
	if len(rows) != 0 {
		t.Fatalf("SQL wildcard should remain literal: %+v", rows)
	}
	var updated MemoryResponse
	testutil.Call(t, testHandler.UpdateMemory, memoryRequest("PUT", m.ID, map[string]any{"content": "new", "expected_revision": 1})).Want(200).JSON(&updated)
	if updated.Revision != 2 || updated.CreatedBy != m.CreatedBy || updated.Source != m.Source {
		t.Fatalf("update changed immutable fields: %+v", updated)
	}
	testutil.Call(t, testHandler.UpdateMemory, memoryRequest("PUT", m.ID, map[string]any{"title": "stale", "expected_revision": 1})).Want(409)
	testutil.Call(t, testHandler.UpdateMemory, memoryRequest("PUT", m.ID, map[string]any{"title": "missing revision"})).Want(400)
	req := memoryRequest("DELETE", m.ID, nil)
	req.URL.RawQuery = "expected_revision=1"
	testutil.Call(t, testHandler.DeleteMemory, req).Want(409)
	req = memoryRequest("DELETE", m.ID, nil)
	req.URL.RawQuery = "expected_revision=2"
	testutil.Call(t, testHandler.DeleteMemory, req).Want(204)
	testutil.Call(t, testHandler.GetMemory, memoryRequest("GET", m.ID, nil)).Want(404)
}
func TestMemoryWorkspaceProjectIsolation(t *testing.T) {
	project := dbfx.Project(t, "memory project")
	foreignWS := dbfx.Workspace(t, "memory foreign", "memory-foreign")
	foreignProject := dbfx.Project(t, "foreign memory project", testutil.Cols{"workspace_id": foreignWS})
	dbfx.Member(t, foreignWS, testUserID, "owner")
	global := createMemoryForTest(t, map[string]any{"title": "global", "content": "workspace"})
	scoped := createMemoryForTest(t, map[string]any{"title": "project", "content": "project fact", "project_id": project})
	testutil.Call(t, testHandler.CreateMemory, newRequest("POST", "/api/memories", map[string]any{"title": "wrong project", "content": "x", "project_id": foreignProject})).Want(404)
	testutil.Call(t, testHandler.CreateMemory, newRequest("POST", "/api/memories", map[string]any{"title": "bad project", "content": "x", "project_id": "bad"})).Want(400)
	req := memoryRequest("GET", global.ID, nil)
	req.Header.Set("X-Workspace-ID", foreignWS)
	testutil.Call(t, testHandler.GetMemory, req).Want(404)
	req = memoryRequest("PUT", global.ID, map[string]any{"title": "cross workspace", "expected_revision": 1})
	req.Header.Set("X-Workspace-ID", foreignWS)
	testutil.Call(t, testHandler.UpdateMemory, req).Want(404)
	req = memoryRequest("DELETE", global.ID, nil)
	req.Header.Set("X-Workspace-ID", foreignWS)
	req.URL.RawQuery = "expected_revision=1"
	testutil.Call(t, testHandler.DeleteMemory, req).Want(404)
	var rows []MemoryResponse
	testutil.Call(t, testHandler.ListMemories, newRequest("GET", "/api/memories?project_id="+project, nil)).Want(200).JSON(&rows)
	if len(rows) != 1 || rows[0].ID != scoped.ID {
		t.Fatalf("project list leaked: %+v", rows)
	}
	testutil.Call(t, testHandler.ListMemories, newRequest("GET", "/api/memories?project_id="+foreignProject, nil)).Want(404)
	testutil.Call(t, testHandler.DeleteProject, withURLParam(newRequest("DELETE", "/api/projects/"+project, nil), "id", project)).Want(204)
	testutil.Call(t, testHandler.GetMemory, memoryRequest("GET", scoped.ID, nil)).Want(404)
	testutil.Call(t, testHandler.GetMemory, memoryRequest("GET", global.ID, nil)).Want(200)
}
func TestMemoryPermissionsValidationAndSettings(t *testing.T) {
	outsider := dbfx.User(t, "Memory Outsider", "memory-outsider@example.test")
	member := dbfx.User(t, "Memory Member", "memory-member@example.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	testutil.Call(t, testHandler.ListMemories, newRequestAs(outsider, "GET", "/api/memories", nil)).Want(404)
	testutil.Call(t, testHandler.CreateMemory, newRequestAs(outsider, "POST", "/api/memories", map[string]any{"title": "no", "content": "no"})).Want(404)
	for _, body := range []map[string]any{{"title": " ", "content": "x"}, {"title": strings.Repeat("字", 161), "content": "x"}, {"title": "x", "content": strings.Repeat("字", 4001)}, {"title": "x", "content": "x", "source": "bad"}} {
		testutil.Call(t, testHandler.CreateMemory, newRequest("POST", "/api/memories", body)).Want(400)
	}
	for _, q := range []string{"limit=0", "limit=101", "offset=-1"} {
		testutil.Call(t, testHandler.ListMemories, newRequest("GET", "/api/memories?"+q, nil)).Want(400)
	}
	dbfx.Cleanup(t, "DELETE FROM workspace_memory_settings WHERE workspace_id = $1", testWorkspaceID)
	dbfx.Exec(t, "DELETE FROM workspace_memory_settings WHERE workspace_id = $1", testWorkspaceID)
	var settings MemorySettingsResponse
	testutil.Call(t, testHandler.GetMemorySettings, newRequest("GET", "/api/memories/settings", nil)).Want(200).JSON(&settings)
	if settings.AutomaticExtraction {
		t.Fatal("extraction must default off")
	}
	testutil.Call(t, testHandler.UpdateMemorySettings, newRequestAs(member, "PUT", "/api/memories/settings", map[string]any{"automatic_extraction": true})).Want(403)
	testutil.Call(t, testHandler.UpdateMemorySettings, newRequest("PUT", "/api/memories/settings", map[string]any{"automatic_extraction": true})).Want(200)
	testutil.Call(t, testHandler.GetMemorySettings, newRequest("GET", "/api/memories/settings", nil)).Want(200).JSON(&settings)
	if !settings.AutomaticExtraction {
		t.Fatal("setting did not persist")
	}
	req := newRequest("POST", "/api/memories", map[string]any{"title": "spoof", "content": "x"})
	req.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	req.Header.Set("X-Task-ID", "00000000-0000-0000-0000-000000000002")
	var m MemoryResponse
	testutil.Call(t, testHandler.CreateMemory, req).Want(201).JSON(&m)
	dbfx.Cleanup(t, "DELETE FROM memory WHERE id = $1", m.ID)
	if m.CreatedByType != "member" || m.CreatedBy != testUserID {
		t.Fatalf("raw headers spoofed author: %+v", m)
	}
	req = newRequest("POST", "/api/memories", map[string]any{"title": "cloud", "content": "x"})
	req.Header.Set("X-Actor-Source", "cloud_pat")
	testutil.Call(t, testHandler.CreateMemory, req).Want(403)
	testutil.Call(t, testHandler.CreateMemory, newRequest("POST", "/api/memories", map[string]any{"title": "auto human", "content": "x", "source": "automatic"})).Want(403)
}

// Exercise the real credential middleware so actor/task/workspace headers cannot
// accidentally become trusted client inputs on the publication path.
func TestMemoryTaskTokenAutomaticGateAndManualChat(t *testing.T) {
	project := dbfx.Project(t, "task memory project")
	otherProject := dbfx.Project(t, "other task memory project")
	agent := dbfx.Agent(t, "memory writer", testRuntimeID)
	issue := dbfx.Issue(t, "memory issue", testutil.Cols{"project_id": project})
	issueTask := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": testRuntimeID})
	chat := dbfx.ChatSession(t, agent, testutil.Cols{"project_id": project})
	linkedChatIssue := dbfx.Issue(t, "private chat linked issue", testutil.Cols{"project_id": project})
	chatTask := dbfx.Task(t, agent, testutil.Cols{"chat_session_id": chat, "issue_id": linkedChatIssue, "runtime_id": testRuntimeID})
	tokenFor := func(task string) string {
		token := "mat_memory_test_" + task
		dbfx.Insert(t, "task_token", testutil.Cols{"token_hash": auth.HashToken(token), "task_id": task, "agent_id": agent, "workspace_id": testWorkspaceID, "user_id": testUserID, "expires_at": testutil.Raw("now() + interval '1 hour'")})
		return token
	}
	issueToken, chatToken := tokenFor(issueTask), tokenFor(chatTask)
	call := func(token string, body any, want int) MemoryResponse {
		req := newRequest("POST", "/api/memories", body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Agent-ID", "spoof")
		req.Header.Set("X-Task-ID", "spoof")
		wrapped := middleware.Auth(testHandler.Queries, nil, nil, nil)(http.HandlerFunc(testHandler.CreateMemory))
		var m MemoryResponse
		resp := testutil.Call(t, wrapped.ServeHTTP, req).Want(want)
		if want == 201 {
			resp.JSON(&m)
			dbfx.Cleanup(t, "DELETE FROM memory WHERE id = $1", m.ID)
		}
		return m
	}
	dbfx.Exec(t, "DELETE FROM workspace_memory_settings WHERE workspace_id = $1", testWorkspaceID)
	dbfx.Cleanup(t, "DELETE FROM workspace_memory_settings WHERE workspace_id = $1", testWorkspaceID)
	auto := map[string]any{"title": "auto issue", "content": "public lesson", "source": "automatic", "project_id": project}
	call(issueToken, auto, 403)
	testutil.Call(t, testHandler.UpdateMemorySettings, newRequest("PUT", "/api/memories/settings", map[string]any{"automatic_extraction": true})).Want(200)
	m := call(issueToken, auto, 201)
	if m.CreatedByType != "agent" || m.CreatedBy != agent || m.Source != "automatic" {
		t.Fatalf("wrong task attribution: %+v", m)
	}
	call(chatToken, auto, 403)
	call(chatToken, map[string]any{"title": "explicit chat save", "content": "user explicitly shared this", "project_id": project}, 201)
	call(issueToken, map[string]any{"title": "wrong project", "content": "x", "project_id": otherProject}, 403)
	call(chatToken, map[string]any{"title": "wrong chat project", "content": "x", "project_id": otherProject}, 403)
}

func TestMemoryWorkspaceLeafCleanup(t *testing.T) {
	ws := dbfx.Workspace(t, "memory cleanup", "memory-cleanup")
	memory := dbfx.Insert(t, "memory", testutil.Cols{"workspace_id": ws, "title": "cleanup", "content": "cleanup", "created_by_type": "member", "created_by": testUserID})
	dbfx.InsertNoID(t, "workspace_memory_settings", testutil.Cols{"workspace_id": ws, "automatic_extraction": true}, "workspace_id = $1", ws)
	if err := testHandler.Queries.DeleteWorkspaceLeafData(t.Context(), parseUUID(ws)); err != nil {
		t.Fatalf("workspace leaf cleanup: %v", err)
	}
	if got := dbfx.Count(t, "SELECT count(*) FROM memory WHERE id = $1", memory); got != 0 {
		t.Fatalf("orphan memory rows=%d", got)
	}
	if got := dbfx.Count(t, "SELECT count(*) FROM workspace_memory_settings WHERE workspace_id = $1", ws); got != 0 {
		t.Fatalf("orphan settings rows=%d", got)
	}
}

func TestSharedMemoryRoleGate(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member", "viewer", "unknown"} {
		if got, want := memoryRoleAllowed(role, false), role != "unknown"; got != want {
			t.Errorf("read role %s = %v, want %v", role, got, want)
		}
		if got, want := memoryRoleAllowed(role, true), role != "viewer" && role != "unknown"; got != want {
			t.Errorf("write role %s = %v, want %v", role, got, want)
		}
	}
}

// Pause workspace deletion after its parent-row fence and leaf sweep. A late
// publisher must wait at that same row, then observe that deletion won instead
// of inserting a no-FK orphan behind the already-completed sweep.
func TestMemorySavesWaitForWorkspaceDeletion(t *testing.T) {
	for _, kind := range []string{"memory", "settings"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ws := dbfx.Workspace(t, "memory delete race "+kind, "memory-delete-race-"+kind)
			dbfx.Member(t, ws, testUserID, "owner")
			dbfx.Cleanup(t, "DELETE FROM memory WHERE workspace_id = $1", ws)
			dbfx.Cleanup(t, "DELETE FROM workspace_memory_settings WHERE workspace_id = $1", ws)
			writerConn, err := testPool.Acquire(ctx)
			if err != nil {
				t.Fatalf("acquire memory writer: %v", err)
			}
			defer writerConn.Release()
			holder, err := testPool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin workspace delete: %v", err)
			}
			defer holder.Rollback(context.Background())
			qtx := testHandler.Queries.WithTx(holder)
			if _, err := qtx.LockWorkspaceForDelete(ctx, parseUUID(ws)); err != nil {
				t.Fatalf("lock workspace: %v", err)
			}
			if err := qtx.DeleteWorkspaceLeafData(ctx, parseUUID(ws)); err != nil {
				t.Fatalf("sweep workspace leaf data: %v", err)
			}

			h := *testHandler
			h.TxStarter = writerConn
			req := newRequest("POST", "/api/memories", map[string]any{"title": "late save", "content": "must not survive deleted workspace"})
			handle := h.CreateMemory
			if kind == "settings" {
				req = newRequest("PUT", "/api/memories/settings", map[string]any{"automatic_extraction": true})
				handle = h.UpdateMemorySettings
			}
			req.Header.Set("X-Workspace-ID", ws)
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); handle(rec, req) }()
			waitForBlockedWriter(t, ctx, int32(writerConn.Conn().PgConn().PID()))
			select {
			case <-done:
				t.Fatalf("save returned %d while workspace deletion held its lock: %s", rec.Code, rec.Body.String())
			default:
			}
			if err := qtx.DeleteWorkspace(ctx, parseUUID(ws)); err != nil {
				t.Fatalf("delete workspace: %v", err)
			}
			if err := holder.Commit(ctx); err != nil {
				t.Fatalf("commit workspace deletion: %v", err)
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("late save did not settle after deletion")
			}
			if rec.Code != http.StatusNotFound {
				t.Fatalf("late %s save status=%d, want 404: %s", kind, rec.Code, rec.Body.String())
			}
			for _, table := range []string{"memory", "workspace_memory_settings"} {
				if n := dbfx.Count(t, "SELECT count(*) FROM "+table+" WHERE workspace_id = $1", ws); n != 0 {
					t.Fatalf("%s retained %d orphan rows", table, n)
				}
			}
			testutil.Call(t, handle, req).Want(http.StatusNotFound)
		})
	}
}
