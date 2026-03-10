package fit

import "testing"

func TestBuildServeArgsFromEndpoint(t *testing.T) {
	args, err := buildServeArgs("http://127.0.0.1:18123", nil)
	if err != nil {
		t.Fatalf("buildServeArgs returned error: %v", err)
	}
	if len(args) != 5 {
		t.Fatalf("unexpected args length: %d", len(args))
	}
	expect := []string{"serve", "--host", "127.0.0.1", "--port", "18123"}
	for i := range expect {
		if args[i] != expect[i] {
			t.Fatalf("unexpected arg at %d, got=%s want=%s", i, args[i], expect[i])
		}
	}
}

func TestNormalizeServeArgsPrependsServe(t *testing.T) {
	args := normalizeServeArgs([]string{"--host", "0.0.0.0", "--port", "19090"})
	expect := []string{"serve", "--host", "0.0.0.0", "--port", "19090"}
	if len(args) != len(expect) {
		t.Fatalf("unexpected args length: %d", len(args))
	}
	for i := range expect {
		if args[i] != expect[i] {
			t.Fatalf("unexpected arg at %d, got=%s want=%s", i, args[i], expect[i])
		}
	}
}
