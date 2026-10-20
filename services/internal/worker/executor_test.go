package worker

import (
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
	"testing"
)

func TestBuildReconciliationNeverBlindlyRepeatsAnUnknownWrite(t *testing.T) {
	attempt := BuildAttempt{SchemaVersion: 1, BeforeID: "before", BeforeNumber: 3, TemplateVersion: "version", Transition: "start", Phase: "START"}
	matching := workspaceengine.Build{ID: "after", BuildNumber: 4, Transition: "start", TemplateVersionID: "version"}
	if result := buildDisposition(attempt, matching, false); result != "ADOPT" {
		t.Fatalf("matching durable successor was not adopted: %s", result)
	}
	if result := buildDisposition(attempt, workspaceengine.Build{ID: "before", BuildNumber: 3}, false); result != "UNCERTAIN" {
		t.Fatalf("recovered write was reissued: %s", result)
	}
	if result := buildDisposition(attempt, workspaceengine.Build{ID: "before", BuildNumber: 3}, true); result != "ISSUE" {
		t.Fatalf("fresh durable intent cannot execute: %s", result)
	}
	for _, bad := range []workspaceengine.Build{{ID: "after", BuildNumber: 5, Transition: "start", TemplateVersionID: "version"}, {ID: "after", BuildNumber: 4, Transition: "stop", TemplateVersionID: "version"}, {ID: "after", BuildNumber: 4, Transition: "start", TemplateVersionID: "foreign"}} {
		if result := buildDisposition(attempt, bad, false); result != "CONFLICT" {
			t.Fatalf("foreign build was adopted: %s", result)
		}
	}
}
