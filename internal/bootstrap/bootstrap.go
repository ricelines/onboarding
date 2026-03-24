package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"github.com/ricelines/chat/onboarding/internal/matrix"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	waitPollInterval      = 200 * time.Millisecond
	waitTimeout           = 5 * time.Minute
	provisionerListenAddr = ":8080"

	metadataKindAuthProxy   = "onboarding-auth-proxy"
	metadataKindProvisioner = "onboarding-provisioner"
	metadataKindOnboarding  = "onboarding-agent"
)

type Runner struct {
	cfg     Config
	matrix  *matrix.Client
	manager *managerclient.Client
}

func NewRunner(cfg Config) *Runner {
	return &Runner{
		cfg:     cfg,
		matrix:  matrix.NewClient(cfg.MatrixHomeserverURL, cfg.RegistrationToken),
		manager: managerclient.NewClient(cfg.ManagerURL),
	}
}

func (r *Runner) Run(ctx context.Context) error {
	state, err := LoadState(r.cfg.StatePath)
	if err != nil {
		return err
	}
	save := func() error {
		return SaveState(r.cfg.StatePath, state)
	}

	adminUserID, err := r.ensureUser(ctx, r.cfg.BootstrapAdminUsername, r.cfg.BootstrapAdminPassword)
	if err != nil {
		return fmt.Errorf("ensure bootstrap admin: %w", err)
	}
	state.BootstrapAdminUserID = adminUserID
	if err := save(); err != nil {
		return err
	}

	adminClient, err := loginClient(r.cfg.MatrixHomeserverURL, r.cfg.BootstrapAdminUsername, r.cfg.BootstrapAdminPassword)
	if err != nil {
		return fmt.Errorf("login bootstrap admin: %w", err)
	}

	welcomeRoomID, err := r.ensureWelcomeRoom(ctx, adminClient, state)
	if err != nil {
		return fmt.Errorf("ensure welcome room: %w", err)
	}
	state.WelcomeRoomID = welcomeRoomID.String()
	if err := save(); err != nil {
		return err
	}

	onboardingUserID, err := r.ensureUser(ctx, r.cfg.OnboardingBotUsername, r.cfg.OnboardingBotPassword)
	if err != nil {
		return fmt.Errorf("ensure onboarding bot: %w", err)
	}
	state.OnboardingBotUserID = onboardingUserID
	if err := save(); err != nil {
		return err
	}

	onboardingClient, err := loginClient(r.cfg.MatrixHomeserverURL, r.cfg.OnboardingBotUsername, r.cfg.OnboardingBotPassword)
	if err != nil {
		return fmt.Errorf("login onboarding bot: %w", err)
	}
	if err := waitForJoinedMember(ctx, adminClient, welcomeRoomID, id.UserID(onboardingUserID), waitTimeout); err != nil {
		return fmt.Errorf("wait for onboarding bot auto-join: %w", err)
	}
	if err := r.ensureWelcomeMessage(ctx, onboardingClient, welcomeRoomID, id.UserID(onboardingUserID)); err != nil {
		return fmt.Errorf("ensure welcome message: %w", err)
	}

	sharedResponsesID, authProxyScenarioID, err := r.ensureSharedResponsesService(ctx, state.AuthProxyScenarioID)
	if err != nil {
		return fmt.Errorf("ensure shared responses service: %w", err)
	}
	state.SharedResponsesBindableServiceID = sharedResponsesID
	state.AuthProxyScenarioID = authProxyScenarioID
	if err := save(); err != nil {
		return err
	}

	matrixServiceID, err := r.lookupServiceIDByDisplayName(ctx, r.cfg.MatrixBindableServiceName)
	if err != nil {
		return fmt.Errorf("lookup matrix bindable service: %w", err)
	}
	managerServiceID, err := r.lookupServiceIDByDisplayName(ctx, r.cfg.ManagerBindableServiceName)
	if err != nil {
		return fmt.Errorf("lookup manager bindable service: %w", err)
	}

	defaultAgentDeveloper, err := readOptionalFile(r.cfg.DefaultAgentDeveloperInstructionsPath)
	if err != nil {
		return err
	}
	defaultAgentAgents, err := readOptionalFile(r.cfg.DefaultAgentAgentsPath)
	if err != nil {
		return err
	}
	defaultAgentWorkspaceAgents, err := readOptionalFile(r.cfg.DefaultAgentWorkspaceAgentsPath)
	if err != nil {
		return err
	}
	defaultAgentConfigTOML, err := readOptionalFile(r.cfg.DefaultAgentConfigTOMLPath)
	if err != nil {
		return err
	}

	provisionerRootConfig := map[string]any{
		"listen_addr":                          provisionerListenAddr,
		"matrix_bindable_service_name":         r.cfg.MatrixBindableServiceName,
		"default_agent_source_url":             r.cfg.DefaultAgentSourceURL,
		"shared_responses_bindable_service_id": sharedResponsesID,
		"default_agent_model":                  r.cfg.DefaultAgentModel,
		"default_agent_model_reasoning_effort": r.cfg.DefaultAgentModelReasoningEffort,
		"revoked_source_urls":                  strings.Join(r.bootstrapOnlySourceURLs(), "\n"),
	}
	setIfNonBlank(provisionerRootConfig, "default_agent_developer_instructions", defaultAgentDeveloper)
	setIfNonBlank(provisionerRootConfig, "default_agent_agents_md", defaultAgentAgents)
	setIfNonBlank(provisionerRootConfig, "default_agent_workspace_agents_md", defaultAgentWorkspaceAgents)
	setIfNonBlank(provisionerRootConfig, "default_agent_config_toml", defaultAgentConfigTOML)
	if strings.TrimSpace(r.cfg.RegistrationToken) != "" {
		provisionerRootConfig["registration_token"] = r.cfg.RegistrationToken
	}

	provisionerScenarioID, err := r.ensureManagedScenario(ctx, ensureScenarioRequest{
		Kind:               metadataKindProvisioner,
		ExistingScenarioID: state.ProvisionerScenarioID,
		SourceURL:          r.cfg.ProvisionerSourceURL,
		RootConfig:         provisionerRootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"matrix": {
				BindableServiceID: matrixServiceID,
			},
			"amber_manager_api": {
				BindableServiceID: managerServiceID,
			},
		},
		Metadata: map[string]any{
			"kind":                 metadataKindProvisioner,
			"default_agent_source": r.cfg.DefaultAgentSourceURL,
		},
	})
	if err != nil {
		return fmt.Errorf("ensure provisioner scenario: %w", err)
	}
	state.ProvisionerScenarioID = provisionerScenarioID
	if err := save(); err != nil {
		return err
	}

	provisionerMCPServiceID, err := r.waitForScenarioExportServiceID(ctx, provisionerScenarioID, "mcp")
	if err != nil {
		return fmt.Errorf("wait for provisioner MCP export: %w", err)
	}

	onboardingDeveloper, err := readOptionalFile(r.cfg.OnboardingDeveloperInstructionsPath)
	if err != nil {
		return err
	}
	onboardingAgents, err := readOptionalFile(r.cfg.OnboardingAgentsPath)
	if err != nil {
		return err
	}
	onboardingWorkspaceAgents, err := readOptionalFile(r.cfg.OnboardingWorkspaceAgentsPath)
	if err != nil {
		return err
	}
	onboardingConfigTOML, err := readOptionalFile(r.cfg.OnboardingConfigTOMLPath)
	if err != nil {
		return err
	}

	onboardingRootConfig := map[string]any{
		"matrix_username":        r.cfg.OnboardingBotUsername,
		"matrix_password":        r.cfg.OnboardingBotPassword,
		"model":                  r.cfg.OnboardingModel,
		"model_reasoning_effort": r.cfg.OnboardingModelReasoningEffort,
	}
	setIfNonBlank(onboardingRootConfig, "developer_instructions", onboardingDeveloper)
	setIfNonBlank(onboardingRootConfig, "agents_md", onboardingAgents)
	setIfNonBlank(onboardingRootConfig, "workspace_agents_md", onboardingWorkspaceAgents)
	setIfNonBlank(onboardingRootConfig, "config_toml", onboardingConfigTOML)

	onboardingScenarioID, err := r.ensureManagedScenario(ctx, ensureScenarioRequest{
		Kind:               metadataKindOnboarding,
		ExistingScenarioID: state.OnboardingScenarioID,
		SourceURL:          r.cfg.OnboardingSourceURL,
		RootConfig:         onboardingRootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"matrix": {
				BindableServiceID: matrixServiceID,
			},
			"responses_api": {
				BindableServiceID: sharedResponsesID,
			},
			"provisioning_mcp": {
				BindableServiceID: provisionerMCPServiceID,
			},
		},
		Metadata: map[string]any{
			"kind":              metadataKindOnboarding,
			"welcome_room_id":   welcomeRoomID.String(),
			"onboarding_bot_id": onboardingUserID,
			"provisioner_id":    provisionerScenarioID,
		},
	})
	if err != nil {
		return fmt.Errorf("ensure onboarding scenario: %w", err)
	}
	state.OnboardingScenarioID = onboardingScenarioID
	if err := save(); err != nil {
		return err
	}

	for _, sourceURL := range r.bootstrapOnlySourceURLs() {
		if err := r.manager.RemoveAllowlistEntry(ctx, sourceURL); err != nil && !errors.Is(err, managerclient.ErrAllowlistEntryMissing) {
			return fmt.Errorf("revoke bootstrap allowlist entry %q: %w", sourceURL, err)
		}
	}

	return save()
}

type ensureScenarioRequest struct {
	Kind               string
	ExistingScenarioID string
	SourceURL          string
	RootConfig         map[string]any
	ExternalSlots      map[string]managerclient.ExternalSlotBindingRequest
	Metadata           map[string]any
}

func (r *Runner) ensureSharedResponsesService(ctx context.Context, existingScenarioID string) (string, string, error) {
	if strings.TrimSpace(r.cfg.SharedResponsesBindableServiceID) != "" {
		return r.cfg.SharedResponsesBindableServiceID, "", nil
	}
	if strings.TrimSpace(r.cfg.SharedResponsesBindableServiceName) != "" {
		serviceID, err := r.lookupServiceIDByDisplayName(ctx, r.cfg.SharedResponsesBindableServiceName)
		if err == nil {
			return serviceID, "", nil
		}
	}
	if r.cfg.AuthProxySourceURL == "" {
		return "", "", errors.New("no shared responses bindable service is available and auth proxy bootstrap is not configured")
	}
	authJSON, err := os.ReadFile(r.cfg.CodexAuthJSONPath)
	if err != nil {
		return "", "", fmt.Errorf("read codex auth json: %w", err)
	}

	scenarioID, err := r.ensureManagedScenario(ctx, ensureScenarioRequest{
		Kind:               metadataKindAuthProxy,
		ExistingScenarioID: existingScenarioID,
		SourceURL:          r.cfg.AuthProxySourceURL,
		RootConfig: map[string]any{
			"auth_json": string(authJSON),
		},
		Metadata: map[string]any{
			"kind": metadataKindAuthProxy,
		},
	})
	if err != nil {
		return "", "", err
	}
	serviceID, err := r.waitForScenarioExportServiceID(ctx, scenarioID, "responses_api")
	if err != nil {
		return "", "", err
	}
	return serviceID, scenarioID, nil
}

func (r *Runner) ensureManagedScenario(ctx context.Context, req ensureScenarioRequest) (string, error) {
	detail, err := r.lookupExistingScenario(ctx, req)
	if err != nil {
		return "", err
	}
	if detail == nil {
		created, err := r.manager.CreateScenario(ctx, managerclient.CreateScenarioRequest{
			SourceURL:     req.SourceURL,
			RootConfig:    req.RootConfig,
			ExternalSlots: req.ExternalSlots,
			Metadata:      req.Metadata,
			StoreBundle:   true,
			Start:         true,
		})
		if err != nil {
			return "", fmt.Errorf("create scenario: %w", err)
		}
		if err := r.waitForOperationSucceeded(ctx, created.OperationID); err != nil {
			return "", err
		}
		if err := r.waitForScenarioRunning(ctx, created.ScenarioID); err != nil {
			return "", err
		}
		return created.ScenarioID, nil
	}

	if detail.SourceURL != req.SourceURL ||
		!rootConfigMapsEqual(detail.RootConfig, detail.SecretRootConfigPaths, req.RootConfig) ||
		!bindingMapsEqual(detail.ExternalSlots, req.ExternalSlots) ||
		!jsonMapsEqual(detail.Metadata, req.Metadata) ||
		!detail.BundleStored {
		var sourceURL *string
		if detail.SourceURL != req.SourceURL {
			next := req.SourceURL
			sourceURL = &next
		}
		upgraded, err := r.manager.UpgradeScenario(ctx, detail.ScenarioID, managerclient.UpgradeScenarioRequest{
			SourceURL:     sourceURL,
			RootConfig:    req.RootConfig,
			ExternalSlots: req.ExternalSlots,
			Metadata:      req.Metadata,
			StoreBundle:   true,
		})
		if err != nil {
			return "", fmt.Errorf("upgrade scenario %s: %w", detail.ScenarioID, err)
		}
		if err := r.waitForOperationSucceeded(ctx, upgraded.OperationID); err != nil {
			return "", err
		}
	}
	if err := r.waitForScenarioRunning(ctx, detail.ScenarioID); err != nil {
		return "", err
	}
	return detail.ScenarioID, nil
}

func (r *Runner) lookupExistingScenario(ctx context.Context, req ensureScenarioRequest) (*managerclient.ScenarioDetailResponse, error) {
	existingID := strings.TrimSpace(req.ExistingScenarioID)
	if existingID != "" {
		detail, err := r.manager.GetScenario(ctx, existingID)
		if err == nil {
			return &detail, nil
		}
		if !errors.Is(err, managerclient.ErrScenarioMissing) {
			return nil, err
		}
	}

	existing, err := r.findScenarioByKind(ctx, req.Kind)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	detail, err := r.manager.GetScenario(ctx, existing.ScenarioID)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

func (r *Runner) findScenarioByKind(ctx context.Context, kind string) (*managerclient.ScenarioSummaryResponse, error) {
	scenarios, err := r.manager.ListScenarios(ctx)
	if err != nil {
		return nil, err
	}
	var match *managerclient.ScenarioSummaryResponse
	for _, scenario := range scenarios {
		if stringValue(scenario.Metadata["kind"]) != kind {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("found multiple scenarios for kind %q", kind)
		}
		scenarioCopy := scenario
		match = &scenarioCopy
	}
	return match, nil
}

func (r *Runner) lookupServiceIDByDisplayName(ctx context.Context, name string) (string, error) {
	services, err := r.manager.ListBindableServices(ctx)
	if err != nil {
		return "", err
	}
	for _, service := range services {
		if service.DisplayName == name {
			if !service.Available {
				return "", fmt.Errorf("bindable service %q is not available", name)
			}
			return service.BindableServiceID, nil
		}
	}
	return "", fmt.Errorf("bindable service %q not found", name)
}

func (r *Runner) waitForScenarioExportServiceID(ctx context.Context, scenarioID, export string) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	for {
		services, err := r.manager.ListBindableServices(waitCtx)
		if err != nil {
			return "", err
		}
		for _, service := range services {
			if service.ScenarioID == scenarioID && service.Export == export && service.Available {
				return service.BindableServiceID, nil
			}
		}
		select {
		case <-waitCtx.Done():
			return "", fmt.Errorf("timed out waiting for scenario %s export %s", scenarioID, export)
		case <-time.After(waitPollInterval):
		}
	}
}

func (r *Runner) waitForOperationSucceeded(ctx context.Context, operationID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	for {
		status, err := r.manager.GetOperation(waitCtx, operationID)
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
			return fmt.Errorf("timed out waiting for operation %s", operationID)
		case <-time.After(waitPollInterval):
		}
	}
}

func (r *Runner) waitForScenarioRunning(ctx context.Context, scenarioID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	for {
		detail, err := r.manager.GetScenario(waitCtx, scenarioID)
		if err != nil {
			return err
		}
		switch detail.ObservedState {
		case "running":
			return nil
		case "failed":
			if strings.TrimSpace(detail.LastError) != "" {
				return fmt.Errorf("scenario %s failed: %s", scenarioID, detail.LastError)
			}
			return fmt.Errorf("scenario %s failed", scenarioID)
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for scenario %s", scenarioID)
		case <-time.After(waitPollInterval):
		}
	}
}

func (r *Runner) ensureUser(ctx context.Context, username, password string) (string, error) {
	created, err := r.matrix.CreateUser(ctx, username, password)
	if err == nil {
		return created.UserID, nil
	}
	if !errors.Is(err, matrix.ErrUserAlreadyExists) {
		return "", err
	}
	loggedIn, loginErr := r.matrix.LoginUser(ctx, username, password)
	if loginErr != nil {
		return "", fmt.Errorf("matrix username %s already exists and could not be recovered with the configured password: %w", username, loginErr)
	}
	return loggedIn.UserID, nil
}

func (r *Runner) ensureWelcomeRoom(ctx context.Context, adminClient *mautrix.Client, state State) (id.RoomID, error) {
	alias := id.NewRoomAlias(r.cfg.WelcomeRoomAliasLocalpart, r.cfg.MatrixServerName)
	roomID, found, err := resolveAlias(ctx, adminClient, alias)
	if err != nil {
		return "", err
	}
	if !found && state.WelcomeRoomID != "" {
		if _, err := adminClient.CreateAlias(ctx, alias, id.RoomID(state.WelcomeRoomID)); err == nil {
			roomID = id.RoomID(state.WelcomeRoomID)
			found = true
		}
	}
	if !found {
		created, err := adminClient.CreateRoom(ctx, &mautrix.ReqCreateRoom{
			Name:          "Welcome",
			Visibility:    "public",
			Preset:        "public_chat",
			RoomAliasName: r.cfg.WelcomeRoomAliasLocalpart,
		})
		if err != nil {
			return "", fmt.Errorf("create welcome room: %w", err)
		}
		roomID = created.RoomID
	}
	if err := setRoomDirectoryVisibility(ctx, adminClient, roomID, "private"); err != nil {
		return "", err
	}
	return roomID, nil
}

func (r *Runner) ensureWelcomeMessage(ctx context.Context, botClient *mautrix.Client, roomID id.RoomID, botUserID id.UserID) error {
	body := r.welcomeMessage(botUserID)
	found, err := roomHasExactMessage(ctx, botClient, roomID, botUserID, body)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = botClient.SendText(ctx, roomID, body)
	if err != nil {
		return fmt.Errorf("send welcome message: %w", err)
	}
	return nil
}

func (r *Runner) welcomeMessage(botUserID id.UserID) string {
	return fmt.Sprintf(
		"Welcome.\n\nClick this link to start a DM with the onboarding agent:\nhttps://matrix.to/#/%s",
		botUserID.URI().String(),
	)
}

func (r *Runner) bootstrapOnlySourceURLs() []string {
	var urls []string
	for _, sourceURL := range []string{
		r.cfg.ProvisionerSourceURL,
		r.cfg.OnboardingSourceURL,
		r.cfg.AuthProxySourceURL,
	} {
		sourceURL = strings.TrimSpace(sourceURL)
		if sourceURL == "" {
			continue
		}
		if !contains(urls, sourceURL) && sourceURL != r.cfg.DefaultAgentSourceURL {
			urls = append(urls, sourceURL)
		}
	}
	return urls
}

func loginClient(homeserverURL, username, password string) (*mautrix.Client, error) {
	client, err := mautrix.NewClient(homeserverURL, "", "")
	if err != nil {
		return nil, fmt.Errorf("new matrix client: %w", err)
	}
	client.DefaultHTTPRetries = 3
	client.DefaultHTTPBackoff = 500 * time.Millisecond

	deadline := time.Now().Add(10 * time.Second)
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

func resolveAlias(ctx context.Context, client *mautrix.Client, alias id.RoomAlias) (id.RoomID, bool, error) {
	resolved, err := client.ResolveAlias(ctx, alias)
	if err == nil {
		return resolved.RoomID, true, nil
	}
	var httpErr mautrix.HTTPError
	if errors.As(err, &httpErr) && httpErr.RespError != nil && httpErr.RespError.ErrCode == "M_NOT_FOUND" {
		return "", false, nil
	}
	return "", false, fmt.Errorf("resolve alias %s: %w", alias, err)
}

func setRoomDirectoryVisibility(ctx context.Context, client *mautrix.Client, roomID id.RoomID, visibility string) error {
	urlPath := client.BuildClientURL("v3", "directory", "list", "room", roomID)
	_, err := client.MakeRequest(ctx, http.MethodPut, urlPath, map[string]any{"visibility": visibility}, &struct{}{})
	if err != nil {
		return fmt.Errorf("set room directory visibility for %s: %w", roomID, err)
	}
	return nil
}

func roomHasExactMessage(ctx context.Context, client *mautrix.Client, roomID id.RoomID, sender id.UserID, body string) (bool, error) {
	resp, err := client.SyncRequest(ctx, 0, "", "", true, "")
	if err != nil {
		return false, fmt.Errorf("sync room messages for %s: %w", roomID, err)
	}
	room, ok := resp.Rooms.Join[roomID]
	if !ok {
		return false, nil
	}
	for _, evt := range room.Timeline.Events {
		if evt == nil || evt.Type != event.EventMessage || evt.Sender != sender {
			continue
		}
		if eventMessageBody(evt) == body {
			return true, nil
		}
	}
	return false, nil
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

func waitForJoinedMember(ctx context.Context, client *mautrix.Client, roomID id.RoomID, userID id.UserID, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		members, err := client.JoinedMembers(waitCtx, roomID)
		if err == nil {
			if _, ok := members.Joined[userID]; ok {
				return nil
			}
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for %s to join %s", userID, roomID)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func readOptionalFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(blob), nil
}

func setIfNonBlank(target map[string]any, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	target[key] = value
}

func bindingMapsEqual(current map[string]managerclient.ExternalSlotBindingResponse, want map[string]managerclient.ExternalSlotBindingRequest) bool {
	if len(current) != len(want) {
		return false
	}
	for name, binding := range want {
		if current[name].BindableServiceID != binding.BindableServiceID {
			return false
		}
	}
	return true
}

func jsonMapsEqual(current map[string]any, want map[string]any) bool {
	return reflect.DeepEqual(normalizeJSONMap(current), normalizeJSONMap(want))
}

func rootConfigMapsEqual(current map[string]any, secretPaths []string, want map[string]any) bool {
	return jsonMapsEqual(current, removeJSONPaths(want, secretPaths))
}

func removeJSONPaths(value map[string]any, paths []string) map[string]any {
	out := normalizeJSONMap(value)
	for _, path := range paths {
		deleteJSONPath(out, path)
	}
	return out
}

func deleteJSONPath(root map[string]any, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	deleteJSONPathSegments(root, strings.Split(path, "."))
}

func deleteJSONPathSegments(node map[string]any, segments []string) {
	if len(segments) == 0 || node == nil {
		return
	}
	if len(segments) == 1 {
		delete(node, segments[0])
		return
	}
	child, ok := node[segments[0]].(map[string]any)
	if !ok {
		return
	}
	deleteJSONPathSegments(child, segments[1:])
}

func normalizeJSONMap(value map[string]any) map[string]any {
	blob, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out map[string]any
	if json.Unmarshal(blob, &out) != nil {
		return value
	}
	return out
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
