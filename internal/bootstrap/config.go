package bootstrap

import (
	"errors"
	"os"
	"strings"
)

const (
	envStatePath                        = "ONBOARDING_BOOTSTRAP_STATE_PATH"
	envMatrixHomeserverURL              = "ONBOARDING_BOOTSTRAP_MATRIX_HOMESERVER_URL"
	envMatrixServerName                 = "ONBOARDING_BOOTSTRAP_MATRIX_SERVER_NAME"
	envRegistrationToken                = "ONBOARDING_BOOTSTRAP_REGISTRATION_TOKEN"
	envManagerURL                       = "ONBOARDING_BOOTSTRAP_MANAGER_URL"
	envMatrixBindableServiceName        = "ONBOARDING_BOOTSTRAP_MATRIX_BINDABLE_SERVICE_NAME"
	envManagerBindableServiceName       = "ONBOARDING_BOOTSTRAP_MANAGER_BINDABLE_SERVICE_NAME"
	envSharedResponsesServiceName       = "ONBOARDING_BOOTSTRAP_SHARED_RESPONSES_BINDABLE_SERVICE_NAME"
	envSharedResponsesServiceID         = "ONBOARDING_BOOTSTRAP_SHARED_RESPONSES_BINDABLE_SERVICE_ID"
	envBootstrapAdminUsername           = "ONBOARDING_BOOTSTRAP_ADMIN_USERNAME"
	envBootstrapAdminPassword           = "ONBOARDING_BOOTSTRAP_ADMIN_PASSWORD"
	envOnboardingBotUsername            = "ONBOARDING_BOOTSTRAP_ONBOARDING_BOT_USERNAME"
	envOnboardingBotPassword            = "ONBOARDING_BOOTSTRAP_ONBOARDING_BOT_PASSWORD"
	envWelcomeRoomAliasLocalpart        = "ONBOARDING_BOOTSTRAP_WELCOME_ROOM_ALIAS_LOCALPART"
	envProvisionerSourceURL             = "ONBOARDING_BOOTSTRAP_PROVISIONER_SOURCE_URL"
	envOnboardingSourceURL              = "ONBOARDING_BOOTSTRAP_ONBOARDING_SOURCE_URL"
	envDefaultAgentSourceURL            = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_SOURCE_URL"
	envAuthProxySourceURL               = "ONBOARDING_BOOTSTRAP_AUTH_PROXY_SOURCE_URL"
	envCodexAuthJSONPath                = "ONBOARDING_BOOTSTRAP_CODEX_AUTH_JSON_PATH"
	envOnboardingModel                  = "ONBOARDING_BOOTSTRAP_ONBOARDING_MODEL"
	envOnboardingModelReasoningEffort   = "ONBOARDING_BOOTSTRAP_ONBOARDING_MODEL_REASONING_EFFORT"
	envDefaultAgentModel                = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_MODEL"
	envDefaultAgentModelReasoningEffort = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_MODEL_REASONING_EFFORT"
	envOnboardingDeveloperPath          = "ONBOARDING_BOOTSTRAP_ONBOARDING_DEVELOPER_INSTRUCTIONS_PATH"
	envOnboardingWorkspaceAgentsPath    = "ONBOARDING_BOOTSTRAP_ONBOARDING_WORKSPACE_AGENTS_MD_PATH"
	envOnboardingConfigTOMLPath         = "ONBOARDING_BOOTSTRAP_ONBOARDING_CONFIG_TOML_PATH"
	envDefaultDeveloperPath             = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_DEVELOPER_INSTRUCTIONS_PATH"
	envDefaultAgentsPath                = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_AGENTS_MD_PATH"
	envDefaultWorkspaceAgentsPath       = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_WORKSPACE_AGENTS_MD_PATH"
	envDefaultConfigTOMLPath            = "ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_CONFIG_TOML_PATH"

	defaultMatrixBindableServiceName      = "matrix"
	defaultManagerBindableServiceName     = "amber-manager-api"
	defaultSharedResponsesServiceName     = "responses-api"
	defaultWelcomeRoomAliasLocalpart      = "welcome"
	defaultOnboardingModel                = "gpt-5.4-mini"
	defaultDefaultAgentModel              = "gpt-5.4-mini"
	defaultOnboardingModelReasoningEffort = "medium"
	defaultDefaultAgentReasoningEffort    = "medium"
)

type Config struct {
	StatePath                             string
	MatrixHomeserverURL                   string
	MatrixServerName                      string
	RegistrationToken                     string
	ManagerURL                            string
	MatrixBindableServiceName             string
	ManagerBindableServiceName            string
	SharedResponsesBindableServiceName    string
	SharedResponsesBindableServiceID      string
	BootstrapAdminUsername                string
	BootstrapAdminPassword                string
	OnboardingBotUsername                 string
	OnboardingBotPassword                 string
	WelcomeRoomAliasLocalpart             string
	ProvisionerSourceURL                  string
	OnboardingSourceURL                   string
	DefaultAgentSourceURL                 string
	AuthProxySourceURL                    string
	CodexAuthJSONPath                     string
	OnboardingModel                       string
	OnboardingModelReasoningEffort        string
	DefaultAgentModel                     string
	DefaultAgentModelReasoningEffort      string
	OnboardingDeveloperInstructionsPath   string
	OnboardingWorkspaceAgentsPath         string
	OnboardingConfigTOMLPath              string
	DefaultAgentDeveloperInstructionsPath string
	DefaultAgentAgentsPath                string
	DefaultAgentWorkspaceAgentsPath       string
	DefaultAgentConfigTOMLPath            string
}

func FromEnv() (Config, error) {
	cfg := Config{
		StatePath:                             strings.TrimSpace(os.Getenv(envStatePath)),
		MatrixHomeserverURL:                   strings.TrimSpace(os.Getenv(envMatrixHomeserverURL)),
		MatrixServerName:                      strings.TrimSpace(os.Getenv(envMatrixServerName)),
		RegistrationToken:                     strings.TrimSpace(os.Getenv(envRegistrationToken)),
		ManagerURL:                            strings.TrimSpace(os.Getenv(envManagerURL)),
		MatrixBindableServiceName:             strings.TrimSpace(os.Getenv(envMatrixBindableServiceName)),
		ManagerBindableServiceName:            strings.TrimSpace(os.Getenv(envManagerBindableServiceName)),
		SharedResponsesBindableServiceName:    strings.TrimSpace(os.Getenv(envSharedResponsesServiceName)),
		SharedResponsesBindableServiceID:      strings.TrimSpace(os.Getenv(envSharedResponsesServiceID)),
		BootstrapAdminUsername:                strings.TrimSpace(os.Getenv(envBootstrapAdminUsername)),
		BootstrapAdminPassword:                os.Getenv(envBootstrapAdminPassword),
		OnboardingBotUsername:                 strings.TrimSpace(os.Getenv(envOnboardingBotUsername)),
		OnboardingBotPassword:                 os.Getenv(envOnboardingBotPassword),
		WelcomeRoomAliasLocalpart:             strings.TrimSpace(os.Getenv(envWelcomeRoomAliasLocalpart)),
		ProvisionerSourceURL:                  strings.TrimSpace(os.Getenv(envProvisionerSourceURL)),
		OnboardingSourceURL:                   strings.TrimSpace(os.Getenv(envOnboardingSourceURL)),
		DefaultAgentSourceURL:                 strings.TrimSpace(os.Getenv(envDefaultAgentSourceURL)),
		AuthProxySourceURL:                    strings.TrimSpace(os.Getenv(envAuthProxySourceURL)),
		CodexAuthJSONPath:                     strings.TrimSpace(os.Getenv(envCodexAuthJSONPath)),
		OnboardingModel:                       strings.TrimSpace(os.Getenv(envOnboardingModel)),
		OnboardingModelReasoningEffort:        strings.TrimSpace(os.Getenv(envOnboardingModelReasoningEffort)),
		DefaultAgentModel:                     strings.TrimSpace(os.Getenv(envDefaultAgentModel)),
		DefaultAgentModelReasoningEffort:      strings.TrimSpace(os.Getenv(envDefaultAgentModelReasoningEffort)),
		OnboardingDeveloperInstructionsPath:   strings.TrimSpace(os.Getenv(envOnboardingDeveloperPath)),
		OnboardingWorkspaceAgentsPath:         strings.TrimSpace(os.Getenv(envOnboardingWorkspaceAgentsPath)),
		OnboardingConfigTOMLPath:              strings.TrimSpace(os.Getenv(envOnboardingConfigTOMLPath)),
		DefaultAgentDeveloperInstructionsPath: strings.TrimSpace(os.Getenv(envDefaultDeveloperPath)),
		DefaultAgentAgentsPath:                strings.TrimSpace(os.Getenv(envDefaultAgentsPath)),
		DefaultAgentWorkspaceAgentsPath:       strings.TrimSpace(os.Getenv(envDefaultWorkspaceAgentsPath)),
		DefaultAgentConfigTOMLPath:            strings.TrimSpace(os.Getenv(envDefaultConfigTOMLPath)),
	}
	if cfg.MatrixBindableServiceName == "" {
		cfg.MatrixBindableServiceName = defaultMatrixBindableServiceName
	}
	if cfg.ManagerBindableServiceName == "" {
		cfg.ManagerBindableServiceName = defaultManagerBindableServiceName
	}
	if cfg.SharedResponsesBindableServiceName == "" {
		cfg.SharedResponsesBindableServiceName = defaultSharedResponsesServiceName
	}
	if cfg.WelcomeRoomAliasLocalpart == "" {
		cfg.WelcomeRoomAliasLocalpart = defaultWelcomeRoomAliasLocalpart
	}
	if cfg.OnboardingModel == "" {
		cfg.OnboardingModel = defaultOnboardingModel
	}
	if cfg.OnboardingModelReasoningEffort == "" {
		cfg.OnboardingModelReasoningEffort = defaultOnboardingModelReasoningEffort
	}
	if cfg.DefaultAgentModel == "" {
		cfg.DefaultAgentModel = defaultDefaultAgentModel
	}
	if cfg.DefaultAgentModelReasoningEffort == "" {
		cfg.DefaultAgentModelReasoningEffort = defaultDefaultAgentReasoningEffort
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var problems []string
	required := map[string]string{
		envStatePath:              c.StatePath,
		envMatrixHomeserverURL:    c.MatrixHomeserverURL,
		envMatrixServerName:       c.MatrixServerName,
		envManagerURL:             c.ManagerURL,
		envBootstrapAdminUsername: c.BootstrapAdminUsername,
		envOnboardingBotUsername:  c.OnboardingBotUsername,
		envProvisionerSourceURL:   c.ProvisionerSourceURL,
		envOnboardingSourceURL:    c.OnboardingSourceURL,
		envDefaultAgentSourceURL:  c.DefaultAgentSourceURL,
		envOnboardingModel:        c.OnboardingModel,
		envDefaultAgentModel:      c.DefaultAgentModel,
	}
	for envName, value := range required {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, envName+" is required")
		}
	}
	if strings.TrimSpace(c.BootstrapAdminPassword) == "" {
		problems = append(problems, envBootstrapAdminPassword+" is required")
	}
	if strings.TrimSpace(c.OnboardingBotPassword) == "" {
		problems = append(problems, envOnboardingBotPassword+" is required")
	}
	if c.SharedResponsesBindableServiceID == "" {
		if c.AuthProxySourceURL == "" && c.SharedResponsesBindableServiceName == "" {
			problems = append(problems, "either shared responses bindable service id/name or auth proxy source URL is required")
		}
		if c.AuthProxySourceURL != "" && c.CodexAuthJSONPath == "" && c.SharedResponsesBindableServiceName == "" {
			problems = append(problems, envCodexAuthJSONPath+" is required when using auth proxy bootstrap")
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
