package bootstrap

import "testing"

func TestFromEnvDefaultsProvisionedUserAgentModelToGPT54Low(t *testing.T) {
	t.Setenv(envStatePath, "/tmp/bootstrap-state.json")
	t.Setenv(envMatrixHomeserverURL, "http://matrix.test")
	t.Setenv(envMatrixServerName, "matrix.test")
	t.Setenv(envRegistrationToken, "")
	t.Setenv(envManagerURL, "http://manager.test")
	t.Setenv(envMatrixBindableServiceName, "")
	t.Setenv(envManagerBindableServiceName, "")
	t.Setenv(envSharedResponsesServiceName, "")
	t.Setenv(envSharedResponsesServiceID, "")
	t.Setenv(envBootstrapAdminUsername, "bootstrap-admin")
	t.Setenv(envBootstrapAdminPassword, "bootstrap-admin-pass")
	t.Setenv(envOnboardingBotUsername, "onboarding")
	t.Setenv(envOnboardingBotPassword, "onboarding-pass")
	t.Setenv(envWelcomeRoomAliasLocalpart, "")
	t.Setenv(envProvisionerSourceURL, "file:///provisioner.json5")
	t.Setenv(envOnboardingSourceURL, "file:///onboarding-agent.json5")
	t.Setenv(envDefaultAgentSourceURL, "file:///user-agent.json5")
	t.Setenv(envAuthProxySourceURL, "")
	t.Setenv(envCodexAuthJSONPath, "")
	t.Setenv(envOnboardingModel, "")
	t.Setenv(envOnboardingModelReasoningEffort, "")
	t.Setenv(envDefaultAgentModel, "")
	t.Setenv(envDefaultAgentModelReasoningEffort, "")
	t.Setenv(envOnboardingDeveloperPath, "")
	t.Setenv(envOnboardingWorkspaceAgentsPath, "")
	t.Setenv(envOnboardingConfigTOMLPath, "")
	t.Setenv(envDefaultDeveloperPath, "")
	t.Setenv(envDefaultAgentsPath, "")
	t.Setenv(envDefaultWorkspaceAgentsPath, "")
	t.Setenv(envDefaultConfigTOMLPath, "")

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
