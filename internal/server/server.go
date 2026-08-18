package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/berthing"
	"portcoord/internal/config"
	"portcoord/internal/declaration"
	"portcoord/internal/engine"
	"portcoord/internal/handover"
	"portcoord/internal/pilottask"
	"portcoord/internal/quota"
	"portcoord/internal/store"
	"portcoord/internal/worker"
	"portcoord/internal/workorder"
)

// Server is the HTTP entry point for the scheduler process.
// It wires together all business services, the scheduling engine,
// and the HTTP router. The executor process uses a separate Worker
// that shares the same SQLite database.
type Server struct {
	cfg         *config.Config
	store       store.Store
	declSvc     *declaration.Service
	windowSvc   *berthing.Service
	orderSvc    *workorder.Service
	taskSvc     *pilottask.Service
	quotaSvc    *quota.Service
	handoverSvc *handover.Service
	auditSvc    *audit.Recorder
	engine      *engine.Engine
	worker      *worker.Worker
	logger      *apperr.Logger
	httpServer  *http.Server
}

// Deps bundles all dependencies needed to construct the Server.
type Deps struct {
	Cfg    *config.Config
	Store  store.Store
	Clock  apperr.Clock
	Logger *apperr.Logger
}

// New constructs a Server with all services wired up.
func New(deps Deps) *Server {
	auditRecorder := audit.New(deps.Store, deps.Clock)

	declSvc := declaration.New(declaration.Deps{
		Declarations: deps.Store,
		Quotas:       deps.Store,
		Idempotency:  deps.Store,
		Handovers:    deps.Store,
		Audit:        auditRecorder,
		Clock:        deps.Clock,
		Logger:       deps.Logger,
		CabinLimit:   deps.Cfg.Quotas.DailyCabinLimit,
		YardLimit:    deps.Cfg.Quotas.DailyYardLimit,
	})

	windowSvc := berthing.New(berthing.Deps{
		Windows:      deps.Store,
		Declarations: deps.Store,
		Escalations:  deps.Store,
		Handovers:    deps.Store,
		Audit:        auditRecorder,
		Clock:        deps.Clock,
		Logger:       deps.Logger,
		LeaseTimeout: deps.Cfg.Scheduler.LeaseTimeout,
	})

	orderSvc := workorder.New(workorder.Deps{
		Orders: deps.Store,
		Audit:  auditRecorder,
		Clock:  deps.Clock,
		Logger: deps.Logger,
	})

	taskSvc := pilottask.New(pilottask.Deps{
		Tasks:        deps.Store,
		Leases:       deps.Store,
		Executions:   deps.Store,
		Audit:        auditRecorder,
		Clock:        deps.Clock,
		Logger:       deps.Logger,
		LeaseTimeout: deps.Cfg.Scheduler.LeaseTimeout,
	})

	quotaSvc := quota.New(quota.Deps{
		Quotas:     deps.Store,
		Audit:      auditRecorder,
		Clock:      deps.Clock,
		Logger:     deps.Logger,
		CabinLimit: deps.Cfg.Quotas.DailyCabinLimit,
		YardLimit:  deps.Cfg.Quotas.DailyYardLimit,
	})

	handoverSvc := handover.New(handover.Deps{
		Handovers: deps.Store,
		Audit:     auditRecorder,
		Clock:     deps.Clock,
		Logger:    deps.Logger,
	})

	eng := engine.New(engine.Deps{
		DeclarationService: declSvc,
		BerthingService:    windowSvc,
		PilotTaskService:   taskSvc,
		Clock:              deps.Clock,
		Logger:             deps.Logger,
		TickInterval:       deps.Cfg.Scheduler.TickInterval,
	})

	srv := &Server{
		cfg:         deps.Cfg,
		store:       deps.Store,
		declSvc:     declSvc,
		windowSvc:   windowSvc,
		orderSvc:    orderSvc,
		taskSvc:     taskSvc,
		quotaSvc:    quotaSvc,
		handoverSvc: handoverSvc,
		auditSvc:    auditRecorder,
		engine:      eng,
		logger:      deps.Logger,
	}

	return srv
}

// SetWorker attaches an executor worker for inline execution (testing/debug).
func (s *Server) SetWorker(w *worker.Worker) {
	s.worker = w
}

// Router returns the configured chi router with all routes registered.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	s.registerRoutes(r)
	return r
}

// Start begins listening for HTTP requests and launches the scheduling engine.
func (s *Server) Start(ctx context.Context) error {
	handler := s.buildHandler()
	s.httpServer = &http.Server{
		Addr:         addr(s.cfg),
		Handler:      handler,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
		IdleTimeout:  s.cfg.Server.IdleTimeout,
	}

	s.engine.Start(ctx)

	s.logger.Info("http server starting", apperr.F("port", s.cfg.Server.Port))
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the HTTP server and scheduling engine.
func (s *Server) Shutdown(ctx context.Context) error {
	s.engine.Stop()
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) buildHandler() http.Handler {
	r := chi.NewRouter()
	s.registerRoutes(r)

	var handler http.Handler = r
	handler = requestIDMiddleware(handler)
	handler = corsMiddleware(handler)
	handler = recoveryMiddleware(handler)
	handler = timeoutMiddleware(s.cfg.Server.WriteTimeout, handler)

	var zlog zerolog.Logger
	handler = loggingMiddleware(&zlog, handler)
	return handler
}

func addr(cfg *config.Config) string {
	return ":" + itoa(cfg.Server.Port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// HealthHandler returns a simple health check handler.
func (s *Server) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "healthy",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	}
}

// ReadyHandler returns a readiness check that verifies the database is accessible.
func (s *Server) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		_, err := s.store.ListAllQuotas(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not ready",
				"error":  err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
