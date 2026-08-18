package server

import "github.com/go-chi/chi/v5"

// registerRoutes wires all HTTP routes to their handlers. Each route has a
// distinct business semantic — no alias routes or health-check padding.
func (s *Server) registerRoutes(r chi.Router) {
	r.Get("/health", s.HealthHandler())
	r.Get("/ready", s.ReadyHandler())

	r.Route("/api/v1", func(r chi.Router) {
		// Arrival declarations
		r.Post("/declarations", s.SubmitDeclaration())
		r.Get("/declarations", s.ListDeclarations())
		r.Get("/declarations/{id}", s.GetDeclaration())
		r.Put("/declarations/{id}/cancel", s.CancelDeclaration())
		r.Put("/declarations/{id}/priority", s.UpdateDeclarationPriority())

		// Berthing windows
		r.Post("/berthing-windows", s.CreateWindow())
		r.Post("/berthing-windows/batch", s.BatchAllocateWindows())
		r.Get("/berthing-windows", s.ListWindows())
		r.Get("/berthing-windows/{id}", s.GetWindow())
		r.Put("/berthing-windows/{id}/release", s.ReleaseWindow())
		r.Put("/berthing-windows/{id}/intervene", s.InterveneWindow())

		// Work orders
		r.Post("/work-orders", s.CreateWorkOrder())
		r.Get("/work-orders", s.ListWorkOrders())
		r.Get("/work-orders/{id}", s.GetWorkOrder())
		r.Put("/work-orders/{id}/assign", s.AssignWorkOrder())
		r.Put("/work-orders/{id}/start", s.StartWorkOrder())
		r.Put("/work-orders/{id}/complete", s.CompleteWorkOrder())
		r.Put("/work-orders/{id}/cancel", s.CancelWorkOrder())

		// Pilot/tug tasks
		r.Post("/pilot-tasks", s.CreatePilotTask())
		r.Get("/pilot-tasks", s.ListPilotTasks())
		r.Get("/pilot-tasks/{id}", s.GetPilotTask())
		r.Put("/pilot-tasks/{id}/assign", s.AssignPilotTask())
		r.Put("/pilot-tasks/{id}/claim", s.ClaimPilotTask())
		r.Put("/pilot-tasks/{id}/report", s.ReportPilotTask())

		// Quotas
		r.Get("/quotas", s.ListQuotas())
		r.Get("/quotas/{id}", s.GetQuota())
		r.Post("/quotas/reserve", s.ReserveQuota())
		r.Put("/quotas/{id}/commit", s.CommitQuota())
		r.Put("/quotas/{id}/release", s.ReleaseQuotaHandler())

		// Handover documents
		r.Post("/handover-documents", s.CreateHandover())
		r.Get("/handover-documents", s.ListHandovers())
		r.Get("/handover-documents/{id}", s.GetHandover())
		r.Put("/handover-documents/{id}/confirm", s.ConfirmHandover())
		r.Put("/handover-documents/{id}/reject", s.RejectHandover())

		// Audit and escalation
		r.Get("/audit-logs", s.ListAuditLogs())
		r.Get("/escalations", s.ListEscalations())

		// Backlog and export
		r.Get("/backlog", s.GetBacklog())
		r.Get("/export/reconciliation", s.ExportReconciliation())
		r.Get("/executions", s.ListExecutions())

		// Engine and worker stats
		r.Get("/engine/stats", s.EngineStats())

		// Forced intervention
		r.Post("/intervene", s.ForceIntervene())
	})
}
