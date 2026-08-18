package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"portcoord/internal/declaration"
	"portcoord/internal/domain"
)

func (s *Server) SubmitDeclaration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SubmitDeclarationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body: "+err.Error())
			return
		}
		eta, err := parseTime(req.ETA)
		if err != nil {
			writeValidation(w, r, "invalid eta: "+err.Error())
			return
		}
		if req.Priority == 0 {
			req.Priority = 5
		}
		result, err := s.declSvc.Submit(r.Context(), declaration.SubmitRequest{
			ShipName:        req.ShipName,
			IMONumber:       req.IMONumber,
			VoyageNumber:    req.VoyageNumber,
			ETA:             eta,
			BerthPreference: req.BerthPreference,
			CargoType:       req.CargoType,
			CargoVolume:     req.CargoVolume,
			CargoUnit:       req.CargoUnit,
			DeclaredBy:      req.DeclaredBy,
			DeclaringParty:  domain.PartyRole(req.DeclaringParty),
			Priority:        req.Priority,
			IdempotencyKey:  req.IdempotencyKey,
			RequestID:       requestIDFromContext(r.Context()),
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) ListDeclarations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := parsePageQuery(
			r.URL.Query().Get("page"),
			r.URL.Query().Get("page_size"),
			r.URL.Query().Get("status"),
			map[string]string{
				"declaring_party": r.URL.Query().Get("party"),
				"ship_name":       r.URL.Query().Get("ship"),
			},
		)
		result, err := s.declSvc.List(r.Context(), q)
		if err != nil {
			writeError(w, r, err)
			return
		}
		items := make([]DeclarationResponse, 0, len(result.Items))
		for _, d := range result.Items {
			items = append(items, declToResponse(d))
		}
		writeJSON(w, http.StatusOK, ListResponse[DeclarationResponse]{
			Items: items, Meta: pageMeta(result),
		})
	}
}

func (s *Server) GetDeclaration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		decl, err := s.declSvc.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, declToResponse(decl))
	}
}

func (s *Server) CancelDeclaration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "unknown"
		}
		if err := s.declSvc.Cancel(r.Context(), id, actor, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "id": id})
	}
}

func (s *Server) UpdateDeclarationPriority() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req UpdatePriorityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "supervisor"
		}
		if err := s.declSvc.UpdatePriority(r.Context(), id, actor, req.Priority, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "priority_updated", "id": id})
	}
}
