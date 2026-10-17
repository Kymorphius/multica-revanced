package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MemoryResponse is shared by the management API and task context hydration.
type MemoryResponse struct {
	ID            string  `json:"id"`
	WorkspaceID   string  `json:"workspace_id"`
	ProjectID     *string `json:"project_id"`
	Title         string  `json:"title"`
	Content       string  `json:"content"`
	Source        string  `json:"source"`
	Revision      int32   `json:"revision"`
	CreatedByType string  `json:"created_by_type"`
	CreatedBy     string  `json:"created_by"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func memoryToResponse(m db.Memory) MemoryResponse {
	return MemoryResponse{ID: uuidToString(m.ID), WorkspaceID: uuidToString(m.WorkspaceID), ProjectID: uuidToPtr(m.ProjectID), Title: m.Title, Content: m.Content, Source: m.Source, Revision: m.Revision, CreatedByType: m.CreatedByType, CreatedBy: uuidToString(m.CreatedBy), CreatedAt: timestampToString(m.CreatedAt), UpdatedAt: timestampToString(m.UpdatedAt)}
}

func (h *Handler) memoryWorkspace(w http.ResponseWriter, r *http.Request, write bool) (pgtype.UUID, bool) {
	ws, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return ws, false
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(ws), "workspace not found")
	if ok && !memoryRoleAllowed(member.Role, write) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return ws, false
	}
	if ok && write && r.Header.Get("X-Actor-Source") == "cloud_pat" {
		writeError(w, http.StatusForbidden, "cloud credentials cannot modify shared memories")
		return ws, false
	}
	return ws, ok
}

func memoryRoleAllowed(role string, write bool) bool {
	if write {
		return roleAllowed(role, "owner", "admin", "member")
	}
	return roleAllowed(role, "owner", "admin", "member", "viewer")
}

// Task credentials are authoritative; private chats may publish only explicit manual saves.
func (h *Handler) authorizeMemoryWrite(w http.ResponseWriter, r *http.Request, ws, project pgtype.UUID, automatic bool) bool {
	if r.Header.Get("X-Actor-Source") != "task_token" {
		if automatic {
			writeError(w, http.StatusForbidden, "automatic memories require an issue task credential")
			return false
		}
		return true
	}
	taskID, ok := parseUUIDOrBadRequest(w, r.Header.Get("X-Task-ID"), "task id")
	if !ok {
		return false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || uuidToString(task.AgentID) != r.Header.Get("X-Agent-ID") {
		writeError(w, http.StatusForbidden, "invalid memory task credential")
		return false
	}
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: task.AgentID, WorkspaceID: ws}); err != nil {
		writeError(w, http.StatusForbidden, "task agent is outside this workspace")
		return false
	}
	if automatic && (!task.IssueID.Valid || task.ChatSessionID.Valid || task.AutopilotRunID.Valid || computeTaskKind(task) == "quick_create") {
		writeError(w, http.StatusForbidden, "automatic memories require a public issue task")
		return false
	}
	var taskProject pgtype.UUID
	if task.ChatSessionID.Valid {
		chat, err := h.Queries.GetChatSessionInWorkspace(r.Context(), db.GetChatSessionInWorkspaceParams{ID: task.ChatSessionID, WorkspaceID: ws})
		if err != nil {
			writeError(w, http.StatusForbidden, "task chat context not found")
			return false
		}
		taskProject = chat.ProjectID
	} else if task.IssueID.Valid {
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: task.IssueID, WorkspaceID: ws})
		if err != nil {
			writeError(w, http.StatusForbidden, "task issue context not found")
			return false
		}
		taskProject = issue.ProjectID
	}
	if project.Valid && taskProject != project {
		writeError(w, http.StatusForbidden, "task cannot write this memory scope")
		return false
	}

	if automatic {
		enabled, err := h.Queries.GetMemoryAutomaticExtraction(r.Context(), ws)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load memory settings")
			return false
		}
		if !enabled {
			writeError(w, http.StatusForbidden, "automatic memory extraction is disabled")
			return false
		}
	}
	return true
}
func validMemoryText(w http.ResponseWriter, title, content *string) bool {
	for _, v := range []struct {
		name string
		p    *string
		max  int
	}{{"title", title, 160}, {"content", content, 4000}} {
		if v.p == nil {
			continue
		}
		*v.p = strings.TrimSpace(*v.p)
		if !utf8.ValidString(*v.p) || utf8.RuneCountInString(*v.p) < 1 || utf8.RuneCountInString(*v.p) > v.max {
			writeError(w, http.StatusBadRequest, v.name+" must be nonempty and within its character limit")
			return false
		}
	}
	return true
}
func decodeMemoryBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		writeError(w, http.StatusBadRequest, "invalid memory request")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid memory request")
		return false
	}
	return true
}
func (h *Handler) memoryProject(w http.ResponseWriter, r *http.Request, ws pgtype.UUID, raw *string) (pgtype.UUID, bool) {
	if raw == nil {
		return pgtype.UUID{}, true
	}
	project, ok := parseUUIDOrBadRequest(w, *raw, "project id")
	if !ok {
		return project, false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: project, WorkspaceID: ws}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return project, false
	}
	return project, true
}
func memoryPagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit, offset := 100, 0
	for _, v := range []struct {
		name string
		dst  *int
	}{{"limit", &limit}, {"offset", &offset}} {
		if raw := r.URL.Query().Get(v.name); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 0 || n > 2147483647 {
				writeError(w, http.StatusBadRequest, "invalid pagination")
				return 0, 0, false
			}
			*v.dst = n
		}
	}
	if limit < 1 || limit > 100 {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
		return 0, 0, false
	}
	return int32(limit), int32(offset), true
}
func (h *Handler) ListMemories(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, false)
	if !ok {
		return
	}
	limit, offset, ok := memoryPagination(w, r)
	if !ok {
		return
	}
	var raw *string
	if v := r.URL.Query().Get("project_id"); v != "" {
		raw = &v
	}
	project, ok := h.memoryProject(w, r, ws, raw)
	if !ok {
		return
	}
	rows, err := h.Queries.ListMemories(r.Context(), db.ListMemoriesParams{WorkspaceID: ws, ProjectID: project, Search: strings.TrimSpace(r.URL.Query().Get("q")), PageLimit: limit, PageOffset: offset})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memories")
		return
	}
	out := make([]MemoryResponse, 0, len(rows))
	for _, m := range rows {
		out = append(out, memoryToResponse(m))
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *Handler) loadMemory(w http.ResponseWriter, r *http.Request, ws pgtype.UUID) (db.Memory, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "memory id")
	if !ok {
		return db.Memory{}, false
	}
	m, err := h.Queries.GetMemoryInWorkspace(r.Context(), db.GetMemoryInWorkspaceParams{ID: id, WorkspaceID: ws})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "memory not found")
		return m, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory")
		return m, false
	}
	return m, true
}
func (h *Handler) GetMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, false)
	if !ok {
		return
	}
	m, ok := h.loadMemory(w, r, ws)
	if ok {
		writeJSON(w, http.StatusOK, memoryToResponse(m))
	}
}
func (h *Handler) CreateMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ProjectID *string `json:"project_id"`
		Title     string  `json:"title"`
		Content   string  `json:"content"`
		Source    string  `json:"source"`
	}
	if !decodeMemoryBody(w, r, &body) || !validMemoryText(w, &body.Title, &body.Content) {
		return
	}
	if body.Source == "" {
		body.Source = "manual"
	}
	if body.Source != "manual" && body.Source != "automatic" {
		writeError(w, http.StatusBadRequest, "invalid memory source")
		return
	}
	project, ok := h.memoryProject(w, r, ws, body.ProjectID)
	if !ok {
		return
	}
	if !h.authorizeMemoryWrite(w, r, ws, project, body.Source == "automatic") {
		return
	}
	// Lock the workspace before the project, matching workspace deletion's lock order.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if !lockMemoryWorkspace(w, r, qtx, ws) {
		return
	}
	if project.Valid {
		if _, err := qtx.LockProjectForDelete(r.Context(), db.LockProjectForDeleteParams{ID: project, WorkspaceID: ws}); err != nil {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
	}
	actorType, actorID := "member", requestUserID(r)
	if r.Header.Get("X-Actor-Source") == "task_token" {
		actorType, actorID = h.resolveActor(r, actorID, uuidToString(ws))
	}
	m, err := qtx.CreateMemory(r.Context(), db.CreateMemoryParams{WorkspaceID: ws, ProjectID: project, Title: body.Title, Content: body.Content, Source: body.Source, CreatedByType: actorType, CreatedBy: parseUUID(actorID)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create memory")
		return
	}
	if tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "failed to save memory")
		return
	}
	writeJSON(w, http.StatusCreated, memoryToResponse(m))
}
func (h *Handler) UpdateMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, true)
	if !ok {
		return
	}
	m, ok := h.loadMemory(w, r, ws)
	if !ok {
		return
	}
	var body struct {
		Title            *string `json:"title"`
		Content          *string `json:"content"`
		ExpectedRevision int32   `json:"expected_revision"`
	}
	if !decodeMemoryBody(w, r, &body) || !validMemoryText(w, body.Title, body.Content) {
		return
	}
	if body.ExpectedRevision < 1 || (body.Title == nil && body.Content == nil) {
		writeError(w, http.StatusBadRequest, "expected_revision and a changed field are required")
		return
	}
	if !h.authorizeMemoryWrite(w, r, ws, m.ProjectID, false) {
		return
	}
	title, content := pgtype.Text{}, pgtype.Text{}
	if body.Title != nil {
		title = pgtype.Text{String: *body.Title, Valid: true}
	}
	if body.Content != nil {
		content = pgtype.Text{String: *body.Content, Valid: true}
	}
	updated, err := h.Queries.UpdateMemory(r.Context(), db.UpdateMemoryParams{ID: m.ID, WorkspaceID: ws, ExpectedRevision: body.ExpectedRevision, Title: title, Content: content})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "memory changed; reload before saving")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update memory")
		return
	}
	writeJSON(w, http.StatusOK, memoryToResponse(updated))
}
func (h *Handler) DeleteMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, true)
	if !ok {
		return
	}
	m, ok := h.loadMemory(w, r, ws)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 32)
	if err != nil || revision < 1 {
		writeError(w, http.StatusBadRequest, "expected_revision is required")
		return
	}
	if !h.authorizeMemoryWrite(w, r, ws, m.ProjectID, false) {
		return
	}
	count, err := h.Queries.DeleteMemory(r.Context(), db.DeleteMemoryParams{ID: m.ID, WorkspaceID: ws, Revision: int32(revision)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete memory")
		return
	}
	if count == 0 {
		writeError(w, http.StatusConflict, "memory changed; reload before deleting")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type MemorySettingsResponse struct {
	AutomaticExtraction bool `json:"automatic_extraction"`
}

func (h *Handler) GetMemorySettings(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, false)
	if !ok {
		return
	}
	enabled, err := h.Queries.GetMemoryAutomaticExtraction(r.Context(), ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load memory settings")
		return
	}
	writeJSON(w, http.StatusOK, MemorySettingsResponse{enabled})
}
func (h *Handler) UpdateMemorySettings(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.memoryWorkspace(w, r, true)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "only humans can configure shared memory")
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(ws), "workspace not found", "owner", "admin"); !ok {
		return
	}
	var body struct {
		AutomaticExtraction *bool `json:"automatic_extraction"`
	}
	if !decodeMemoryBody(w, r, &body) {
		return
	}
	if body.AutomaticExtraction == nil {
		writeError(w, http.StatusBadRequest, "automatic_extraction is required")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if !lockMemoryWorkspace(w, r, qtx, ws) {
		return
	}
	if err := qtx.SetMemoryAutomaticExtraction(r.Context(), db.SetMemoryAutomaticExtractionParams{WorkspaceID: ws, AutomaticExtraction: *body.AutomaticExtraction}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save memory settings")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save memory settings")
		return
	}
	writeJSON(w, http.StatusOK, MemorySettingsResponse{*body.AutomaticExtraction})
}

// Holding the parent row until commit makes both save paths participate in the
// workspace teardown fence even though their tables deliberately have no FK.
func lockMemoryWorkspace(w http.ResponseWriter, r *http.Request, qtx *db.Queries, ws pgtype.UUID) bool {
	_, err := qtx.LockWorkspaceForMemoryWrite(r.Context(), ws)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock memory workspace")
		return false
	}
	return true
}
