package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"portcoord/internal/domain"
	"portcoord/internal/workorder"
)

func (s *Server) CreateWorkOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateWorkOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		wo, err := s.orderSvc.Create(r.Context(), workorder.CreateRequest{
			DeclarationID:    req.DeclarationID,
			BerthingWindowID: req.BerthingWindowID,
			OrderType:        domain.WorkOrderType(req.OrderType),
			CargoType:        req.CargoType,
			PlannedVolume:    req.PlannedVolume,
			AssignedTo:       req.AssignedTo,
			RequestID:        requestIDFromContext(r.Context()),
		})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, workOrderToResponse(wo))
	}
}

func (s *Server) ListWorkOrders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := parsePageQuery(
			r.URL.Query().Get("page"),
			r.URL.Query().Get("page_size"),
			r.URL.Query().Get("status"),
			map[string]string{
				"order_type":     r.URL.Query().Get("type"),
				"declaration_id": r.URL.Query().Get("declaration"),
			},
		)
		result, err := s.orderSvc.List(r.Context(), q)
		if err != nil {
			writeError(w, r, err)
			return
		}
		items := make([]WorkOrderResponse, 0, len(result.Items))
		for _, wo := range result.Items {
			items = append(items, workOrderToResponse(wo))
		}
		writeJSON(w, http.StatusOK, ListResponse[WorkOrderResponse]{
			Items: items, Meta: pageMeta(result),
		})
	}
}

func (s *Server) GetWorkOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		wo, err := s.orderSvc.Get(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, workOrderToResponse(wo))
	}
}

func (s *Server) AssignWorkOrder() http.HandlerFunc {
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
		if err := s.orderSvc.Assign(r.Context(), id, req.Assignee, actor, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "assigned", "id": id})
	}
}

func (s *Server) StartWorkOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "executor"
		}
		if err := s.orderSvc.StartProgress(r.Context(), id, actor, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "in_progress", "id": id})
	}
}

func (s *Server) CompleteWorkOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req CompleteWorkOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeValidation(w, r, "invalid JSON body")
			return
		}
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "executor"
		}
		if err := s.orderSvc.Complete(r.Context(), workorder.CompleteRequest{
			ID:           id,
			ActualVolume: req.ActualVolume,
			Actor:        actor,
			RequestID:    requestIDFromContext(r.Context()),
		}); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "completed", "id": id})
	}
}

func (s *Server) CancelWorkOrder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		actor := r.URL.Query().Get("actor")
		if actor == "" {
			actor = "dispatcher"
		}
		if err := s.orderSvc.Cancel(r.Context(), id, actor, requestIDFromContext(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled", "id": id})
	}
}
