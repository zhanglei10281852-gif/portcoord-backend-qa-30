package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"portcoord/internal/berthing"
	"portcoord/internal/domain"
)

func (s *Server) CreateWindow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateWindowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		effectiveAt, err := parseTime(req.EffectiveAt)
		if err != nil {
			writeValidation(w, r, "invalid effective_at")
			return
		}
		deadlineAt, err := parseTime(req.DeadlineAt)
		if err != nil {
			writeValidation(w, r, "invalid deadline_at")
			return
		}
		win, err := s.windowSvc.Create(r.Context(), berthing.CreateRequest{
			DeclarationID:    req.DeclarationID,
			BerthNumber:      req.BerthNumber,
			ShipName:         req.ShipName,
			EffectiveAt:      effectiveAt,
			DeadlineAt:       deadlineAt,
			ResponsibleParty: domain.PartyRole(req.ResponsibleParty),
			RequestID:        requestIDFromContext(r.Context()),
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, windowToResponse(win))
	}
}

func (s *Server) BatchAllocateWindows() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req BatchWindowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		var items []berthing.BatchItem
		for _, ri := range req.Items {
			effectiveAt, err := parseTime(ri.EffectiveAt)
			if err != nil {
				writeValidation(w, r, "invalid effective_at in batch item")
				return
			}
			deadlineAt, err := parseTime(ri.DeadlineAt)
			if err != nil {
				writeValidation(w, r, "invalid deadline_at in batch item")
				return
			}
			items = append(items, berthing.BatchItem{
				DeclarationID:    ri.DeclarationID,
				BerthNumber:      ri.BerthNumber,
				ShipName:         ri.ShipName,
				EffectiveAt:      effectiveAt,
				DeadlineAt:       deadlineAt,
				ResponsibleParty: domain.PartyRole(ri.ResponsibleParty),
			})
		}
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "dispatcher"
		}
		windows, err := s.windowSvc.BatchAllocate(r.Context(), actor, items, requestIDFromContext(r.Context()))
		if err != nil {
			writeError(w, r, err)
			return
		}
		resp := make([]WindowResponse, 0, len(windows))
		for _, win := range windows {
			resp = append(resp, windowToResponse(win))
		}
		writeJSON(w, http.StatusCreated, map[string]any{"items": resp, "count": len(resp)})
	}
}

func (s *Server) ListWindows() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := parsePageQuery(
			r.URL.Query().Get("page"),
			r.URL.Query().Get("page_size"),
			r.URL.Query().Get("status"),
			map[string]string{
				"berth_number": r.URL.Query().Get("berth"),
				"ship_name":    r.URL.Query().Get("ship"),
			},
		)
		result, err := s.windowSvc.List(r.Context(), q)
		if err != nil {
			writeError(w, r, err)
			return
		}
		items := make([]WindowResponse, 0, len(result.Items))
		for _, win := range result.Items {
			items = append(items, windowToResponse(win))
		}
		writeJSON(w, http.StatusOK, ListResponse[WindowResponse]{
			Items: items, Meta: pageMeta(result),
		})
	}
}

func (s *Server) GetWindow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		win, err := s.windowSvc.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, windowToResponse(win))
	}
}

func (s *Server) ReleaseWindow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "dispatcher"
		}
		if err := s.windowSvc.Release(r.Context(), id, actor, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "released", "id": id})
	}
}

func (s *Server) InterveneWindow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			TargetState string `json:"target_state"`
			Actor       string `json:"actor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		if req.Actor == "" {
			req.Actor = "supervisor"
		}
		if err := s.windowSvc.ForceIntervene(r.Context(), id, req.Actor, domain.WindowStatus(req.TargetState), requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": req.TargetState, "id": id})
	}
}
