package lmstudio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ModelIntegrator/src/pkg/model"
)

func TestLoadModelUsesAPIPathFirst(t *testing.T) {
	var hit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = append(hit, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{"key": "demo-model", "display_name": "Demo Model", "loaded_instances": []interface{}{}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/load":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	adapter := NewAdapter(srv.URL, "", 2*time.Second, false, 0)
	result, err := adapter.LoadModel(context.Background(), model.Model{ID: "demo-model", Name: "Demo Model"})
	if err != nil {
		t.Fatalf("LoadModel error: %v", err)
	}
	if !result.Success {
		t.Fatalf("LoadModel result not success: %+v", result)
	}
	if got := result.Detail["path"]; got != "/api/v1/models/load" {
		t.Fatalf("LoadModel used path=%v, want=/api/v1/models/load", got)
	}
}

func TestUnloadModelFallbackToLegacyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{"key": "demo-model", "display_name": "Demo Model", "loaded_instances": []interface{}{map[string]interface{}{"id": "demo-model"}}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/unload":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"legacy only"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/models/unload":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	adapter := NewAdapter(srv.URL, "", 2*time.Second, false, 0)
	result, err := adapter.UnloadModel(context.Background(), model.Model{ID: "demo-model", Name: "Demo Model"})
	if err != nil {
		t.Fatalf("UnloadModel error: %v", err)
	}
	if !result.Success {
		t.Fatalf("UnloadModel result not success: %+v", result)
	}
	path, _ := result.Detail["path"].(string)
	if !strings.HasPrefix(path, "/v1/models/unload") {
		t.Fatalf("UnloadModel fallback path=%s, want /v1/models/unload", path)
	}
}

func TestUnloadModelUsesInstanceIDOnAPIPath(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{
						"key":              "demo-model",
						"display_name":     "Demo Model",
						"loaded_instances": []interface{}{map[string]interface{}{"id": "inst-123"}},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/unload":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	adapter := NewAdapter(srv.URL, "", 2*time.Second, false, 0)
	result, err := adapter.UnloadModel(context.Background(), model.Model{ID: "demo-model", Name: "Demo Model"})
	if err != nil {
		t.Fatalf("UnloadModel error: %v", err)
	}
	if !result.Success {
		t.Fatalf("UnloadModel result not success: %+v", result)
	}
	if got := result.Detail["path"]; got != "/api/v1/models/unload" {
		t.Fatalf("UnloadModel used path=%v, want=/api/v1/models/unload", got)
	}
	if _, ok := gotBody["instance_id"]; !ok {
		t.Fatalf("UnloadModel body missing instance_id: %+v", gotBody)
	}
	if gotBody["instance_id"] != "inst-123" {
		t.Fatalf("UnloadModel instance_id=%v, want inst-123", gotBody["instance_id"])
	}
}

func TestListModelsPrefersAPIPath(t *testing.T) {
	var hit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = append(hit, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{"key": "demo-model", "display_name": "Demo Model", "loaded_instances": []interface{}{}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"should not hit legacy"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()

	adapter := NewAdapter(srv.URL, "", 2*time.Second, false, 0)
	models, err := adapter.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "demo-model" {
		t.Fatalf("ListModels unexpected result: %+v", models)
	}
	for _, h := range hit {
		if h == "GET /v1/models" {
			t.Fatalf("ListModels should prefer /api/v1/models, but hit legacy path")
		}
	}
}
