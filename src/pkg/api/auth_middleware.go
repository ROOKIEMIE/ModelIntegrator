package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

func bearerAuthMiddleware(expectedToken string, logger *slog.Logger) func(next http.Handler) http.Handler {
	token := strings.TrimSpace(expectedToken)
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 兼容开发模式：未配置 token 时允许匿名访问。
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			providedToken, ok := parseBearerToken(r.Header.Get("Authorization"))
			if !ok || subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
				logger.Warn("api 鉴权失败",
					"path", r.URL.Path,
					"method", r.Method,
					"remote_addr", r.RemoteAddr,
				)
				Fail(w, http.StatusUnauthorized, "未授权", "missing or invalid bearer token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func parseBearerToken(authHeader string) (string, bool) {
	value := strings.TrimSpace(authHeader)
	if value == "" {
		return "", false
	}

	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "Bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}
