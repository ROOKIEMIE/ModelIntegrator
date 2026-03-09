package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"ModelIntegrator/src/pkg/config"
)

type Server struct {
	httpServer      *http.Server
	logger          *slog.Logger
	shutdownTimeout time.Duration
}

func New(cfg *config.Config, handler http.Handler, logger *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         cfg.Server.Address,
			Handler:      handler,
			ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
			WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
		},
		logger:          logger,
		shutdownTimeout: time.Duration(cfg.Server.ShutdownTimeoutSeconds) * time.Second,
	}
}

func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("HTTP 服务启动", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.logger.Info("收到退出信号，准备关闭 HTTP 服务")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("HTTP 服务优雅关闭失败: %w", err)
		}
		return nil
	}
}
