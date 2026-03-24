package config

import (
	"errors"
	"os"
	"slices"
	"strings"
)

const (
	envListenAddr                         = "ONBOARDING_LISTEN_ADDR"
	envDBPath                             = "ONBOARDING_DB_PATH"
	envMatrixHomeserverURL                = "ONBOARDING_MATRIX_HOMESERVER_URL"
	envRegistrationToken                  = "ONBOARDING_REGISTRATION_TOKEN"
	envManagerURL                         = "ONBOARDING_MANAGER_URL"
	envMatrixBindableServiceName          = "ONBOARDING_MATRIX_BINDABLE_SERVICE_NAME"
	envDefaultAgentSourceURL              = "ONBOARDING_DEFAULT_AGENT_SOURCE_URL"
	envSharedResponsesBindableServiceID   = "ONBOARDING_SHARED_RESPONSES_BINDABLE_SERVICE_ID"
	envDefaultAgentModel                  = "ONBOARDING_DEFAULT_AGENT_MODEL"
	envDefaultAgentModelReasoningEffort   = "ONBOARDING_DEFAULT_AGENT_MODEL_REASONING_EFFORT"
	envDefaultAgentDeveloperInstructions  = "ONBOARDING_DEFAULT_AGENT_DEVELOPER_INSTRUCTIONS"
	envDefaultAgentConfigTOML             = "ONBOARDING_DEFAULT_AGENT_CONFIG_TOML"
	envDefaultAgentAgentsMD               = "ONBOARDING_DEFAULT_AGENT_AGENTS_MD"
	envDefaultAgentWorkspaceAgentsMD      = "ONBOARDING_DEFAULT_AGENT_WORKSPACE_AGENTS_MD"
	envRevokedSourceURLs                  = "ONBOARDING_REVOKED_SOURCE_URLS"
	defaultListenAddr                     = ":8080"
	defaultMatrixBindableServiceName      = "matrix"
	defaultAgentModelValue                = "gpt-5.4-mini"
	defaultAgentModelReasoningEffortValue = "medium"
)

type Config struct {
	ListenAddr                        string
	DBPath                            string
	MatrixHomeserverURL               string
	RegistrationToken                 string
	ManagerURL                        string
	MatrixBindableServiceName         string
	DefaultAgentSourceURL             string
	SharedResponsesBindableServiceID  string
	DefaultAgentModel                 string
	DefaultAgentModelReasoningEffort  string
	DefaultAgentDeveloperInstructions string
	DefaultAgentConfigTOML            string
	DefaultAgentAgentsMD              string
	DefaultAgentWorkspaceAgentsMD     string
	RevokedSourceURLs                 []string
}

func FromEnv() (Config, error) {
	cfg := Config{
		ListenAddr:                        strings.TrimSpace(os.Getenv(envListenAddr)),
		DBPath:                            strings.TrimSpace(os.Getenv(envDBPath)),
		MatrixHomeserverURL:               strings.TrimSpace(os.Getenv(envMatrixHomeserverURL)),
		RegistrationToken:                 strings.TrimSpace(os.Getenv(envRegistrationToken)),
		ManagerURL:                        strings.TrimSpace(os.Getenv(envManagerURL)),
		MatrixBindableServiceName:         strings.TrimSpace(os.Getenv(envMatrixBindableServiceName)),
		DefaultAgentSourceURL:             strings.TrimSpace(os.Getenv(envDefaultAgentSourceURL)),
		SharedResponsesBindableServiceID:  strings.TrimSpace(os.Getenv(envSharedResponsesBindableServiceID)),
		DefaultAgentModel:                 strings.TrimSpace(os.Getenv(envDefaultAgentModel)),
		DefaultAgentModelReasoningEffort:  strings.TrimSpace(os.Getenv(envDefaultAgentModelReasoningEffort)),
		DefaultAgentDeveloperInstructions: os.Getenv(envDefaultAgentDeveloperInstructions),
		DefaultAgentConfigTOML:            os.Getenv(envDefaultAgentConfigTOML),
		DefaultAgentAgentsMD:              os.Getenv(envDefaultAgentAgentsMD),
		DefaultAgentWorkspaceAgentsMD:     os.Getenv(envDefaultAgentWorkspaceAgentsMD),
		RevokedSourceURLs:                 parseDelimitedURLs(os.Getenv(envRevokedSourceURLs)),
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = defaultListenAddr
	}
	if cfg.MatrixBindableServiceName == "" {
		cfg.MatrixBindableServiceName = defaultMatrixBindableServiceName
	}
	if cfg.DefaultAgentModel == "" {
		cfg.DefaultAgentModel = defaultAgentModelValue
	}
	if cfg.DefaultAgentModelReasoningEffort == "" {
		cfg.DefaultAgentModelReasoningEffort = defaultAgentModelReasoningEffortValue
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var problems []string
	if c.ListenAddr == "" {
		problems = append(problems, "listen addr must not be empty")
	}
	if c.DBPath == "" {
		problems = append(problems, envDBPath+" is required")
	}
	if c.MatrixHomeserverURL == "" {
		problems = append(problems, envMatrixHomeserverURL+" is required")
	}
	if c.ManagerURL == "" {
		problems = append(problems, envManagerURL+" is required")
	}
	if c.MatrixBindableServiceName == "" {
		problems = append(problems, "matrix bindable service name must not be empty")
	}
	if c.DefaultAgentSourceURL == "" {
		problems = append(problems, envDefaultAgentSourceURL+" is required")
	}
	if c.SharedResponsesBindableServiceID == "" {
		problems = append(problems, envSharedResponsesBindableServiceID+" is required")
	}
	if c.DefaultAgentModel == "" {
		problems = append(problems, envDefaultAgentModel+" is required")
	}
	if slices.Contains(c.RevokedSourceURLs, c.DefaultAgentSourceURL) {
		problems = append(problems, "revoked source URLs must not include the default agent source URL")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func parseDelimitedURLs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if !slices.Contains(result, field) {
			result = append(result, field)
		}
	}
	return result
}
