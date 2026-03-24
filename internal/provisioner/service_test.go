package provisioner

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ricelines/chat/onboarding/internal/config"
	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"github.com/ricelines/chat/onboarding/internal/matrix"
	"github.com/ricelines/chat/onboarding/internal/store"
)

func TestProvisionInitialCreatesUserAndScenario(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	matrixClient := &fakeMatrixClient{
		createUserResult: matrix.CreateUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		createScenarioResponse: managerclient.EnqueueOperationResponse{
			ScenarioID:  "scn_user_agent",
			OperationID: "op_create",
		},
		operationStatuses: map[string][]managerclient.OperationStatusResponse{
			"op_create": {{
				OperationID: "op_create",
				Status:      "succeeded",
			}},
		},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_user_agent": {{
				ScenarioID:    "scn_user_agent",
				ObservedState: "running",
			}},
		},
	}

	service := NewService(db, matrixClient, managerClient, testConfig())
	output, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
		BotUsername:       "alice-bot",
		BotPassword:       "secret-pass",
	})
	if err != nil {
		t.Fatalf("ProvisionInitial() error = %v", err)
	}

	if !output.Created || output.AlreadyExists {
		t.Fatalf("unexpected output flags = %#v", output)
	}
	if output.BotPassword != "secret-pass" {
		t.Fatalf("output.BotPassword = %q, want secret-pass", output.BotPassword)
	}
	if matrixClient.createUserCalls != 1 {
		t.Fatalf("createUserCalls = %d, want 1", matrixClient.createUserCalls)
	}
	if len(managerClient.createdScenarios) != 1 {
		t.Fatalf("createdScenarios = %d, want 1", len(managerClient.createdScenarios))
	}

	request := managerClient.createdScenarios[0]
	if request.SourceURL != testConfig().DefaultAgentSourceURL {
		t.Fatalf("SourceURL = %q", request.SourceURL)
	}
	if !request.StoreBundle || !request.Start {
		t.Fatalf("unexpected create scenario flags = %#v", request)
	}
	if request.ExternalSlots["responses_api"].BindableServiceID != testConfig().SharedResponsesBindableServiceID {
		t.Fatalf("responses_api binding = %#v", request.ExternalSlots["responses_api"])
	}
	if request.ExternalSlots["matrix"].BindableServiceID != "svc_matrix" {
		t.Fatalf("matrix binding = %#v", request.ExternalSlots["matrix"])
	}
	if request.Metadata["template"] != defaultAgentTemplate {
		t.Fatalf("metadata template = %#v", request.Metadata["template"])
	}
	developerInstructions, _ := request.RootConfig["developer_instructions"].(string)
	if developerInstructions == "" {
		t.Fatalf("developer_instructions should include default prompt content")
	}
	if developerInstructions != "You are created by @alice:example.com.\n\nBase instructions" {
		t.Fatalf("developer_instructions = %q", developerInstructions)
	}

	record, found, err := db.GetUserAgent(context.Background(), "@alice:example.com", provisioningModeOnboardingDefault, provisioningInstanceKeyDefault)
	if err != nil {
		t.Fatalf("GetUserAgent() error = %v", err)
	}
	if !found {
		t.Fatal("expected provisioning record to exist")
	}
	if record.State != store.StateCompleted {
		t.Fatalf("record.State = %q, want %q", record.State, store.StateCompleted)
	}
	if record.BotPassword != "" {
		t.Fatalf("record.BotPassword should be cleared after completion")
	}
}

func TestProvisionInitialGeneratesCredentialsWhenOmitted(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	matrixClient := &fakeMatrixClient{
		createUserResult: matrix.CreateUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		createScenarioResponse: managerclient.EnqueueOperationResponse{
			ScenarioID:  "scn_user_agent",
			OperationID: "op_create",
		},
		operationStatuses: map[string][]managerclient.OperationStatusResponse{
			"op_create": {{
				OperationID: "op_create",
				Status:      "succeeded",
			}},
		},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_user_agent": {{
				ScenarioID:    "scn_user_agent",
				ObservedState: "running",
			}},
		},
	}

	service := NewService(db, matrixClient, managerClient, testConfig())
	output, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
	})
	if err != nil {
		t.Fatalf("ProvisionInitial() error = %v", err)
	}

	if output.BotUsername == "" {
		t.Fatal("BotUsername should be generated")
	}
	if output.BotPassword == "" {
		t.Fatal("BotPassword should be generated")
	}
	if output.BotUsername == "alice-bot" {
		t.Fatal("generated BotUsername should not depend on the old required input shape")
	}
	request := managerClient.createdScenarios[0]
	if got := request.RootConfig["matrix_username"]; got != output.BotUsername {
		t.Fatalf("matrix_username = %#v, want %q", got, output.BotUsername)
	}
	if got := request.RootConfig["matrix_password"]; got != output.BotPassword {
		t.Fatalf("matrix_password = %#v, want generated password", got)
	}
}

func TestProvisionInitialReturnsAlreadyExistsForCompletedRecord(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	matrixClient := &fakeMatrixClient{
		createUserResult: matrix.CreateUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		createScenarioResponse: managerclient.EnqueueOperationResponse{
			ScenarioID:  "scn_user_agent",
			OperationID: "op_create",
		},
		operationStatuses: map[string][]managerclient.OperationStatusResponse{
			"op_create": {{
				OperationID: "op_create",
				Status:      "succeeded",
			}},
		},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_user_agent": {{
				ScenarioID:    "scn_user_agent",
				ObservedState: "running",
			}},
		},
	}
	service := NewService(db, matrixClient, managerClient, testConfig())

	if _, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
		BotUsername:       "alice-bot",
		BotPassword:       "secret-pass",
	}); err != nil {
		t.Fatalf("first ProvisionInitial() error = %v", err)
	}

	output, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
		BotUsername:       "different-name",
		BotPassword:       "different-pass",
	})
	if err != nil {
		t.Fatalf("second ProvisionInitial() error = %v", err)
	}
	if output.Created || !output.AlreadyExists {
		t.Fatalf("unexpected second output = %#v", output)
	}
	if output.BotPassword != "" {
		t.Fatalf("second output should not return a password")
	}
	if matrixClient.createUserCalls != 1 {
		t.Fatalf("createUserCalls = %d, want 1", matrixClient.createUserCalls)
	}
	if len(managerClient.createdScenarios) != 1 {
		t.Fatalf("createdScenarios = %d, want 1", len(managerClient.createdScenarios))
	}
}

func TestProvisionInitialResumeUsesStoredGeneratedCredentials(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	matrixClient := &fakeMatrixClient{
		createUserResult: matrix.CreateUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		createScenarioResponse: managerclient.EnqueueOperationResponse{
			ScenarioID:  "scn_user_agent",
			OperationID: "op_create",
		},
		operationStatuses: map[string][]managerclient.OperationStatusResponse{
			"op_create": {{
				OperationID: "op_create",
				Status:      "succeeded",
			}},
		},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_user_agent": {{
				ScenarioID:    "scn_user_agent",
				ObservedState: "running",
			}},
		},
	}

	service := NewService(db, matrixClient, managerClient, testConfig())
	first, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
	})
	if err != nil {
		t.Fatalf("first ProvisionInitial() error = %v", err)
	}
	if first.BotUsername == "" || first.BotPassword == "" {
		t.Fatalf("generated credentials missing in first output = %#v", first)
	}

	second, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
	})
	if err != nil {
		t.Fatalf("second ProvisionInitial() error = %v", err)
	}
	if second.BotUsername != first.BotUsername {
		t.Fatalf("second BotUsername = %q, want %q", second.BotUsername, first.BotUsername)
	}
	if second.BotPassword != "" {
		t.Fatalf("second BotPassword = %q, want empty on already exists", second.BotPassword)
	}
}

func TestProvisionInitialRecoversExistingMatrixUserByLoggingIn(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	matrixClient := &fakeMatrixClient{
		createUserErr:   matrix.ErrUserAlreadyExists,
		loginUserResult: matrix.LoginUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		createScenarioResponse: managerclient.EnqueueOperationResponse{
			ScenarioID:  "scn_user_agent",
			OperationID: "op_create",
		},
		operationStatuses: map[string][]managerclient.OperationStatusResponse{
			"op_create": {{
				OperationID: "op_create",
				Status:      "succeeded",
			}},
		},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_user_agent": {{
				ScenarioID:    "scn_user_agent",
				ObservedState: "running",
			}},
		},
	}

	service := NewService(db, matrixClient, managerClient, testConfig())
	output, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
		BotUsername:       "alice-bot",
		BotPassword:       "secret-pass",
	})
	if err != nil {
		t.Fatalf("ProvisionInitial() error = %v", err)
	}
	if !output.Created {
		t.Fatalf("unexpected output = %#v", output)
	}
	if matrixClient.loginUserCalls != 1 {
		t.Fatalf("loginUserCalls = %d, want 1", matrixClient.loginUserCalls)
	}
}

func TestProvisionInitialAdoptsExistingScenarioByMetadata(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	matrixClient := &fakeMatrixClient{
		createUserResult: matrix.CreateUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		listScenarios: []managerclient.ScenarioSummaryResponse{{
			ScenarioID:    "scn_existing",
			SourceURL:     testConfig().DefaultAgentSourceURL,
			ObservedState: "starting",
			Metadata: map[string]any{
				"kind":                      "user-agent",
				"owner_matrix_user_id":      "@alice:example.com",
				"provisioning_mode":         provisioningModeOnboardingDefault,
				"provisioning_instance_key": provisioningInstanceKeyDefault,
				"bot_matrix_user_id":        "@bot:example.com",
				"bot_username":              "alice-bot",
			},
		}},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_existing": {{
				ScenarioID:    "scn_existing",
				ObservedState: "running",
			}},
		},
	}

	service := NewService(db, matrixClient, managerClient, testConfig())
	output, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
		BotUsername:       "alice-bot",
		BotPassword:       "secret-pass",
	})
	if err != nil {
		t.Fatalf("ProvisionInitial() error = %v", err)
	}
	if output.ScenarioID != "scn_existing" {
		t.Fatalf("ScenarioID = %q, want scn_existing", output.ScenarioID)
	}
	if len(managerClient.createdScenarios) != 0 {
		t.Fatalf("createdScenarios = %d, want 0", len(managerClient.createdScenarios))
	}
}

func TestProvisionInitialRemovesConfiguredAllowlistEntries(t *testing.T) {
	t.Parallel()

	db := openTestStore(t)
	cfg := testConfig()
	cfg.RevokedSourceURLs = []string{
		"file:///provisioner.json5",
		"file:///onboarding.json5",
	}
	matrixClient := &fakeMatrixClient{
		createUserResult: matrix.CreateUserResult{UserID: "@bot:example.com"},
	}
	managerClient := &fakeManagerClient{
		bindableServices: []managerclient.BindableServiceResponse{{
			BindableServiceID: "svc_matrix",
			DisplayName:       "matrix",
			Available:         true,
		}},
		removeAllowlistErrs: map[string]error{
			"file:///onboarding.json5": managerclient.ErrAllowlistEntryMissing,
		},
		createScenarioResponse: managerclient.EnqueueOperationResponse{
			ScenarioID:  "scn_user_agent",
			OperationID: "op_create",
		},
		operationStatuses: map[string][]managerclient.OperationStatusResponse{
			"op_create": {{
				OperationID: "op_create",
				Status:      "succeeded",
			}},
		},
		scenarioDetails: map[string][]managerclient.ScenarioDetailResponse{
			"scn_user_agent": {{
				ScenarioID:    "scn_user_agent",
				ObservedState: "running",
			}},
		},
	}

	service := NewService(db, matrixClient, managerClient, cfg)
	if _, err := service.ProvisionInitial(context.Background(), ProvisionInitialInput{
		OwnerMatrixUserID: "@alice:example.com",
		BotUsername:       "alice-bot",
		BotPassword:       "secret-pass",
	}); err != nil {
		t.Fatalf("ProvisionInitial() error = %v", err)
	}
	if len(managerClient.removedAllowlistEntries) != 2 {
		t.Fatalf("removedAllowlistEntries = %#v", managerClient.removedAllowlistEntries)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "provisioner.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func testConfig() config.Config {
	return config.Config{
		ListenAddr:                        ":0",
		DBPath:                            "/tmp/unused.sqlite",
		MatrixHomeserverURL:               "http://matrix.test",
		ManagerURL:                        "http://manager.test",
		MatrixBindableServiceName:         "matrix",
		DefaultAgentSourceURL:             "file:///user-agent.json5",
		SharedResponsesBindableServiceID:  "svc_responses",
		DefaultAgentModel:                 "gpt-5.4",
		DefaultAgentModelReasoningEffort:  "low",
		DefaultAgentDeveloperInstructions: "Base instructions",
	}
}

type fakeMatrixClient struct {
	createUserResult matrix.CreateUserResult
	createUserErr    error
	loginUserResult  matrix.LoginUserResult
	loginUserErr     error
	createUserCalls  int
	loginUserCalls   int
}

func (f *fakeMatrixClient) CreateUser(ctx context.Context, username, password string) (matrix.CreateUserResult, error) {
	f.createUserCalls++
	if f.createUserErr != nil {
		return matrix.CreateUserResult{}, f.createUserErr
	}
	return f.createUserResult, nil
}

func (f *fakeMatrixClient) LoginUser(ctx context.Context, username, password string) (matrix.LoginUserResult, error) {
	f.loginUserCalls++
	if f.loginUserErr != nil {
		return matrix.LoginUserResult{}, f.loginUserErr
	}
	return f.loginUserResult, nil
}

type fakeManagerClient struct {
	bindableServices        []managerclient.BindableServiceResponse
	removeAllowlistErrs     map[string]error
	removedAllowlistEntries []string
	listScenarios           []managerclient.ScenarioSummaryResponse
	createdScenarios        []managerclient.CreateScenarioRequest
	createScenarioResponse  managerclient.EnqueueOperationResponse
	createScenarioErr       error
	operationStatuses       map[string][]managerclient.OperationStatusResponse
	scenarioDetails         map[string][]managerclient.ScenarioDetailResponse
}

func (f *fakeManagerClient) RemoveAllowlistEntry(ctx context.Context, sourceURL string) error {
	f.removedAllowlistEntries = append(f.removedAllowlistEntries, sourceURL)
	if err := f.removeAllowlistErrs[sourceURL]; err != nil {
		return err
	}
	return nil
}

func (f *fakeManagerClient) ListBindableServices(ctx context.Context) ([]managerclient.BindableServiceResponse, error) {
	return append([]managerclient.BindableServiceResponse(nil), f.bindableServices...), nil
}

func (f *fakeManagerClient) CreateScenario(ctx context.Context, request managerclient.CreateScenarioRequest) (managerclient.EnqueueOperationResponse, error) {
	f.createdScenarios = append(f.createdScenarios, request)
	if f.createScenarioErr != nil {
		return managerclient.EnqueueOperationResponse{}, f.createScenarioErr
	}
	return f.createScenarioResponse, nil
}

func (f *fakeManagerClient) GetOperation(ctx context.Context, operationID string) (managerclient.OperationStatusResponse, error) {
	statuses := f.operationStatuses[operationID]
	if len(statuses) == 0 {
		return managerclient.OperationStatusResponse{}, errors.New("operation not found in fake manager")
	}
	status := statuses[0]
	if len(statuses) > 1 {
		f.operationStatuses[operationID] = statuses[1:]
	}
	return status, nil
}

func (f *fakeManagerClient) ListScenarios(ctx context.Context) ([]managerclient.ScenarioSummaryResponse, error) {
	return append([]managerclient.ScenarioSummaryResponse(nil), f.listScenarios...), nil
}

func (f *fakeManagerClient) GetScenario(ctx context.Context, scenarioID string) (managerclient.ScenarioDetailResponse, error) {
	details := f.scenarioDetails[scenarioID]
	if len(details) == 0 {
		return managerclient.ScenarioDetailResponse{}, errors.New("scenario not found in fake manager")
	}
	detail := details[0]
	if len(details) > 1 {
		f.scenarioDetails[scenarioID] = details[1:]
	}
	return detail, nil
}
