package config

import "testing"

func TestFromEnvDefaultsUserAgentModelToGPT54Low(t *testing.T) {
	t.Setenv(envListenAddr, "")
	t.Setenv(envDBPath, "/tmp/onboarding-test.db")
	t.Setenv(envMatrixHomeserverURL, "http://matrix.test")
	t.Setenv(envRegistrationToken, "")
	t.Setenv(envManagerURL, "http://manager.test")
	t.Setenv(envMatrixBindableServiceName, "")
	t.Setenv(envDefaultAgentSourceURL, "file:///user-agent.json5")
	t.Setenv(envSharedResponsesBindableServiceID, "svc_responses")
	t.Setenv(envDefaultAgentModel, "")
	t.Setenv(envDefaultAgentModelReasoningEffort, "")
	t.Setenv(envDefaultAgentDeveloperInstructions, "")
	t.Setenv(envDefaultAgentConfigTOML, "")
	t.Setenv(envDefaultAgentAgentsMD, "")
	t.Setenv(envDefaultAgentWorkspaceAgentsMD, "")
	t.Setenv(envRevokedSourceURLs, "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.DefaultAgentModel != "gpt-5.4" {
		t.Fatalf("DefaultAgentModel = %q, want gpt-5.4", cfg.DefaultAgentModel)
	}
	if cfg.DefaultAgentModelReasoningEffort != "low" {
		t.Fatalf("DefaultAgentModelReasoningEffort = %q, want low", cfg.DefaultAgentModelReasoningEffort)
	}
}
