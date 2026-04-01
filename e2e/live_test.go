package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ricelines/chat/onboarding/internal/bootstrap"
	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"github.com/ricelines/chat/onboarding/internal/managerforwarders"
	matrixclient "github.com/ricelines/chat/onboarding/internal/matrix"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	liveEnvVar                  = "ONBOARDING_RUN_LIVE"
	liveRealEnvVar              = "ONBOARDING_RUN_LIVE_REAL"
	profileEnvVar               = "ONBOARDING_E2E_PROFILE"
	authProxySourceEnvVar       = "ONBOARDING_E2E_AUTH_PROXY_SOURCE_URL"
	codexAuthPathEnvVar         = "ONBOARDING_E2E_CODEX_AUTH_JSON_PATH"
	onboardingModelEnvVar       = "ONBOARDING_E2E_ONBOARDING_MODEL"
	onboardingReasoningEnvVar   = "ONBOARDING_E2E_ONBOARDING_REASONING_EFFORT"
	defaultAgentModelEnvVar     = "ONBOARDING_E2E_DEFAULT_AGENT_MODEL"
	defaultAgentReasoningEnvVar = "ONBOARDING_E2E_DEFAULT_AGENT_REASONING_EFFORT"
	managerImageEnvVar          = "ONBOARDING_E2E_AMBER_MANAGER_IMAGE"

	serverName                  = "tuwunel.test"
	tuwunelImage                = "ghcr.io/matrix-construct/tuwunel:v1.5.1"
	defaultAmberManagerImage    = "ghcr.io/rdi-foundation/amber-manager:v0.1"
	publishedUserAgentSourceURL = "https://raw.githubusercontent.com/ricelines/scenarios/refs/heads/main/amber/user-agent.json5"
	publishedAuthProxySourceURL = "https://raw.githubusercontent.com/ricelines/codex-a2a/refs/heads/main/amber/codex-auth-proxy.json5"
	tuwunelPort                 = 8008
	registrationToken           = "invite-only-token"

	bootstrapAdminUsername = "bootstrap-admin"
	bootstrapAdminPassword = "bootstrap-admin-pass"
	onboardingBotUsername  = "onboarding"
	onboardingBotPassword  = "onboarding-pass"

	ownerUsername = "owner"
	ownerPassword = "owner-pass"

	mockModel = "mock-model"

	managerReadyTimeout  = 45 * time.Second
	matrixReadyTimeout   = 60 * time.Second
	bootstrapTimeout     = 10 * time.Minute
	roomEventTimeout     = 2 * time.Minute
	scenarioReadyTimeout = 5 * time.Minute

	managerProxyPortRangeStart = 43000
	managerProxyPortRangeEnd   = 43999
)

func e2eProfilingEnabled() bool {
	return strings.TrimSpace(os.Getenv(profileEnvVar)) != ""
}

func TestLiveBootstrapAndOnboardingWorkflow(t *testing.T) {
	requireLiveTests(t)
	if testing.Short() {
		t.Skip("live onboarding tests are not run in short mode")
	}
	testStart := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	stackStart := time.Now()
	stack := startLiveStack(t)
	t.Logf("phase start_live_stack: %s", time.Since(stackStart))
	defer func() {
		if t.Failed() {
			t.Logf("amber-manager logs:\n%s", stack.manager.logs())
			t.Logf("mock responses requests:\n%s", stack.responses.dumpRequests())
			t.Logf("tuwunel logs:\n%s", stack.tuwunel.logs())
		}
		if err := stack.Close(); err != nil {
			t.Fatalf("close live stack: %v", err)
		}
	}()

	cfg := stack.bootstrapConfig()
	bootstrapCtx, bootstrapCancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer bootstrapCancel()

	firstBootstrapStart := time.Now()
	if err := bootstrap.NewRunner(cfg).Run(bootstrapCtx); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	t.Logf("phase first_bootstrap: %s", time.Since(firstBootstrapStart))
	state1, err := bootstrap.LoadState(cfg.StatePath)
	if err != nil {
		t.Fatalf("load first bootstrap state: %v", err)
	}
	if strings.TrimSpace(state1.ProvisionerScenarioID) == "" {
		t.Fatalf("first bootstrap did not persist a provisioner scenario ID: %+v", state1)
	}
	if strings.TrimSpace(state1.OnboardingScenarioID) == "" {
		t.Fatalf("first bootstrap did not persist an onboarding scenario ID: %+v", state1)
	}
	if _, err := stack.managerClient.GetScenario(ctx, state1.ProvisionerScenarioID); err != nil {
		t.Fatalf("load first provisioner scenario %s: %v", state1.ProvisionerScenarioID, err)
	}
	if _, err := stack.managerClient.GetScenario(ctx, state1.OnboardingScenarioID); err != nil {
		t.Fatalf("load first onboarding scenario %s: %v", state1.OnboardingScenarioID, err)
	}
	secondBootstrapStart := time.Now()
	if err := bootstrap.NewRunner(cfg).Run(bootstrapCtx); err != nil {
		t.Fatalf("second bootstrap run: %v", err)
	}
	t.Logf("phase second_bootstrap: %s", time.Since(secondBootstrapStart))
	state2, err := bootstrap.LoadState(cfg.StatePath)
	if err != nil {
		t.Fatalf("load second bootstrap state: %v", err)
	}
	assertBootstrapStateStable(t, state1, state2)

	scenarios := stack.mustListScenarios(ctx)
	assertScenarioCount(t, scenarios, "onboarding-provisioner", 1)
	assertScenarioCount(t, scenarios, "onboarding-agent", 1)
	assertScenarioCount(t, scenarios, "user-agent", 0)

	stack.assertScenarioCreateRejected(ctx, cfg.ProvisionerSourceURL)
	stack.assertScenarioCreateRejected(ctx, cfg.OnboardingSourceURL)

	adminClient := stack.loginClient(t, bootstrapAdminUsername, bootstrapAdminPassword)
	onboardingBotUserID := id.UserID(state2.OnboardingBotUserID)
	welcomeRoomID := id.RoomID(state2.WelcomeRoomID)

	waitForMessageContaining(t, adminClient, welcomeRoomID, onboardingBotUserID, "Click this link to start a DM with the onboarding agent", roomEventTimeout)

	registerOwnerStart := time.Now()
	ownerClient, ownerUserID := stack.registerAndLoginUser(t, ctx, ownerUsername, ownerPassword)
	waitForJoinedMember(t, adminClient, welcomeRoomID, ownerUserID, roomEventTimeout)
	waitForMessageContaining(t, ownerClient, welcomeRoomID, onboardingBotUserID, "https://matrix.to/#/", roomEventTimeout)
	t.Logf("phase register_owner_and_autojoin: %s", time.Since(registerOwnerStart))

	initialDMStart := time.Now()
	dmRoomID := createPrivateRoomAndInvite(t, ownerClient, onboardingBotUserID)
	waitForJoinedMember(t, ownerClient, dmRoomID, onboardingBotUserID, roomEventTimeout)
	waitForExactMessage(t, ownerClient, dmRoomID, onboardingBotUserID, "Welcome. Do you want a new agent?", roomEventTimeout)
	t.Logf("phase create_onboarding_dm: %s", time.Since(initialDMStart))

	provisionStart := time.Now()
	if _, err := ownerClient.SendText(ctx, dmRoomID, "yes"); err != nil {
		t.Fatalf("send onboarding confirmation: %v", err)
	}

	userAgentScenario := waitForSingleUserAgentScenario(t, ctx, stack.managerClient, ownerUserID.String(), "", scenarioReadyTimeout)
	expectedBotUsername := stringValue(userAgentScenario.Metadata["bot_username"])
	if strings.TrimSpace(expectedBotUsername) == "" {
		t.Fatalf("user-agent bot_username metadata missing: %#v", userAgentScenario.Metadata)
	}
	expectedBotUserID := id.UserID(stringValue(userAgentScenario.Metadata["bot_matrix_user_id"]))
	if expectedBotUserID == "" {
		t.Fatalf("user-agent bot_matrix_user_id metadata missing: %#v", userAgentScenario.Metadata)
	}
	createdBotUsername, _ := waitForCreatedCredentialsMessage(
		t,
		ownerClient,
		dmRoomID,
		onboardingBotUserID,
		roomEventTimeout,
	)
	if createdBotUsername != expectedBotUsername {
		t.Fatalf("created bot username in onboarding reply = %q, want %q", createdBotUsername, expectedBotUsername)
	}
	waitForRoomMembersExactly(t, ownerClient, dmRoomID, []id.UserID{ownerUserID, onboardingBotUserID}, roomEventTimeout)

	newBotDMRoomID := waitForInvitedRoom(t, ownerClient, expectedBotUserID, ownerUserID, roomEventTimeout)
	joinRoom(t, ownerClient, newBotDMRoomID)
	waitForJoinedMember(t, ownerClient, newBotDMRoomID, ownerUserID, roomEventTimeout)
	waitForJoinedMember(t, ownerClient, newBotDMRoomID, expectedBotUserID, roomEventTimeout)
	waitForExactMessage(t, ownerClient, newBotDMRoomID, expectedBotUserID, "Hi", roomEventTimeout)
	waitForRoomMembersExactly(t, ownerClient, newBotDMRoomID, []id.UserID{ownerUserID, expectedBotUserID}, roomEventTimeout)
	t.Logf("phase provision_and_child_intro_dm: %s", time.Since(provisionStart))

	repeatGuardStart := time.Now()
	repeatRoomID := createPrivateRoomAndInvite(t, ownerClient, onboardingBotUserID)
	waitForJoinedMember(t, ownerClient, repeatRoomID, onboardingBotUserID, roomEventTimeout)
	waitForExactMessage(t, ownerClient, repeatRoomID, onboardingBotUserID, "Welcome. Do you want a new agent?", roomEventTimeout)
	if _, err := ownerClient.SendText(ctx, repeatRoomID, "I want a new agent"); err != nil {
		t.Fatalf("send duplicate onboarding request: %v", err)
	}
	waitForExactMessage(
		t,
		ownerClient,
		repeatRoomID,
		onboardingBotUserID,
		fmt.Sprintf("You already have a default agent at %s.", expectedBotUsername),
		roomEventTimeout,
	)

	scenarios = stack.mustListScenarios(ctx)
	assertScenarioCount(t, scenarios, "user-agent", 1)
	t.Logf("phase duplicate_guard: %s", time.Since(repeatGuardStart))
	t.Logf("phase total: %s", time.Since(testStart))
}

type liveStack struct {
	manager     *managerProcess
	managerURL  string
	managerData string
	forwarders  *managerforwarders.Monitor

	tuwunel *tuwunelInstance

	responses *mockResponsesServer

	managerClient *managerclient.Client
	httpClient    *http.Client

	bootstrapConfigValue bootstrap.Config
}

type liveStackOptions struct {
	useMockResponses            bool
	authProxySourceInput        string
	codexAuthJSONPath           string
	onboardingModel             string
	onboardingReasoningEffort   string
	defaultAgentModel           string
	defaultAgentReasoningEffort string
	codexConfigTOMLPath         string
}

func startLiveStack(t *testing.T) *liveStack {
	t.Helper()

	mockCodexConfigPath := filepath.Join(t.TempDir(), "mock-codex-config.toml")
	writeMockCodexConfigTOML(t, mockCodexConfigPath)
	return startLiveStackWithOptions(t, liveStackOptions{
		useMockResponses:            true,
		onboardingModel:             mockModel,
		onboardingReasoningEffort:   "low",
		defaultAgentModel:           mockModel,
		defaultAgentReasoningEffort: "low",
		codexConfigTOMLPath:         mockCodexConfigPath,
	})
}

func startRealLiveStack(t *testing.T) *liveStack {
	t.Helper()

	realCodexConfigPath := filepath.Join(t.TempDir(), "real-codex-config.toml")
	writeRealCodexConfigTOML(t, realCodexConfigPath)

	authProxySourceInput := strings.TrimSpace(os.Getenv(authProxySourceEnvVar))
	if authProxySourceInput == "" {
		authProxySourceInput = publishedAuthProxySourceURL
	}

	codexAuthJSONPath := strings.TrimSpace(os.Getenv(codexAuthPathEnvVar))
	if codexAuthJSONPath == "" {
		codexAuthJSONPath = defaultCodexAuthJSONPath(t)
	}

	onboardingModel := strings.TrimSpace(os.Getenv(onboardingModelEnvVar))
	if onboardingModel == "" {
		onboardingModel = "gpt-5.4-mini"
	}
	onboardingReasoningEffort := strings.TrimSpace(os.Getenv(onboardingReasoningEnvVar))
	if onboardingReasoningEffort == "" {
		onboardingReasoningEffort = "medium"
	}
	defaultAgentModel := strings.TrimSpace(os.Getenv(defaultAgentModelEnvVar))
	if defaultAgentModel == "" {
		defaultAgentModel = "gpt-5.4"
	}
	defaultAgentReasoningEffort := strings.TrimSpace(os.Getenv(defaultAgentReasoningEnvVar))
	if defaultAgentReasoningEffort == "" {
		defaultAgentReasoningEffort = "low"
	}

	return startLiveStackWithOptions(t, liveStackOptions{
		authProxySourceInput:        authProxySourceInput,
		codexAuthJSONPath:           codexAuthJSONPath,
		onboardingModel:             onboardingModel,
		onboardingReasoningEffort:   onboardingReasoningEffort,
		defaultAgentModel:           defaultAgentModel,
		defaultAgentReasoningEffort: defaultAgentReasoningEffort,
		codexConfigTOMLPath:         realCodexConfigPath,
	})
}

func startLiveStackWithOptions(t *testing.T, opts liveStackOptions) *liveStack {
	t.Helper()

	ensureCommand(t, "docker")
	ensureCommand(t, "go")
	cleanupOrphanedOnboardingContainers(t)
	cleanupOrphanedAmberNetworks(t)

	stack := &liveStack{}
	started := false
	defer func() {
		if started {
			return
		}
		if err := closeStartupArtifacts(stack); err != nil {
			t.Logf("cleanup partial live stack: %v", err)
		}
	}()

	onboardingRoot := repoRoot(t)
	amberManagerImage := strings.TrimSpace(os.Getenv(managerImageEnvVar))
	if amberManagerImage == "" {
		amberManagerImage = defaultAmberManagerImage
	}

	pullDepsStart := time.Now()
	ensureDockerImage(t, amberManagerImage)
	ensureDockerImage(t, "ghcr.io/rdi-foundation/amber-helper:v0.2")
	ensureDockerImage(t, "ghcr.io/rdi-foundation/amber-router:v0.1")
	ensureDockerImage(t, "ghcr.io/rdi-foundation/amber-provisioner:v0.1")
	ensureDockerImage(t, "ghcr.io/ricelines/matrix-mcp:v0.1")
	ensureDockerImage(t, "ghcr.io/ricelines/matrix-a2a-bridge:v0.1")
	ensureDockerImage(t, "ghcr.io/ricelines/codex-a2a:v0.1")
	t.Logf("phase start_live_stack.pull_dependencies: %s", time.Since(pullDepsStart))

	buildOnboardingStart := time.Now()
	buildDockerImage(t, onboardingRoot, filepath.Join(onboardingRoot, "Dockerfile"), "ghcr.io/ricelines/onboarding:v0.1")
	t.Logf("phase start_live_stack.build_onboarding_image: %s", time.Since(buildOnboardingStart))
	startTuwunelStart := time.Now()
	tuwunel := startTuwunel(t)
	stack.tuwunel = tuwunel
	t.Logf("phase start_live_stack.start_tuwunel: %s", time.Since(startTuwunelStart))

	var responses *mockResponsesServer
	if opts.useMockResponses {
		startResponsesStart := time.Now()
		responses = startMockResponsesServer(t)
		stack.responses = responses
		t.Logf("phase start_live_stack.start_mock_responses: %s", time.Since(startResponsesStart))
	}

	startManagerStart := time.Now()
	managerPort := reservePort(t)
	managerAddr := fmt.Sprintf("127.0.0.1:%d", managerPort)
	managerURL := "http://" + managerAddr

	managerData := t.TempDir()
	configPath := filepath.Join(managerData, "manager-config.json")
	managerSourceDir := filepath.Join(managerData, "manager-sources")
	prepareSourcesStart := time.Now()
	managerSources := prepareManagerSources(
		t,
		managerSourceDir,
		opts.authProxySourceInput,
	)
	t.Logf("phase start_live_stack.prepare_manager_sources: %s", time.Since(prepareSourcesStart))
	dockerSockPath := detectDockerSocketPath(t)
	dockerAPIVersion := detectDockerAPIVersion(t)
	managerCfg := managerConfig{
		MatrixURL:  "http://host.docker.internal:" + fmt.Sprint(tuwunel.hostPort),
		ManagerURL: "http://host.docker.internal:" + fmt.Sprint(managerPort),
		AllowedSourceURLs: []string{
			managerSources.ProvisionerSourceURL,
			managerSources.OnboardingSourceURL,
			managerSources.DefaultAgentSourceURL,
			managerSources.AuthProxySourceURL,
		},
	}
	if responses != nil {
		managerCfg.ResponsesURL = "http://host.docker.internal:" + fmt.Sprint(responses.port())
	}
	writeManagerConfig(t, configPath, managerCfg)

	managerProc := startManagerProcess(
		t,
		amberManagerImage,
		managerData,
		configPath,
		dockerSockPath,
		dockerAPIVersion,
		onboardingRoot,
		managerSourceDir,
		managerPort,
	)
	stack.manager = managerProc
	stack.managerURL = managerURL
	stack.managerData = managerData
	waitForManagerReady(t, managerURL, managerProc)
	forwarderMonitor, err := managerforwarders.Start(context.Background(), managerforwarders.Config{
		ManagerContainerName: managerProc.containerName,
		ForwarderImage:       "ghcr.io/ricelines/onboarding:v0.1",
		ForwarderNamePrefix:  fmt.Sprintf("onboarding-manager-forwarder-%d", time.Now().UnixNano()),
		PollInterval:         200 * time.Millisecond,
		Logger:               t.Logf,
	})
	if err != nil {
		t.Fatalf("start manager forwarders: %v", err)
	}
	stack.forwarders = forwarderMonitor
	t.Logf("phase start_live_stack.start_manager: %s", time.Since(startManagerStart))

	stack.managerClient = managerclient.NewClient(managerURL)
	stack.httpClient = &http.Client{Timeout: 30 * time.Second}
	stack.bootstrapConfigValue = bootstrap.Config{
		StatePath:                           filepath.Join(t.TempDir(), "bootstrap-state.json"),
		MatrixHomeserverURL:                 tuwunel.baseURL(),
		MatrixServerName:                    serverName,
		RegistrationToken:                   registrationToken,
		ManagerURL:                          managerURL,
		MatrixBindableServiceName:           "matrix",
		ManagerBindableServiceName:          "amber-manager-api",
		SharedResponsesBindableServiceName:  "responses-api",
		BootstrapAdminUsername:              bootstrapAdminUsername,
		BootstrapAdminPassword:              bootstrapAdminPassword,
		OnboardingBotUsername:               onboardingBotUsername,
		OnboardingBotPassword:               onboardingBotPassword,
		WelcomeRoomAliasLocalpart:           "welcome",
		ProvisionerSourceURL:                managerSources.ProvisionerSourceURL,
		OnboardingSourceURL:                 managerSources.OnboardingSourceURL,
		DefaultAgentSourceURL:               managerSources.DefaultAgentSourceURL,
		AuthProxySourceURL:                  managerSources.AuthProxySourceURL,
		CodexAuthJSONPath:                   opts.codexAuthJSONPath,
		OnboardingModel:                     opts.onboardingModel,
		OnboardingModelReasoningEffort:      opts.onboardingReasoningEffort,
		DefaultAgentModel:                   opts.defaultAgentModel,
		DefaultAgentModelReasoningEffort:    opts.defaultAgentReasoningEffort,
		OnboardingDeveloperInstructionsPath: filepath.Join(onboardingRoot, "prompts", "onboarding-developer-instructions.md"),
		OnboardingConfigTOMLPath:            opts.codexConfigTOMLPath,
		DefaultAgentDeveloperInstructionsPath: filepath.Join(
			onboardingRoot,
			"prompts",
			"default-user-agent-developer-instructions.md",
		),
		DefaultAgentAgentsPath:     filepath.Join(onboardingRoot, "agents", "default-user-agent.md"),
		DefaultAgentConfigTOMLPath: opts.codexConfigTOMLPath,
	}
	started = true
	return stack
}

func closeStartupArtifacts(s *liveStack) error {
	var errs []error

	if s == nil {
		return nil
	}
	if s.forwarders != nil {
		if err := s.forwarders.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.manager != nil {
		if err := s.manager.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.responses != nil {
		s.responses.Close()
	}
	if s.tuwunel != nil {
		if err := s.tuwunel.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *liveStack) Close() error {
	var errs []error

	if s.manager != nil {
		if err := s.cleanupScenarios(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.forwarders != nil {
		if err := s.forwarders.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.manager != nil {
		if err := s.manager.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.responses != nil {
		s.responses.Close()
	}
	if s.tuwunel != nil {
		if err := s.tuwunel.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (s *liveStack) cleanupScenarios() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scenarios, err := s.managerClient.ListScenarios(ctx)
	if err != nil {
		return fmt.Errorf("list scenarios for cleanup: %w", err)
	}
	slices.SortFunc(scenarios, func(a, b managerclient.ScenarioSummaryResponse) int {
		return cleanupPriority(a) - cleanupPriority(b)
	})
	for _, scenario := range scenarios {
		op, err := deleteScenario(ctx, s.httpClient, s.managerURL, scenario.ScenarioID, true)
		if err != nil {
			return fmt.Errorf("delete scenario %s: %w", scenario.ScenarioID, err)
		}
		if err := waitForOperationSucceeded(ctx, s.managerClient, op.OperationID); err != nil {
			return fmt.Errorf("wait for delete scenario %s: %w", scenario.ScenarioID, err)
		}
	}
	return nil
}

func cleanupPriority(scenario managerclient.ScenarioSummaryResponse) int {
	switch stringValue(scenario.Metadata["kind"]) {
	case "user-agent":
		return 0
	case "onboarding-agent":
		return 1
	case "onboarding-provisioner":
		return 2
	case "onboarding-auth-proxy":
		return 3
	default:
		return 10
	}
}

func (s *liveStack) bootstrapConfig() bootstrap.Config {
	return s.bootstrapConfigValue
}

func (s *liveStack) loginClient(t *testing.T, username, password string) *mautrix.Client {
	t.Helper()
	client, err := loginClient(s.tuwunel.baseURL(), username, password)
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return client
}

func (s *liveStack) registerAndLoginUser(t *testing.T, ctx context.Context, username, password string) (*mautrix.Client, id.UserID) {
	t.Helper()

	matrixAPI := matrixclient.NewClient(s.tuwunel.baseURL(), registrationToken)
	created, err := matrixAPI.CreateUser(ctx, username, password)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	client := s.loginClient(t, username, password)
	return client, id.UserID(created.UserID)
}

func (s *liveStack) mustListScenarios(ctx context.Context) []managerclient.ScenarioSummaryResponse {
	scenarios, err := s.managerClient.ListScenarios(ctx)
	if err != nil {
		panic(fmt.Sprintf("list scenarios: %v", err))
	}
	return scenarios
}

func (s *liveStack) assertScenarioCreateRejected(ctx context.Context, sourceURL string) {
	_, err := s.managerClient.CreateScenario(ctx, managerclient.CreateScenarioRequest{
		SourceURL:     sourceURL,
		RootConfig:    map[string]any{},
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{},
		Metadata:      map[string]any{"kind": "should-fail"},
		StoreBundle:   true,
		Start:         true,
	})
	if err == nil {
		panic(fmt.Sprintf("create scenario unexpectedly succeeded for revoked source %s", sourceURL))
	}
}

type managerProcess struct {
	containerName string
}

func startManagerProcess(
	t *testing.T,
	image string,
	dataDir string,
	configPath string,
	dockerSockPath string,
	dockerAPIVersion string,
	onboardingRoot string,
	managerSourceDir string,
	listenPort int,
) *managerProcess {
	t.Helper()

	containerName := fmt.Sprintf("onboarding-manager-%d", time.Now().UnixNano())
	args := []string{
		"run", "--rm", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:4100", listenPort),
		"-p", fmt.Sprintf("127.0.0.1:%d-%d:%d-%d", managerProxyPortRangeStart, managerProxyPortRangeEnd, managerProxyPortRangeStart, managerProxyPortRangeEnd),
		"--sysctl", fmt.Sprintf("net.ipv4.ip_local_port_range=%d %d", managerProxyPortRangeStart, managerProxyPortRangeEnd),
		"-e", "RUST_LOG=info",
		"-e", "DOCKER_HOST=unix:///var/run/docker.sock",
		"-e", "DOCKER_API_VERSION=" + dockerAPIVersion,
		"-e", "AMBER_DOCKER_SOCK=" + dockerSockPath,
		"-v", fmt.Sprintf("%s:/var/lib/amber-manager", dataDir),
		"-v", fmt.Sprintf("%s:/etc/amber-manager/manager-config.json:ro", configPath),
		"-v", fmt.Sprintf("%s:%s", dockerSockPath, "/var/run/docker.sock"),
		"-v", fmt.Sprintf("%s:/opt/onboarding:ro", onboardingRoot),
	}
	if runtime.GOOS == "linux" {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	if dirHasEntries(t, managerSourceDir) {
		args = append(args, "-v", fmt.Sprintf("%s:/opt/onboarding-sources:ro", managerSourceDir))
	}
	args = append(
		args,
		image,
		"--listen", "0.0.0.0:4100",
		"--data-dir", "/var/lib/amber-manager",
		"--config", "/etc/amber-manager/manager-config.json",
	)
	if output, err := runCommand(60*time.Second, "docker", args...); err != nil {
		t.Fatalf("start amber-manager: %v\n%s", err, output)
	}
	return &managerProcess{containerName: containerName}
}

func (p *managerProcess) logs() string {
	if p == nil {
		return ""
	}
	output, _ := runCommand(20*time.Second, "docker", "logs", p.containerName)
	return output
}

func (p *managerProcess) assertAlive(context string) error {
	if p == nil {
		return errors.New("manager process is nil")
	}
	output, err := runCommand(20*time.Second, "docker", "inspect", "-f", "{{.State.Running}}", p.containerName)
	if err != nil {
		return fmt.Errorf("inspect amber-manager while %s: %w\n%s", context, err, p.logs())
	}
	if strings.TrimSpace(output) != "true" {
		return fmt.Errorf("amber-manager exited while %s\n%s", context, p.logs())
	}
	return nil
}

func (p *managerProcess) Close() error {
	if p == nil || p.containerName == "" {
		return nil
	}
	output, err := runCommand(30*time.Second, "docker", "stop", "-t", "1", p.containerName)
	if err != nil && !strings.Contains(output, "No such container") {
		return fmt.Errorf("stop amber-manager: %v\n%s", err, output)
	}
	return nil
}

type tuwunelInstance struct {
	containerName string
	baseDir       string
	hostPort      int
}

func startTuwunel(t *testing.T) *tuwunelInstance {
	t.Helper()

	baseDir := t.TempDir()
	configPath := filepath.Join(baseDir, "tuwunel.toml")
	if err := os.WriteFile(configPath, []byte(tuwunelConfig()), 0o644); err != nil {
		t.Fatalf("write tuwunel config: %v", err)
	}

	hostPort := reservePort(t)
	containerName := fmt.Sprintf("onboarding-e2e-%d", time.Now().UnixNano())
	cmd := exec.Command(
		"docker", "run", "--rm", "-d",
		"--name", containerName,
		"-e", "TUWUNEL_CONFIG=/data/tuwunel.toml",
		"-v", fmt.Sprintf("%s:/data", baseDir),
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, tuwunelPort),
		tuwunelImage,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start tuwunel: %v\n%s", err, output)
	}

	inst := &tuwunelInstance{
		containerName: containerName,
		baseDir:       baseDir,
		hostPort:      hostPort,
	}
	waitForMatrixReady(t, inst.baseURL())
	return inst
}

func (t *tuwunelInstance) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", t.hostPort)
}

func (t *tuwunelInstance) logs() string {
	output, _ := runCommand(20*time.Second, "docker", "logs", t.containerName)
	return output
}

func (t *tuwunelInstance) Close() error {
	if t == nil || t.containerName == "" {
		return nil
	}
	output, err := runCommand(30*time.Second, "docker", "stop", "-t", "1", t.containerName)
	if err != nil && !strings.Contains(output, "No such container") {
		return fmt.Errorf("stop tuwunel: %v\n%s", err, output)
	}
	return nil
}

type managerConfig struct {
	MatrixURL         string
	ManagerURL        string
	ResponsesURL      string
	AllowedSourceURLs []string
}

type managerSources struct {
	ProvisionerSourceURL  string
	OnboardingSourceURL   string
	DefaultAgentSourceURL string
	AuthProxySourceURL    string
}

func writeManagerConfig(t *testing.T, path string, cfg managerConfig) {
	t.Helper()

	bindableServices := map[string]any{
		"matrix": map[string]any{
			"protocol": "http",
			"provider": map[string]any{
				"kind": "direct_url",
				"url":  cfg.MatrixURL,
			},
		},
		"amber-manager-api": map[string]any{
			"protocol": "http",
			"provider": map[string]any{
				"kind": "direct_url",
				"url":  cfg.ManagerURL,
			},
		},
	}
	if strings.TrimSpace(cfg.ResponsesURL) != "" {
		bindableServices["responses-api"] = map[string]any{
			"protocol": "http",
			"provider": map[string]any{
				"kind": "direct_url",
				"url":  cfg.ResponsesURL,
			},
		}
	}
	allowlist := make([]string, 0, len(cfg.AllowedSourceURLs))
	for _, sourceURL := range cfg.AllowedSourceURLs {
		sourceURL = strings.TrimSpace(sourceURL)
		if sourceURL != "" {
			allowlist = append(allowlist, sourceURL)
		}
	}
	payload := map[string]any{
		"bindable_services":         bindableServices,
		"scenario_source_allowlist": allowlist,
	}
	blob, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal manager config: %v", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
		t.Fatalf("write manager config: %v", err)
	}
}

func prepareManagerSources(
	t *testing.T,
	managerSourceDir string,
	authProxySourceInput string,
) managerSources {
	t.Helper()

	sources := managerSources{
		ProvisionerSourceURL:  "file:///opt/onboarding/amber/agent-provisioner.json5",
		OnboardingSourceURL:   "file:///opt/onboarding/amber/onboarding-agent.json5",
		DefaultAgentSourceURL: publishedUserAgentSourceURL,
	}
	authProxySourceInput = strings.TrimSpace(authProxySourceInput)
	if authProxySourceInput != "" {
		sources.AuthProxySourceURL = normalizeManagerSourceURL(t, authProxySourceInput, managerSourceDir, "codex-auth-proxy.json5")
	}
	return sources
}

func sourceURLToLocalPath(t *testing.T, source string) string {
	t.Helper()
	if strings.HasPrefix(source, "file://") {
		return strings.TrimPrefix(source, "file://")
	}
	absPath, err := filepath.Abs(source)
	if err != nil {
		t.Fatalf("filepath.Abs(%s): %v", source, err)
	}
	return absPath
}

func normalizeManagerSourceURL(t *testing.T, rawSourceURL string, managerSourceDir string, stagedName string) string {
	t.Helper()
	if strings.HasPrefix(rawSourceURL, "http://") || strings.HasPrefix(rawSourceURL, "https://") {
		return rawSourceURL
	}
	localPath := sourceURLToLocalPath(t, rawSourceURL)
	stagedDir := filepath.Join(managerSourceDir, "external")
	if err := os.MkdirAll(stagedDir, 0o755); err != nil {
		t.Fatalf("mkdir manager source dir: %v", err)
	}
	stagedPath := filepath.Join(stagedDir, stagedName)
	contents, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read manager source %s: %v", localPath, err)
	}
	if err := os.WriteFile(stagedPath, contents, 0o644); err != nil {
		t.Fatalf("write staged manager source %s: %v", stagedPath, err)
	}
	return "file:///opt/onboarding-sources/external/" + stagedName
}

func writeMockCodexConfigTOML(t *testing.T, path string) {
	t.Helper()

	const config = `model = "mock-model"
approval_policy = "never"
sandbox_mode = "read-only"
enable_request_compression = false

[features]
child_agents_md = true
`
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatalf("write mock codex config: %v", err)
	}
}

func writeRealCodexConfigTOML(t *testing.T, path string) {
	t.Helper()

	const config = `[features]
child_agents_md = true
`
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatalf("write real codex config: %v", err)
	}
}

func waitForManagerReady(t *testing.T, baseURL string, proc *managerProcess) {
	t.Helper()

	deadline := time.Now().Add(managerReadyTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		if err := proc.assertAlive("waiting for readiness"); err != nil {
			t.Fatal(err)
		}
		resp, err := client.Get(baseURL + "/readyz")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"ready":true`) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for amber-manager readiness\n%s", proc.logs())
}

func waitForMatrixReady(t *testing.T, homeserverURL string) {
	t.Helper()

	deadline := time.Now().Add(matrixReadyTimeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(homeserverURL + "/_matrix/client/versions")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for matrix homeserver at %s", homeserverURL)
}

func createPrivateRoomAndInvite(t *testing.T, client *mautrix.Client, invitee id.UserID) id.RoomID {
	t.Helper()

	room, err := client.CreateRoom(context.Background(), &mautrix.ReqCreateRoom{
		Preset:   "private_chat",
		IsDirect: false,
	})
	if err != nil {
		t.Fatalf("create private room: %v", err)
	}
	if _, err := client.InviteUser(context.Background(), room.RoomID, &mautrix.ReqInviteUser{UserID: invitee}); err != nil {
		t.Fatalf("invite %s to room %s: %v", invitee, room.RoomID, err)
	}
	return room.RoomID
}

func joinRoom(t *testing.T, client *mautrix.Client, roomID id.RoomID) {
	t.Helper()

	deadline := time.Now().Add(roomEventTimeout)
	for time.Now().Before(deadline) {
		if _, err := client.JoinRoom(context.Background(), roomID.String(), &mautrix.ReqJoinRoom{}); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out joining invited room %s", roomID)
}

func waitForSingleUserAgentScenario(
	t *testing.T,
	ctx context.Context,
	manager *managerclient.Client,
	ownerMatrixUserID string,
	botUsername string,
	timeout time.Duration,
) managerclient.ScenarioSummaryResponse {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		scenarios, err := manager.ListScenarios(waitCtx)
		if err == nil {
			var matches []managerclient.ScenarioSummaryResponse
			for _, scenario := range scenarios {
				if stringValue(scenario.Metadata["kind"]) != "user-agent" {
					continue
				}
				if stringValue(scenario.Metadata["owner_matrix_user_id"]) != ownerMatrixUserID {
					continue
				}
				if strings.TrimSpace(botUsername) != "" && stringValue(scenario.Metadata["bot_username"]) != botUsername {
					continue
				}
				matches = append(matches, scenario)
			}
			if len(matches) == 1 {
				detail, err := manager.GetScenario(waitCtx, matches[0].ScenarioID)
				if err == nil {
					switch detail.ObservedState {
					case "running":
						return matches[0]
					case "failed":
						if strings.TrimSpace(detail.LastError) != "" {
							t.Fatalf("user-agent scenario %s failed: %s", matches[0].ScenarioID, detail.LastError)
						}
						t.Fatalf("user-agent scenario %s failed", matches[0].ScenarioID)
					}
				}
			}
		}

		select {
		case <-waitCtx.Done():
			t.Fatalf("timed out waiting for user-agent scenario for %s/%s", ownerMatrixUserID, botUsername)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func waitForJoinedMember(t *testing.T, client *mautrix.Client, roomID id.RoomID, userID id.UserID, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		members, err := client.JoinedMembers(context.Background(), roomID)
		if err == nil {
			if _, ok := members.Joined[userID]; ok {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to join %s", userID, roomID)
}

func waitForInvitedRoom(
	t *testing.T,
	client *mautrix.Client,
	inviter id.UserID,
	invitee id.UserID,
	timeout time.Duration,
) id.RoomID {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.SyncRequest(context.Background(), 0, "", "", true, "")
		if err == nil {
			for roomID, room := range resp.Rooms.Invite {
				if room == nil {
					continue
				}
				if hasInviteFrom(room.State.Events, inviter, invitee) {
					return roomID
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for invite from %s to %s", inviter, invitee)
	return ""
}

func waitForRoomMembersExactly(t *testing.T, client *mautrix.Client, roomID id.RoomID, want []id.UserID, timeout time.Duration) {
	t.Helper()

	wantStrings := make([]string, 0, len(want))
	for _, member := range want {
		wantStrings = append(wantStrings, member.String())
	}
	slices.Sort(wantStrings)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		members, err := client.JoinedMembers(context.Background(), roomID)
		if err == nil {
			got := make([]string, 0, len(members.Joined))
			for userID := range members.Joined {
				got = append(got, userID.String())
			}
			slices.Sort(got)
			if slices.Equal(got, wantStrings) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for joined members %#v in room %s", wantStrings, roomID)
}

func waitForExactMessage(t *testing.T, client *mautrix.Client, roomID id.RoomID, sender id.UserID, body string, timeout time.Duration) {
	t.Helper()
	waitForMessage(t, client, roomID, sender, timeout, func(text string) bool { return text == body }, "exact", body)
}

func waitForCreatedCredentialsMessage(t *testing.T, client *mautrix.Client, roomID id.RoomID, sender id.UserID, timeout time.Duration) (string, string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.SyncRequest(context.Background(), 0, "", "", true, "")
		if err == nil {
			room, ok := resp.Rooms.Join[roomID]
			if ok {
				for _, evt := range room.Timeline.Events {
					if evt == nil || evt.Type != event.EventMessage || evt.Sender != sender {
						continue
					}
					username, password, ok := parseCreatedCredentialsMessage(eventMessageBody(evt))
					if ok {
						return username, password
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for created-credentials message from %s in room %s", sender, roomID)
	return "", ""
}

func parseCreatedCredentialsMessage(body string) (string, string, bool) {
	const prefix = "Created "
	const middle = " with password "
	const suffix = "."

	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, prefix) || !strings.HasSuffix(body, suffix) {
		return "", "", false
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(body, prefix), suffix)
	parts := strings.SplitN(trimmed, middle, 2)
	if len(parts) != 2 {
		return "", "", false
	}
	username := strings.TrimSpace(parts[0])
	password := strings.TrimSpace(parts[1])
	if username == "" || password == "" {
		return "", "", false
	}
	return username, password, true
}

func waitForMessageContaining(t *testing.T, client *mautrix.Client, roomID id.RoomID, sender id.UserID, substring string, timeout time.Duration) {
	t.Helper()
	waitForMessage(t, client, roomID, sender, timeout, func(text string) bool { return strings.Contains(text, substring) }, "substring", substring)
}

func waitForMessage(
	t *testing.T,
	client *mautrix.Client,
	roomID id.RoomID,
	sender id.UserID,
	timeout time.Duration,
	match func(string) bool,
	label string,
	want string,
) {
	t.Helper()

	_ = waitForMessageBody(t, client, roomID, sender, timeout, match, label, want)
}

func waitForMessageBody(
	t *testing.T,
	client *mautrix.Client,
	roomID id.RoomID,
	sender id.UserID,
	timeout time.Duration,
	match func(string) bool,
	label string,
	want string,
) string {
	t.Helper()

	body, err := waitForMessageBodyUntil(client, roomID, sender, timeout, match, label, want)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func waitForMessageBodyUntil(
	client *mautrix.Client,
	roomID id.RoomID,
	sender id.UserID,
	timeout time.Duration,
	match func(string) bool,
	label string,
	want string,
) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.SyncRequest(context.Background(), 0, "", "", true, "")
		if err == nil {
			room, ok := resp.Rooms.Join[roomID]
			if ok {
				for _, evt := range room.Timeline.Events {
					if evt == nil || evt.Type != event.EventMessage || evt.Sender != sender {
						continue
					}
					body := eventMessageBody(evt)
					if match(body) {
						return body, nil
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for message with %s %q from %s in room %s", label, want, sender, roomID)
}

func collectMessageBodies(client *mautrix.Client, roomID id.RoomID, sender id.UserID) ([]string, error) {
	resp, err := client.SyncRequest(context.Background(), 0, "", "", true, "")
	if err != nil {
		return nil, err
	}
	room, ok := resp.Rooms.Join[roomID]
	if !ok {
		return nil, fmt.Errorf("room %s not found in sync response", roomID)
	}
	messages := make([]string, 0, len(room.Timeline.Events))
	for _, evt := range room.Timeline.Events {
		if evt == nil || evt.Type != event.EventMessage {
			continue
		}
		if sender != "" && evt.Sender != sender {
			continue
		}
		messages = append(messages, eventMessageBody(evt))
	}
	return messages, nil
}

func eventMessageBody(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	if err := evt.Content.ParseRaw(evt.Type); err == nil || errors.Is(err, event.ErrContentAlreadyParsed) {
		return evt.Content.AsMessage().Body
	}
	body, _ := evt.Content.Raw["body"].(string)
	return body
}

func hasInviteFrom(events []*event.Event, inviter id.UserID, invitee id.UserID) bool {
	for _, evt := range events {
		if evt == nil || evt.Type != event.StateMember || evt.Sender != inviter || evt.GetStateKey() != invitee.String() {
			continue
		}
		if membership, _ := evt.Content.Raw["membership"].(string); membership == "invite" {
			return true
		}
	}
	return false
}

func assertBootstrapStateStable(t *testing.T, before, after bootstrap.State) {
	t.Helper()
	if before.BootstrapAdminUserID != after.BootstrapAdminUserID {
		t.Fatalf("bootstrap admin changed: %q -> %q", before.BootstrapAdminUserID, after.BootstrapAdminUserID)
	}
	if before.OnboardingBotUserID != after.OnboardingBotUserID {
		t.Fatalf("onboarding bot changed: %q -> %q", before.OnboardingBotUserID, after.OnboardingBotUserID)
	}
	if before.WelcomeRoomID != after.WelcomeRoomID {
		t.Fatalf("welcome room changed: %q -> %q", before.WelcomeRoomID, after.WelcomeRoomID)
	}
	if before.SharedResponsesBindableServiceID != after.SharedResponsesBindableServiceID {
		t.Fatalf("shared responses service changed: %q -> %q", before.SharedResponsesBindableServiceID, after.SharedResponsesBindableServiceID)
	}
	if before.ProvisionerScenarioID != after.ProvisionerScenarioID {
		t.Fatalf("provisioner scenario changed: %q -> %q", before.ProvisionerScenarioID, after.ProvisionerScenarioID)
	}
	if before.OnboardingScenarioID != after.OnboardingScenarioID {
		t.Fatalf("onboarding scenario changed: %q -> %q", before.OnboardingScenarioID, after.OnboardingScenarioID)
	}
}

func assertScenarioCount(t *testing.T, scenarios []managerclient.ScenarioSummaryResponse, kind string, want int) {
	t.Helper()
	got := 0
	for _, scenario := range scenarios {
		if stringValue(scenario.Metadata["kind"]) == kind {
			got++
		}
	}
	if got != want {
		t.Fatalf("scenario count for kind %q = %d, want %d", kind, got, want)
	}
}

func waitForOperationSucceeded(ctx context.Context, manager *managerclient.Client, operationID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, scenarioReadyTimeout)
	defer cancel()

	for {
		status, err := manager.GetOperation(waitCtx, operationID)
		if err != nil {
			return err
		}
		switch status.Status {
		case "succeeded":
			return nil
		case "failed":
			if status.LastError != "" {
				return fmt.Errorf("operation %s failed: %s", operationID, status.LastError)
			}
			return fmt.Errorf("operation %s failed", operationID)
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for operation %s", operationID)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func deleteScenario(ctx context.Context, client *http.Client, managerURL, scenarioID string, destroyStorage bool) (managerclient.EnqueueOperationResponse, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("%s/v1/scenarios/%s?destroy_storage=%t", managerURL, scenarioID, destroyStorage),
		nil,
	)
	if err != nil {
		return managerclient.EnqueueOperationResponse{}, fmt.Errorf("build delete scenario request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return managerclient.EnqueueOperationResponse{}, fmt.Errorf("perform delete scenario request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return managerclient.EnqueueOperationResponse{}, fmt.Errorf("read delete scenario response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return managerclient.EnqueueOperationResponse{}, fmt.Errorf("delete scenario %s returned %s: %s", scenarioID, resp.Status, strings.TrimSpace(string(body)))
	}

	var result managerclient.EnqueueOperationResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return managerclient.EnqueueOperationResponse{}, fmt.Errorf("decode delete scenario response: %w", err)
	}
	return result, nil
}

func buildDockerImage(t *testing.T, contextDir, dockerfile, tag string) {
	t.Helper()

	cmd := exec.Command("docker", "build", "-t", tag, "-f", dockerfile, contextDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build docker image %s: %v\n%s", tag, err, output)
	}
}

func ensureDockerImage(t *testing.T, tag string) {
	t.Helper()

	if output, err := runCommand(20*time.Second, "docker", "image", "inspect", tag); err == nil && strings.TrimSpace(output) != "" {
		return
	}
	cmd := exec.Command("docker", "pull", tag)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pull docker image %s: %v\n%s", tag, err, output)
	}
}

func cleanupOrphanedAmberNetworks(t *testing.T) {
	t.Helper()

	output, err := runCommand(30*time.Second, "docker", "network", "ls", "--format", "{{.Name}}")
	if err != nil {
		t.Fatalf("list docker networks: %v\n%s", err, output)
	}

	var removed []string
	for _, name := range strings.Split(output, "\n") {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "amber_scn_") {
			continue
		}

		inspectOutput, err := runCommand(20*time.Second, "docker", "network", "inspect", name, "--format", "{{len .Containers}}")
		if err != nil {
			t.Logf("skip docker network %s during inspect: %v\n%s", name, err, inspectOutput)
			continue
		}
		if strings.TrimSpace(inspectOutput) != "0" {
			continue
		}

		if removeOutput, err := runCommand(20*time.Second, "docker", "network", "rm", name); err != nil {
			t.Logf("skip docker network %s during remove: %v\n%s", name, err, removeOutput)
			continue
		}
		removed = append(removed, name)
	}

	if len(removed) > 0 {
		t.Logf("cleaned %d orphaned amber docker networks", len(removed))
	}
}

func cleanupOrphanedOnboardingContainers(t *testing.T) {
	t.Helper()

	output, err := runCommand(30*time.Second, "docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		t.Fatalf("list onboarding test containers: %v\n%s", err, output)
	}

	prefixes := []string{
		"onboarding-manager-",
		"onboarding-e2e-",
		"onboarding-manager-forwarder-",
	}
	var removed []string
	for _, name := range strings.Split(output, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		matched := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		stopOutput, err := runCommand(20*time.Second, "docker", "stop", "-t", "1", name)
		if err != nil && !strings.Contains(stopOutput, "No such container") {
			t.Logf("skip docker container %s during stop: %v\n%s", name, err, stopOutput)
			continue
		}
		removed = append(removed, name)
	}

	if len(removed) > 0 {
		t.Logf("cleaned %d orphaned onboarding docker containers", len(removed))
	}
}

func ensureCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is required for live onboarding tests", name)
	}
}

func requireLiveTests(t *testing.T) {
	t.Helper()
	if os.Getenv(liveEnvVar) == "" {
		t.Skipf("set %s=1 to run live onboarding tests", liveEnvVar)
	}
}

func requireLiveRealTests(t *testing.T) {
	t.Helper()
	if os.Getenv(liveRealEnvVar) == "" {
		t.Skipf("set %s=1 to run live onboarding tests against real Codex", liveRealEnvVar)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	if filepath.Base(root) != "onboarding" {
		t.Fatalf("unexpected onboarding repo root %s", root)
	}
	return root
}

func reservePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener addr type %T", listener.Addr())
	}
	return addr.Port
}

func dirHasEntries(t *testing.T, path string) bool {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		t.Fatalf("read dir %s: %v", path, err)
	}
	return len(entries) > 0
}

func detectDockerSocketPath(t *testing.T) string {
	t.Helper()

	// Docker Desktop on macOS exposes the host CLI through a per-user socket,
	// but nested containers still need /var/run/docker.sock as the bind source.
	if runtime.GOOS == "darwin" {
		return "/var/run/docker.sock"
	}
	if raw := strings.TrimSpace(os.Getenv("DOCKER_HOST")); strings.HasPrefix(raw, "unix://") {
		path := strings.TrimPrefix(raw, "unix://")
		if socketExists(path) {
			return path
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".docker", "run", "docker.sock")
		if socketExists(path) {
			return path
		}
	}
	if socketExists("/var/run/docker.sock") {
		return "/var/run/docker.sock"
	}
	t.Fatal("failed to detect a reachable Docker socket from DOCKER_HOST, ~/.docker/run/docker.sock, or /var/run/docker.sock")
	return ""
}

func detectDockerAPIVersion(t *testing.T) string {
	t.Helper()

	output, err := runCommand(20*time.Second, "docker", "version", "--format", "{{.Server.APIVersion}}")
	if err != nil {
		t.Fatalf("detect docker API version: %v\n%s", err, output)
	}
	version := strings.TrimSpace(output)
	if version == "" {
		t.Fatal("docker server API version was empty")
	}
	return version
}

func defaultCodexAuthJSONPath(t *testing.T) string {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("determine home dir: %v", err)
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

func loginClient(homeserverURL, username, password string) (*mautrix.Client, error) {
	client, err := mautrix.NewClient(homeserverURL, "", "")
	if err != nil {
		return nil, fmt.Errorf("new matrix client: %w", err)
	}
	client.DefaultHTTPRetries = 3
	client.DefaultHTTPBackoff = 500 * time.Millisecond

	deadline := time.Now().Add(15 * time.Second)
	for {
		_, err = client.Login(context.Background(), &mautrix.ReqLogin{
			Type: mautrix.AuthTypePassword,
			Identifier: mautrix.UserIdentifier{
				Type: mautrix.IdentifierTypeUser,
				User: username,
			},
			Password:         password,
			StoreCredentials: true,
		})
		if err == nil {
			return client, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("matrix login for %s: %w", username, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func tuwunelConfig() string {
	return strings.TrimSpace(fmt.Sprintf(`
[global]
server_name = %q
database_path = "/data/database"
address = "0.0.0.0"
port = %d
new_user_displayname_suffix = ""
allow_registration = true
registration_token = %q
allow_guest_registration = false
allow_room_creation = true
lockdown_public_room_directory = false
allow_unlisted_room_search_by_id = false
allow_public_room_directory_without_auth = false
allow_encryption = true
encryption_enabled_by_default_for_room_type = "all"
allow_federation = false
federate_created_rooms = false
grant_admin_to_first_user = false
create_admin_room = false
allow_legacy_media = true
error_on_unknown_config_opts = true
query_trusted_key_servers_first = false
query_trusted_key_servers_first_on_join = false
trusted_servers = []
auto_join_rooms = ["#welcome:%s"]

[global.well_known]
client = "http://127.0.0.1:%d"
server = "%s:443"
`, serverName, tuwunelPort, registrationToken, serverName, tuwunelPort, serverName))
}

func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}
