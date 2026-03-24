package provisioner

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ricelines/chat/onboarding/internal/config"
	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"github.com/ricelines/chat/onboarding/internal/matrix"
	"github.com/ricelines/chat/onboarding/internal/store"
)

const (
	provisioningModeOnboardingDefault = "onboarding-default"
	provisioningInstanceKeyDefault    = "default"
	defaultAgentTemplate              = "default-agent/v1"
	waitPollInterval                  = 200 * time.Millisecond
	waitTimeout                       = 5 * time.Minute
	generatedUsernamePrefixLimit      = 12
	generatedUsernameSuffixLength     = 8
	generatedPasswordLength           = 24
)

type MatrixAPI interface {
	CreateUser(context.Context, string, string) (matrix.CreateUserResult, error)
	LoginUser(context.Context, string, string) (matrix.LoginUserResult, error)
}

type ManagerAPI interface {
	RemoveAllowlistEntry(context.Context, string) error
	ListBindableServices(context.Context) ([]managerclient.BindableServiceResponse, error)
	CreateScenario(context.Context, managerclient.CreateScenarioRequest) (managerclient.EnqueueOperationResponse, error)
	GetOperation(context.Context, string) (managerclient.OperationStatusResponse, error)
	ListScenarios(context.Context) ([]managerclient.ScenarioSummaryResponse, error)
	GetScenario(context.Context, string) (managerclient.ScenarioDetailResponse, error)
}

type Service struct {
	store   *store.Store
	matrix  MatrixAPI
	manager ManagerAPI
	config  config.Config
	now     func() time.Time
	logf    func(string, ...any)
	mu      sync.Mutex
}

type GetUserAgentsInput struct {
	OwnerMatrixUserID string `json:"owner_matrix_user_id,omitempty"`
}

type UserAgentRecord struct {
	OwnerMatrixUserID       string `json:"owner_matrix_user_id"`
	ProvisioningMode        string `json:"provisioning_mode"`
	ProvisioningInstanceKey string `json:"provisioning_instance_key"`
	State                   string `json:"state"`
	BotUserID               string `json:"bot_user_id,omitempty"`
	BotUsername             string `json:"bot_username,omitempty"`
	ScenarioID              string `json:"scenario_id,omitempty"`
	CreatedAt               string `json:"created_at,omitempty"`
	UpdatedAt               string `json:"updated_at,omitempty"`
	CompletedAt             string `json:"completed_at,omitempty"`
}

type GetUserAgentsOutput struct {
	UserAgents []UserAgentRecord `json:"user_agents"`
}

type ProvisionInitialInput struct {
	OwnerMatrixUserID string `json:"owner_matrix_user_id"`
	BotUsername       string `json:"bot_username,omitempty"`
	BotPassword       string `json:"bot_password,omitempty"`
}

type ProvisionInitialOutput struct {
	Created       bool   `json:"created"`
	AlreadyExists bool   `json:"already_exists"`
	ScenarioID    string `json:"scenario_id"`
	BotUserID     string `json:"bot_user_id"`
	BotUsername   string `json:"bot_username"`
	BotPassword   string `json:"bot_password,omitempty"`
}

func NewService(db *store.Store, matrixClient MatrixAPI, managerClient ManagerAPI, cfg config.Config) *Service {
	return &Service{
		store:   db,
		matrix:  matrixClient,
		manager: managerClient,
		config:  cfg,
		now:     time.Now,
		logf:    func(string, ...any) {},
	}
}

func (s *Service) SetLogger(logf func(string, ...any)) {
	if logf == nil {
		s.logf = func(string, ...any) {}
		return
	}
	s.logf = logf
}

func (s *Service) GetUserAgents(ctx context.Context, input GetUserAgentsInput) (GetUserAgentsOutput, error) {
	records, err := s.store.ListUserAgents(ctx, strings.TrimSpace(input.OwnerMatrixUserID))
	if err != nil {
		return GetUserAgentsOutput{}, err
	}

	output := GetUserAgentsOutput{UserAgents: make([]UserAgentRecord, 0, len(records))}
	for _, record := range records {
		output.UserAgents = append(output.UserAgents, UserAgentRecord{
			OwnerMatrixUserID:       record.OwnerMatrixUserID,
			ProvisioningMode:        record.ProvisioningMode,
			ProvisioningInstanceKey: record.ProvisioningInstanceKey,
			State:                   record.State,
			BotUserID:               record.BotUserID,
			BotUsername:             record.BotUsername,
			ScenarioID:              record.ScenarioID,
			CreatedAt:               formatTime(record.CreatedAt),
			UpdatedAt:               formatTime(record.UpdatedAt),
			CompletedAt:             formatTime(record.CompletedAt),
		})
	}
	return output, nil
}

func (s *Service) ProvisionInitial(ctx context.Context, input ProvisionInitialInput) (result ProvisionInitialOutput, err error) {
	if err := validateProvisionInput(input); err != nil {
		return ProvisionInitialOutput{}, err
	}

	startedAt := time.Now()
	logStep := func(step string, stepStartedAt time.Time, details string) {
		if details != "" {
			s.logf(
				"provision_initial owner=%s step=%s step_elapsed=%s total_elapsed=%s %s",
				input.OwnerMatrixUserID,
				step,
				time.Since(stepStartedAt).Round(time.Millisecond),
				time.Since(startedAt).Round(time.Millisecond),
				details,
			)
			return
		}
		s.logf(
			"provision_initial owner=%s step=%s step_elapsed=%s total_elapsed=%s",
			input.OwnerMatrixUserID,
			step,
			time.Since(stepStartedAt).Round(time.Millisecond),
			time.Since(startedAt).Round(time.Millisecond),
		)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	reserveStartedAt := time.Now()
	record, _, err := s.store.ReserveInitialUserAgent(
		ctx,
		input.OwnerMatrixUserID,
		provisioningModeOnboardingDefault,
		provisioningInstanceKeyDefault,
		input.BotUsername,
		input.BotPassword,
	)
	if err != nil {
		return ProvisionInitialOutput{}, err
	}
	logStep("reserve_record", reserveStartedAt, "state="+record.State)

	defer func() {
		if err == nil {
			return
		}
		record.LastError = err.Error()
		_ = s.store.SaveUserAgent(ctx, record)
	}()

	if record.State == store.StateCompleted {
		logStep("already_exists", startedAt, "scenario_id="+record.ScenarioID)
		return ProvisionInitialOutput{
			Created:       false,
			AlreadyExists: true,
			ScenarioID:    record.ScenarioID,
			BotUserID:     record.BotUserID,
			BotUsername:   record.BotUsername,
		}, nil
	}

	credentialsStartedAt := time.Now()
	if err := s.ensureProvisionCredentials(ctx, &record, input); err != nil {
		return ProvisionInitialOutput{}, err
	}
	logStep("ensure_credentials", credentialsStartedAt, "bot_username="+record.BotUsername)
	password := record.BotPassword

	if record.BotUserID == "" {
		matrixUserStartedAt := time.Now()
		userID, err := s.ensureMatrixUser(ctx, record.BotUsername, password)
		if err != nil {
			return ProvisionInitialOutput{}, err
		}
		record.BotUserID = userID
		record.State = store.StateMatrixUserCreated
		record.LastError = ""
		if err := s.store.SaveUserAgent(ctx, record); err != nil {
			return ProvisionInitialOutput{}, err
		}
		logStep("ensure_matrix_user", matrixUserStartedAt, "bot_user_id="+record.BotUserID)
	}

	allowlistStartedAt := time.Now()
	if err := s.tightenAllowlist(ctx); err != nil {
		return ProvisionInitialOutput{}, err
	}
	logStep("tighten_allowlist", allowlistStartedAt, "")

	if record.ScenarioID == "" {
		findScenarioStartedAt := time.Now()
		existingScenarioID, err := s.findExistingScenarioID(ctx, record)
		if err != nil {
			return ProvisionInitialOutput{}, err
		}
		logStep("find_existing_scenario", findScenarioStartedAt, "existing_scenario_id="+existingScenarioID)
		if existingScenarioID == "" {
			lookupMatrixServiceStartedAt := time.Now()
			matrixBindableServiceID, err := s.lookupMatrixBindableServiceID(ctx)
			if err != nil {
				return ProvisionInitialOutput{}, err
			}
			logStep("lookup_matrix_bindable_service", lookupMatrixServiceStartedAt, "bindable_service_id="+matrixBindableServiceID)

			createScenarioStartedAt := time.Now()
			created, err := s.manager.CreateScenario(ctx, s.buildCreateScenarioRequest(
				record,
				password,
				matrixBindableServiceID,
			))
			if err != nil {
				return ProvisionInitialOutput{}, err
			}
			record.ScenarioID = created.ScenarioID
			record.State = store.StateScenarioCreated
			record.LastError = ""
			if err := s.store.SaveUserAgent(ctx, record); err != nil {
				return ProvisionInitialOutput{}, err
			}
			logStep("create_scenario", createScenarioStartedAt, "scenario_id="+record.ScenarioID+" operation_id="+created.OperationID)
			waitOperationStartedAt := time.Now()
			if err := s.waitForOperationSucceeded(ctx, created.OperationID); err != nil {
				return ProvisionInitialOutput{}, err
			}
			logStep("wait_operation_succeeded", waitOperationStartedAt, "operation_id="+created.OperationID)
		} else {
			record.ScenarioID = existingScenarioID
			record.State = store.StateScenarioCreated
			record.LastError = ""
			if err := s.store.SaveUserAgent(ctx, record); err != nil {
				return ProvisionInitialOutput{}, err
			}
			logStep("reuse_existing_scenario", findScenarioStartedAt, "scenario_id="+record.ScenarioID)
		}
	}

	waitRunningStartedAt := time.Now()
	if err := s.waitForScenarioRunning(ctx, record.ScenarioID); err != nil {
		return ProvisionInitialOutput{}, err
	}
	logStep("wait_scenario_running", waitRunningStartedAt, "scenario_id="+record.ScenarioID)

	record.State = store.StateCompleted
	record.BotPassword = ""
	record.LastError = ""
	record.CompletedAt = s.now().UTC()
	if err := s.store.SaveUserAgent(ctx, record); err != nil {
		return ProvisionInitialOutput{}, err
	}
	logStep("completed", startedAt, "scenario_id="+record.ScenarioID)

	return ProvisionInitialOutput{
		Created:       true,
		AlreadyExists: false,
		ScenarioID:    record.ScenarioID,
		BotUserID:     record.BotUserID,
		BotUsername:   record.BotUsername,
		BotPassword:   password,
	}, nil
}

func (s *Service) ensureProvisionCredentials(ctx context.Context, record *store.UserAgentRecord, input ProvisionInitialInput) error {
	changed := false

	if strings.TrimSpace(record.BotUsername) == "" {
		username := strings.TrimSpace(input.BotUsername)
		if username == "" {
			generated, err := generateBotUsername(input.OwnerMatrixUserID)
			if err != nil {
				return err
			}
			username = generated
		}
		record.BotUsername = username
		changed = true
	}

	if strings.TrimSpace(record.BotPassword) == "" {
		password := input.BotPassword
		if strings.TrimSpace(password) == "" {
			generated, err := generateSecret(generatedPasswordLength)
			if err != nil {
				return err
			}
			password = generated
		}
		record.BotPassword = password
		changed = true
	}

	if !changed {
		return nil
	}

	record.LastError = ""
	return s.store.SaveUserAgent(ctx, *record)
}

func (s *Service) ensureMatrixUser(ctx context.Context, username, password string) (string, error) {
	created, err := s.matrix.CreateUser(ctx, username, password)
	if err == nil {
		return created.UserID, nil
	}
	if !errors.Is(err, matrix.ErrUserAlreadyExists) {
		return "", err
	}
	loggedIn, loginErr := s.matrix.LoginUser(ctx, username, password)
	if loginErr != nil {
		return "", fmt.Errorf("matrix username %s already exists and could not be recovered with the reserved password: %w", username, loginErr)
	}
	return loggedIn.UserID, nil
}

func (s *Service) lookupMatrixBindableServiceID(ctx context.Context) (string, error) {
	services, err := s.manager.ListBindableServices(ctx)
	if err != nil {
		return "", err
	}
	for _, service := range services {
		if service.DisplayName == s.config.MatrixBindableServiceName {
			if !service.Available {
				return "", fmt.Errorf("matrix bindable service %q is not available", s.config.MatrixBindableServiceName)
			}
			return service.BindableServiceID, nil
		}
	}
	return "", fmt.Errorf("matrix bindable service %q was not found", s.config.MatrixBindableServiceName)
}

func (s *Service) tightenAllowlist(ctx context.Context) error {
	for _, sourceURL := range s.config.RevokedSourceURLs {
		err := s.manager.RemoveAllowlistEntry(ctx, sourceURL)
		if err == nil || errors.Is(err, managerclient.ErrAllowlistEntryMissing) {
			continue
		}
		return fmt.Errorf("remove scenario source allowlist entry %q: %w", sourceURL, err)
	}
	return nil
}

func (s *Service) findExistingScenarioID(ctx context.Context, record store.UserAgentRecord) (string, error) {
	scenarios, err := s.manager.ListScenarios(ctx)
	if err != nil {
		return "", err
	}

	var matches []string
	for _, scenario := range scenarios {
		if scenario.SourceURL != s.config.DefaultAgentSourceURL {
			continue
		}
		if !scenarioMatchesRecord(scenario, record) {
			continue
		}
		matches = append(matches, scenario.ScenarioID)
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("found multiple matching default-agent scenarios for owner %s", record.OwnerMatrixUserID)
	}
}

func scenarioMatchesRecord(scenario managerclient.ScenarioSummaryResponse, record store.UserAgentRecord) bool {
	metadata := scenario.Metadata
	if metadata == nil {
		return false
	}
	return stringValue(metadata["kind"]) == "user-agent" &&
		stringValue(metadata["owner_matrix_user_id"]) == record.OwnerMatrixUserID &&
		stringValue(metadata["provisioning_mode"]) == record.ProvisioningMode &&
		stringValue(metadata["provisioning_instance_key"]) == record.ProvisioningInstanceKey &&
		(record.BotUsername == "" || stringValue(metadata["bot_username"]) == record.BotUsername) &&
		(record.BotUserID == "" || stringValue(metadata["bot_matrix_user_id"]) == record.BotUserID)
}

func (s *Service) buildCreateScenarioRequest(
	record store.UserAgentRecord,
	password string,
	matrixBindableServiceID string,
) managerclient.CreateScenarioRequest {
	rootConfig := map[string]any{
		"matrix_username":        record.BotUsername,
		"matrix_password":        password,
		"model":                  s.config.DefaultAgentModel,
		"model_reasoning_effort": s.config.DefaultAgentModelReasoningEffort,
	}
	if developerInstructions := appendOwnerDeveloperInstructions(
		s.config.DefaultAgentDeveloperInstructions,
		record.OwnerMatrixUserID,
	); developerInstructions != "" {
		rootConfig["developer_instructions"] = developerInstructions
	}
	if strings.TrimSpace(s.config.DefaultAgentConfigTOML) != "" {
		rootConfig["config_toml"] = s.config.DefaultAgentConfigTOML
	}
	if strings.TrimSpace(s.config.DefaultAgentAgentsMD) != "" {
		rootConfig["agents_md"] = s.config.DefaultAgentAgentsMD
	}
	if strings.TrimSpace(s.config.DefaultAgentWorkspaceAgentsMD) != "" {
		rootConfig["workspace_agents_md"] = s.config.DefaultAgentWorkspaceAgentsMD
	}

	return managerclient.CreateScenarioRequest{
		SourceURL:  s.config.DefaultAgentSourceURL,
		RootConfig: rootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"matrix": {
				BindableServiceID: matrixBindableServiceID,
			},
			"responses_api": {
				BindableServiceID: s.config.SharedResponsesBindableServiceID,
			},
		},
		Metadata: map[string]any{
			"kind":                      "user-agent",
			"owner_matrix_user_id":      record.OwnerMatrixUserID,
			"provisioning_source":       "onboarding",
			"provisioning_mode":         record.ProvisioningMode,
			"provisioning_instance_key": record.ProvisioningInstanceKey,
			"bot_matrix_user_id":        record.BotUserID,
			"bot_username":              record.BotUsername,
			"template":                  defaultAgentTemplate,
		},
		StoreBundle: true,
		Start:       true,
	}
}

func (s *Service) waitForOperationSucceeded(ctx context.Context, operationID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	for {
		status, err := s.manager.GetOperation(waitCtx, operationID)
		if err != nil {
			return err
		}
		switch status.Status {
		case "succeeded":
			return nil
		case "failed":
			if strings.TrimSpace(status.LastError) != "" {
				return fmt.Errorf("manager operation %s failed: %s", operationID, status.LastError)
			}
			return fmt.Errorf("manager operation %s failed", operationID)
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for manager operation %s: %w", operationID, waitCtx.Err())
		case <-time.After(waitPollInterval):
		}
	}
}

func (s *Service) waitForScenarioRunning(ctx context.Context, scenarioID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	for {
		scenario, err := s.manager.GetScenario(waitCtx, scenarioID)
		if err != nil {
			return err
		}
		switch scenario.ObservedState {
		case "running":
			return nil
		case "failed":
			if strings.TrimSpace(scenario.LastError) != "" {
				return fmt.Errorf("scenario %s failed: %s", scenarioID, scenario.LastError)
			}
			return fmt.Errorf("scenario %s failed", scenarioID)
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for scenario %s to become running: %w", scenarioID, waitCtx.Err())
		case <-time.After(waitPollInterval):
		}
	}
}

func validateProvisionInput(input ProvisionInitialInput) error {
	switch {
	case strings.TrimSpace(input.OwnerMatrixUserID) == "":
		return fmt.Errorf("owner_matrix_user_id must not be empty")
	case strings.Contains(strings.TrimSpace(input.BotUsername), "@"):
		return fmt.Errorf("bot_username must be a localpart, not a full Matrix user ID")
	default:
		return nil
	}
}

func generateBotUsername(ownerMatrixUserID string) (string, error) {
	prefix := ownerUsernamePrefix(ownerMatrixUserID)
	suffix, err := generateSecret(generatedUsernameSuffixLength)
	if err != nil {
		return "", fmt.Errorf("generate bot username: %w", err)
	}
	return prefix + "bot" + suffix, nil
}

func ownerUsernamePrefix(ownerMatrixUserID string) string {
	localpart := strings.TrimSpace(ownerMatrixUserID)
	if strings.HasPrefix(localpart, "@") {
		localpart = localpart[1:]
	}
	if idx := strings.IndexByte(localpart, ':'); idx >= 0 {
		localpart = localpart[:idx]
	}

	var builder strings.Builder
	for _, r := range strings.ToLower(localpart) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}

	prefix := builder.String()
	if prefix == "" {
		return "user"
	}
	if len(prefix) > generatedUsernamePrefixLimit {
		return prefix[:generatedUsernamePrefixLimit]
	}
	return prefix
}

func generateSecret(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("secret length must be positive")
	}
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	if len(encoded) < length {
		return "", fmt.Errorf("generated secret shorter than requested length")
	}
	return encoded[:length], nil
}

func appendOwnerDeveloperInstructions(baseInstructions, ownerMatrixUserID string) string {
	baseInstructions = strings.TrimSpace(baseInstructions)
	ownerMatrixUserID = strings.TrimSpace(ownerMatrixUserID)
	if ownerMatrixUserID == "" {
		return baseInstructions
	}
	createdBy := fmt.Sprintf("You are created by %s.", ownerMatrixUserID)
	if baseInstructions == "" {
		return createdBy
	}
	return createdBy + "\n\n" + baseInstructions
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
