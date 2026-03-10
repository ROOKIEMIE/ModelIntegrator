package lmstudio

import (
	"testing"

	"model-control-plane/src/pkg/model"
)

func TestParseModelState(t *testing.T) {
	testCases := []struct {
		name  string
		entry map[string]interface{}
		want  model.ModelState
	}{
		{
			name:  "loaded bool true",
			entry: map[string]interface{}{"loaded": true},
			want:  model.ModelStateLoaded,
		},
		{
			name:  "loaded string true",
			entry: map[string]interface{}{"loaded": "true"},
			want:  model.ModelStateLoaded,
		},
		{
			name:  "loaded number zero",
			entry: map[string]interface{}{"loaded": float64(0)},
			want:  model.ModelStateStopped,
		},
		{
			name:  "status not loaded",
			entry: map[string]interface{}{"status": "not-loaded"},
			want:  model.ModelStateStopped,
		},
		{
			name:  "nested loaded status",
			entry: map[string]interface{}{"state": map[string]interface{}{"loaded": "1"}},
			want:  model.ModelStateLoaded,
		},
		{
			name:  "loaded instances loaded",
			entry: map[string]interface{}{"loaded_instances": []interface{}{map[string]interface{}{"id": "m1"}}},
			want:  model.ModelStateLoaded,
		},
		{
			name:  "loaded instances empty",
			entry: map[string]interface{}{"loaded_instances": []interface{}{}},
			want:  model.ModelStateStopped,
		},
		{
			name:  "running serving",
			entry: map[string]interface{}{"state": "serving"},
			want:  model.ModelStateRunning,
		},
		{
			name:  "unknown state",
			entry: map[string]interface{}{"foo": "bar"},
			want:  model.ModelStateUnknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseModelState(tc.entry)
			if got != tc.want {
				t.Fatalf("parseModelState()=%s, want=%s", got, tc.want)
			}
		})
	}
}
