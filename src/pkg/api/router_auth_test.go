package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"model-control-plane/src/pkg/version"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRouterHealthzNoAuthRequired(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, testLogger(), version.Get())
	router := NewRouter(handler, "", "secret-token", testLogger())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("healthz should be public, got status=%d", rr.Code)
	}
}

func TestRouterAPIRequiresBearerTokenWhenConfigured(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, testLogger(), version.Get())
	router := NewRouter(handler, "", "secret-token", testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("api should require token, got status=%d", rr.Code)
	}

	reqOK := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	reqOK.Header.Set("Authorization", "Bearer secret-token")
	rrOK := httptest.NewRecorder()
	router.ServeHTTP(rrOK, reqOK)
	if rrOK.Code != http.StatusOK {
		t.Fatalf("api with valid token should pass, got status=%d", rrOK.Code)
	}
}

func TestRouterAPIAllowsAnonymousWhenTokenEmpty(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, testLogger(), version.Get())
	router := NewRouter(handler, "", "", testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("api should allow anonymous when auth token empty, got status=%d", rr.Code)
	}
}
