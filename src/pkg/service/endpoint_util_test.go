package service

import "testing"

func TestNormalizeControllerAccessibleEndpoint(t *testing.T) {
	t.Setenv("MCP_CONTAINER_HOST_ALIAS", "host.docker.internal")

	t.Run("rewrite_loopback_when_running_in_container", func(t *testing.T) {
		t.Setenv("MCP_RUNNING_IN_CONTAINER", "true")
		got := normalizeControllerAccessibleEndpoint("http://127.0.0.1:58001")
		want := "http://host.docker.internal:58001"
		if got != want {
			t.Fatalf("unexpected endpoint rewrite, want=%s got=%s", want, got)
		}
	})

	t.Run("keep_original_when_not_in_container", func(t *testing.T) {
		t.Setenv("MCP_RUNNING_IN_CONTAINER", "false")
		got := normalizeControllerAccessibleEndpoint("http://127.0.0.1:58001")
		want := "http://127.0.0.1:58001"
		if got != want {
			t.Fatalf("unexpected endpoint, want=%s got=%s", want, got)
		}
	})

	t.Run("keep_non_loopback_host", func(t *testing.T) {
		t.Setenv("MCP_RUNNING_IN_CONTAINER", "true")
		got := normalizeControllerAccessibleEndpoint("http://192.168.1.10:58001")
		want := "http://192.168.1.10:58001"
		if got != want {
			t.Fatalf("unexpected endpoint, want=%s got=%s", want, got)
		}
	})
}
