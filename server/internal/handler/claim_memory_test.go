package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestClaimSharedMemoryScopeAndFreshSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	project := dbfx.Project(t, "claim memory project")
	otherProject := dbfx.Project(t, "claim other project")
	foreignWS := dbfx.Workspace(t, "claim foreign memory", "claim-foreign-memory")
	global := createMemoryForTest(t, map[string]any{"title": "claim global", "content": "old fact"})
	scoped := createMemoryForTest(t, map[string]any{"title": "claim scoped", "content": "project fact", "project_id": project})
	createMemoryForTest(t, map[string]any{"title": "claim other", "content": "must not leak", "project_id": otherProject})
	dbfx.Insert(t, "memory", testutil.Cols{"workspace_id": foreignWS, "title": "foreign", "content": "foreign must not leak", "created_by_type": "member", "created_by": testUserID})
	agent, runtimeID, _ := createRuntimeGuardAgent(t, ctx)
	issue := dbfx.Issue(t, "claim memory issue", testutil.Cols{"project_id": project})
	taskID := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtimeID})
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	claim := func() AgentTaskResponse {
		t.Helper()
		req := newDaemonTokenRequest("POST", "/claim", nil, testWorkspaceID, "memory-claim")
		resp, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(req, &task, runtime, runtimeID, testWorkspaceID)
		if failure != nil {
			t.Fatalf("claim failed: %+v", failure)
		}
		return resp
	}
	assertMemories := func(resp AgentTaskResponse, wantIDs ...string) {
		t.Helper()
		if len(resp.SharedMemories) != len(wantIDs) || resp.SharedMemoriesTruncated || !resp.SharedMemorySnapshotKnown {
			t.Fatalf("unexpected snapshot %+v truncated=%t", resp.SharedMemories, resp.SharedMemoriesTruncated)
		}
		seen := make(map[string]bool)
		for _, memory := range resp.SharedMemories {
			if memory.WorkspaceID != testWorkspaceID {
				t.Fatal("foreign workspace memory leaked")
			}
			seen[memory.ID] = true
		}
		for _, id := range wantIDs {
			if !seen[id] {
				t.Errorf("missing memory %s", id)
			}
		}
	}
	resp := claim()
	assertMemories(resp, global.ID, scoped.ID)
	if resp.ProjectID != project || resp.MemoryAutoExtract {
		t.Fatal("project hydration or extraction default wrong")
	}
	testutil.Call(t, testHandler.UpdateMemory, memoryRequest(http.MethodPut, global.ID, map[string]any{"content": "corrected fact", "expected_revision": 1})).Want(http.StatusOK)
	resp = claim()
	for _, memory := range resp.SharedMemories {
		if memory.ID == global.ID && (memory.Content != "corrected fact" || memory.Revision != 2) {
			t.Fatal("claim did not refresh edited memory")
		}
	}
	req := memoryRequest(http.MethodDelete, global.ID, nil)
	req.URL.RawQuery = "expected_revision=2"
	testutil.Call(t, testHandler.DeleteMemory, req).Want(http.StatusNoContent)
	assertMemories(claim(), scoped.ID)
	// A task without a project must not see project-only memories.
	task.IssueID = pgtype.UUID{}
	task.Context = json.RawMessage(`{"type":"quick_create","prompt":"create a task","workspace_id":"` + testWorkspaceID + `","requester_id":"` + testUserID + `"}`)
	resp = claim()
	assertMemories(resp)
}

func TestClaimSharedMemoryCapAndAutomaticTaskKinds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	for range 51 {
		dbfx.Insert(t, "memory", testutil.Cols{"workspace_id": testWorkspaceID, "title": "memory", "content": "stable fact", "created_by_type": "member", "created_by": testUserID})
	}
	dbfx.Exec(t, "INSERT INTO workspace_memory_settings (workspace_id, automatic_extraction) VALUES ($1, true)", testWorkspaceID)
	dbfx.Cleanup(t, "DELETE FROM workspace_memory_settings WHERE workspace_id = $1", testWorkspaceID)
	for _, tc := range []struct {
		name string
		task db.AgentTaskQueue
		want bool
	}{
		{"direct", db.AgentTaskQueue{IssueID: parseUUID("00000000-0000-0000-0000-000000000001")}, true},
		{"comment", db.AgentTaskQueue{IssueID: parseUUID("00000000-0000-0000-0000-000000000001"), TriggerCommentID: parseUUID("00000000-0000-0000-0000-000000000002")}, true},
		{"private chat linked issue", db.AgentTaskQueue{IssueID: parseUUID("00000000-0000-0000-0000-000000000001"), ChatSessionID: parseUUID("00000000-0000-0000-0000-000000000002")}, false},
		{"autopilot", db.AgentTaskQueue{AutopilotRunID: parseUUID("00000000-0000-0000-0000-000000000001")}, false},
		{"quick create linked issue", db.AgentTaskQueue{IssueID: parseUUID("00000000-0000-0000-0000-000000000001"), Context: json.RawMessage(`{"type":"quick_create"}`)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := AgentTaskResponse{WorkspaceID: testWorkspaceID}
			testHandler.hydrateClaimSharedMemory(ctx, tc.task, &resp)
			if len(resp.SharedMemories) != 50 || !resp.SharedMemoriesTruncated || resp.MemoryAutoExtract != tc.want {
				t.Fatalf("memory cap/auto gate: count=%d truncated=%t auto=%t", len(resp.SharedMemories), resp.SharedMemoriesTruncated, resp.MemoryAutoExtract)
			}
		})
	}
	// A failed read must never masquerade as an authoritative empty snapshot.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	resp := AgentTaskResponse{WorkspaceID: testWorkspaceID}
	testHandler.hydrateClaimSharedMemory(cancelled, db.AgentTaskQueue{}, &resp)
	if !resp.SharedMemoriesTruncated || resp.SharedMemorySnapshotKnown || resp.MemoryAutoExtract {
		t.Fatal("failed reads must remain incomplete and disable extraction")
	}
}
