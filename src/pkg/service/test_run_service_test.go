package service

import (
	"testing"
	"time"
)

func TestListTestRunScenariosContainsStage0ToB(t *testing.T) {
	scenarios := ListTestRunScenarios()
	if len(scenarios) < 5 {
		t.Fatalf("expected at least 5 scenarios, got=%d", len(scenarios))
	}

	var foundSuite bool
	for _, item := range scenarios {
		if item.Name == "stage0_to_b_full_smoke" {
			foundSuite = true
			if !item.Recommended {
				t.Fatalf("stage0_to_b_full_smoke should be recommended")
			}
			break
		}
	}
	if !foundSuite {
		t.Fatalf("stage0_to_b_full_smoke not found in scenario catalog")
	}
}

func TestIsAllowedScenario(t *testing.T) {
	if !isAllowedScenario("stage0_runtime_object_smoke") {
		t.Fatalf("stage0_runtime_object_smoke should be allowed")
	}
	if !isAllowedScenario("e5_gating_blocked_smoke") {
		t.Fatalf("e5_gating_blocked_smoke should be allowed")
	}
	if isAllowedScenario("unknown_scenario") {
		t.Fatalf("unknown scenario should not be allowed")
	}
}

func TestScenarioTimeoutForStage0ToBSuite(t *testing.T) {
	if got := scenarioTimeout("stage0_to_b_full_smoke"); got < 10*time.Minute {
		t.Fatalf("unexpected timeout for suite: %v", got)
	}
}
