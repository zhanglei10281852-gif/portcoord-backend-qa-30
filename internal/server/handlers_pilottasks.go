package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"portcoord/internal/domain"
	"portcoord/internal/pilottask"
)

func (s *Server) CreatePilotTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreatePilotTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		if req.Priority == 0 {
			req.Priority = 5
		}
		task, err := s.taskSvc.Create(r.Context(), pilottask.CreateRequest{
			DeclarationID:    req.DeclarationID,
			BerthingWindowID: req.BerthingWindowID,
			TaskType:         domain.PilotTaskType(req.TaskType),
			Location:         req.Location,
			Priority:         req.Priority,
			RequestID:        requestIDFromContext(r.Context()),
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, pilotTaskToResponse(task))
	}
}

func (s *Server) ListPilotTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := parsePageQuery(
			r.URL.Query().Get("page"),
			r.URL.Query().Get("page_size"),
			r.URL.Query().Get("status"),
			map[string]string{
				"task_type":  r.URL.Query().Get("type"),
				"claimed_by": r.URL.Query().Get("executor"),
			},
		)
		result, err := s.taskSvc.List(r.Context(), q)
		if err != nil {
			writeError(w, r, err)
			return
		}
		items := make([]PilotTaskResponse, 0, len(result.Items))
		for _, t := range result.Items {
			items = append(items, pilotTaskToResponse(t))
		}
		writeJSON(w, http.StatusOK, ListResponse[PilotTaskResponse]{
			Items: items, Meta: pageMeta(result),
		})
	}
}

func (s *Server) GetPilotTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		task, err := s.taskSvc.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, pilotTaskToResponse(task))
	}
}

func (s *Server) AssignPilotTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Assignee string `json:"assignee"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "dispatcher"
		}
		if err := s.taskSvc.Assign(r.Context(), id, req.Assignee, actor, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "assigned", "id": id})
	}
}

func (s *Server) ClaimPilotTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req ClaimTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		result, err := s.taskSvc.Claim(r.Context(), pilottask.ClaimRequest{
			TaskID:     id,
			ExecutorID: req.ExecutorID,
			RequestID:  requestIDFromContext(r.Context()),
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"task_id":    result.TaskID,
			"lease_id":   result.LeaseID,
			"expires_at": result.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
}

func (s *Server) ReportPilotTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req ReportTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		if err := s.taskSvc.Report(r.Context(), pilottask.ReportRequest{
			TaskID:     id,
			ExecutorID: req.ExecutorID,
			Result:     req.Result,
			ReportData: req.ReportData,
			RequestID:  requestIDFromContext(r.Context()),
		}); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "completed", "id": id})
	}
}
