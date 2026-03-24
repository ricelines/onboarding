package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/ricelines/chat/onboarding/internal/bootstrap"
	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"maunium.net/go/mautrix/id"
)

func TestLiveBootstrapAndOnboardingWorkflowRealCodex(t *testing.T) {
	requireLiveRealTests(t)
	if testing.Short() {
		t.Skip("real live onboarding tests are not run in short mode")
	}
	testStart := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	stackStart := time.Now()
	stack := startRealLiveStack(t)
	t.Logf("phase start_live_stack: %s", time.Since(stackStart))
	defer func() {
		if t.Failed() {
			t.Logf("amber-manager logs:\n%s", stack.manager.logs())
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
	if strings.TrimSpace(state1.AuthProxyScenarioID) == "" {
		t.Fatalf("first bootstrap did not persist an auth proxy scenario ID: %+v", state1)
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
	if strings.TrimSpace(state2.AuthProxyScenarioID) == "" {
		t.Fatalf("second bootstrap did not persist an auth proxy scenario ID: %+v", state2)
	}
	if strings.TrimSpace(state2.SharedResponsesBindableServiceID) == "" {
		t.Fatalf("second bootstrap did not persist a shared responses bindable service ID: %+v", state2)
	}

	scenarios := stack.mustListScenarios(ctx)
	assertScenarioCount(t, scenarios, "onboarding-auth-proxy", 1)
	assertScenarioCount(t, scenarios, "onboarding-provisioner", 1)
	assertScenarioCount(t, scenarios, "onboarding-agent", 1)
	assertScenarioCount(t, scenarios, "user-agent", 0)

	onboardingDetail, err := stack.managerClient.GetScenario(ctx, state2.OnboardingScenarioID)
	if err != nil {
		t.Fatalf("load onboarding scenario %s: %v", state2.OnboardingScenarioID, err)
	}
	assertScenarioUsesResponsesProvider(t, onboardingDetail, state2.AuthProxyScenarioID)
	assertScenarioModelConfig(t, onboardingDetail, cfg.OnboardingModel, cfg.OnboardingModelReasoningEffort)
	assertCodexContainerEnv(t, state2.OnboardingScenarioID, cfg.OnboardingModel, cfg.OnboardingModelReasoningEffort)

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
	welcomeBody := waitForMessageBody(
		t,
		ownerClient,
		dmRoomID,
		onboardingBotUserID,
		roomEventTimeout,
		func(body string) bool {
			return containsAllFold(body, "agent") && strings.Contains(body, "?")
		},
		"welcome message",
		"agent",
	)
	t.Logf("real onboarding welcome: %q", welcomeBody)
	t.Logf("phase create_onboarding_dm: %s", time.Since(initialDMStart))

	provisionStart := time.Now()
	if _, err := ownerClient.SendText(ctx, dmRoomID, "yes"); err != nil {
		t.Fatalf("send onboarding confirmation: %v", err)
	}

	waitScenarioStart := time.Now()
	userAgentScenario := waitForSingleUserAgentScenario(t, ctx, stack.managerClient, ownerUserID.String(), "", scenarioReadyTimeout)
	t.Logf("phase provision.wait_user_agent_running: %s", time.Since(waitScenarioStart))
	expectedBotUsername := stringValue(userAgentScenario.Metadata["bot_username"])
	if strings.TrimSpace(expectedBotUsername) == "" {
		t.Fatalf("user-agent bot_username metadata missing: %#v", userAgentScenario.Metadata)
	}
	expectedBotUserID := id.UserID(stringValue(userAgentScenario.Metadata["bot_matrix_user_id"]))
	if expectedBotUserID == "" {
		t.Fatalf("user-agent bot_matrix_user_id metadata missing: %#v", userAgentScenario.Metadata)
	}

	userAgentDetail, err := stack.managerClient.GetScenario(ctx, userAgentScenario.ScenarioID)
	if err != nil {
		t.Fatalf("load user-agent scenario %s: %v", userAgentScenario.ScenarioID, err)
	}
	assertScenarioUsesResponsesProvider(t, userAgentDetail, state2.AuthProxyScenarioID)
	assertScenarioModelConfig(t, userAgentDetail, cfg.DefaultAgentModel, cfg.DefaultAgentModelReasoningEffort)
	assertCodexContainerEnv(t, userAgentScenario.ScenarioID, cfg.DefaultAgentModel, cfg.DefaultAgentModelReasoningEffort)

	waitCredentialsStart := time.Now()
	credentialsBody := waitForMessageBody(
		t,
		ownerClient,
		dmRoomID,
		onboardingBotUserID,
		roomEventTimeout,
		func(body string) bool {
			if username, password, ok := parseCreatedCredentialsMessage(body); ok {
				return username == expectedBotUsername && strings.TrimSpace(password) != ""
			}
			return containsAllFold(body, expectedBotUsername, "password")
		},
		"created credentials",
		expectedBotUsername,
	)
	t.Logf("phase provision.wait_credentials_reply: %s", time.Since(waitCredentialsStart))
	t.Logf("real onboarding credentials reply: %q", credentialsBody)
	if e2eProfilingEnabled() {
		logScenarioRoomTaskTrace(t, stack, state2.OnboardingScenarioID, dmRoomID, "onboarding.provision")
	}
	waitForRoomMembersExactly(t, ownerClient, dmRoomID, []id.UserID{ownerUserID, onboardingBotUserID}, roomEventTimeout)

	waitChildInviteStart := time.Now()
	newBotDMRoomID := waitForInvitedRoom(t, ownerClient, expectedBotUserID, ownerUserID, 30*time.Second)
	t.Logf("phase provision.wait_child_dm_invite: %s", time.Since(waitChildInviteStart))
	joinRoom(t, ownerClient, newBotDMRoomID)
	waitForJoinedMember(t, ownerClient, newBotDMRoomID, ownerUserID, roomEventTimeout)
	waitForJoinedMember(t, ownerClient, newBotDMRoomID, expectedBotUserID, roomEventTimeout)
	waitChildIntroStart := time.Now()
	introBody := waitForMessageBody(
		t,
		ownerClient,
		newBotDMRoomID,
		expectedBotUserID,
		roomEventTimeout,
		func(body string) bool { return strings.TrimSpace(body) != "" },
		"child intro message",
		"",
	)
	t.Logf("phase provision.wait_child_intro: %s", time.Since(waitChildIntroStart))
	if !looksLikeGreeting(introBody) {
		t.Fatalf("child intro message %q did not look like a greeting", introBody)
	}
	t.Logf("real child intro message: %q", introBody)
	waitForRoomMembersExactly(t, ownerClient, newBotDMRoomID, []id.UserID{ownerUserID, expectedBotUserID}, roomEventTimeout)
	t.Logf("phase provision_and_child_intro_dm: %s", time.Since(provisionStart))
	if e2eProfilingEnabled() {
		logProvisionerProfileLines(t, state2.ProvisionerScenarioID, ownerUserID.String())
	}

	repeatGuardStart := time.Now()
	repeatRoomID := createPrivateRoomAndInvite(t, ownerClient, onboardingBotUserID)
	waitForJoinedMember(t, ownerClient, repeatRoomID, onboardingBotUserID, roomEventTimeout)
	waitRepeatWelcomeStart := time.Now()
	waitForMessageBody(
		t,
		ownerClient,
		repeatRoomID,
		onboardingBotUserID,
		roomEventTimeout,
		func(body string) bool { return containsAllFold(body, "agent") },
		"repeat welcome message",
		"agent",
	)
	t.Logf("phase duplicate.wait_repeat_welcome: %s", time.Since(waitRepeatWelcomeStart))
	if _, err := ownerClient.SendText(ctx, repeatRoomID, "I want a new agent"); err != nil {
		t.Fatalf("send duplicate onboarding request: %v", err)
	}
	waitDuplicateReplyStart := time.Now()
	duplicateBody, err := waitForMessageBodyUntil(
		ownerClient,
		repeatRoomID,
		onboardingBotUserID,
		roomEventTimeout,
		func(body string) bool { return containsAllFold(body, "already", expectedBotUsername) },
		"duplicate guard message",
		expectedBotUsername,
	)
	if err != nil {
		if messages, messageErr := collectMessageBodies(ownerClient, repeatRoomID, onboardingBotUserID); messageErr != nil {
			t.Logf("failed to collect duplicate room messages: %v", messageErr)
		} else {
			t.Logf("duplicate room messages from onboarding: %#v", messages)
		}
		if e2eProfilingEnabled() {
			logScenarioRoomTaskTrace(t, stack, state2.OnboardingScenarioID, repeatRoomID, "onboarding.duplicate.failure")
		}
		t.Fatal(err)
	}
	t.Logf("phase duplicate.wait_duplicate_reply: %s", time.Since(waitDuplicateReplyStart))
	t.Logf("real duplicate-guard reply: %q", duplicateBody)
	if e2eProfilingEnabled() {
		logScenarioRoomTaskTrace(t, stack, state2.OnboardingScenarioID, repeatRoomID, "onboarding.duplicate")
		logProvisionerProfileLines(t, state2.ProvisionerScenarioID, ownerUserID.String())
	}

	scenarios = stack.mustListScenarios(ctx)
	assertScenarioCount(t, scenarios, "user-agent", 1)
	t.Logf("phase duplicate_guard: %s", time.Since(repeatGuardStart))
	t.Logf("phase total: %s", time.Since(testStart))
}

func splitNonEmptyLines(input string) []string {
	var lines []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

type bridgeStateSnapshot struct {
	RoomSessions []struct {
		RoomID       string `json:"room_id"`
		ContextID    string `json:"context_id"`
		LatestTaskID string `json:"latest_task_id"`
	} `json:"room_sessions"`
}

type taskArtifactTrace struct {
	ArtifactID     string
	ItemType       string
	ItemID         string
	ItemStartedAt  time.Time
	ItemStartedOK  bool
	EmittedAt      time.Time
	EmittedAtOK    bool
	TaskStartedAt  time.Time
	TaskStartedOK  bool
	Description    string
}

func logScenarioRoomTaskTrace(t *testing.T, stack *liveStack, scenarioID string, roomID id.RoomID, label string) {
	t.Helper()

	taskID := latestTaskIDForScenarioRoom(t, scenarioID, roomID)
	endpoint := scenarioExportURLFromManagerLogs(t, stack.manager.logs(), scenarioID, "a2a")
	task := fetchScenarioTask(t, stack.manager.containerName, endpoint, taskID)
	t.Logf(
		"profile %s task=%s endpoint=%s state=%s artifacts=%d history=%d",
		label,
		taskID,
		endpoint,
		task.Status.State,
		len(task.Artifacts),
		len(task.History),
	)
	for _, trace := range summarizeTaskArtifacts(task) {
		t.Logf("profile %s %s", label, trace.Description)
	}
}

func latestTaskIDForScenarioRoom(t *testing.T, scenarioID string, roomID id.RoomID) string {
	t.Helper()

	containerID := scenarioContainerIDByImagePrefix(t, scenarioID, "ghcr.io/ricelines/matrix-a2a-bridge:")
	stateCopyPath := fmt.Sprintf("%s/%s-state.json", t.TempDir(), scenarioID)
	output, err := runCommand(
		20*time.Second,
		"docker",
		"cp",
		containerID+":/app/data/state.json",
		stateCopyPath,
	)
	if err != nil {
		t.Fatalf("copy bridge state for scenario %s: %v\n%s", scenarioID, err, output)
	}
	stateBytes, err := os.ReadFile(stateCopyPath)
	if err != nil {
		t.Fatalf("read copied bridge state for scenario %s: %v", scenarioID, err)
	}

	var snapshot bridgeStateSnapshot
	if err := json.Unmarshal(stateBytes, &snapshot); err != nil {
		t.Fatalf("decode bridge state for scenario %s: %v\n%s", scenarioID, err, string(stateBytes))
	}

	for _, session := range snapshot.RoomSessions {
		if session.RoomID != roomID.String() {
			continue
		}
		if strings.TrimSpace(session.LatestTaskID) == "" {
			t.Fatalf("room %s in scenario %s has no latest task id in bridge state: %+v", roomID, scenarioID, session)
		}
		return session.LatestTaskID
	}

	t.Fatalf("bridge state for scenario %s has no room session for %s: %+v", scenarioID, roomID, snapshot.RoomSessions)
	return ""
}

func fetchScenarioTask(t *testing.T, managerContainerName, endpoint string, taskID string) *a2a.Task {
	t.Helper()

	output, err := runCommand(
		20*time.Second,
		"docker",
		"run",
		"--rm",
		"--network", "container:"+managerContainerName,
		"--entrypoint", "/app/onboarding-a2a-get-task",
		"ghcr.io/ricelines/onboarding:v0.1",
		"--base-url", endpoint,
		"--task-id", taskID,
	)
	if err != nil {
		t.Fatalf("fetch scenario task %s from %s: %v\n%s", taskID, endpoint, err, output)
	}

	var task a2a.Task
	if err := json.Unmarshal([]byte(output), &task); err != nil {
		t.Fatalf("decode scenario task %s from %s: %v\n%s", taskID, endpoint, err, output)
	}
	return &task
}

func scenarioExportURLFromManagerLogs(t *testing.T, logs string, scenarioID string, exportName string) string {
	t.Helper()

	scenarioMarker := "/var/lib/amber-manager/scenarios/" + scenarioID + "/revisions/"
	exportPrefix := "export " + exportName + " -> "
	inScenarioBlock := false
	lastURL := ""

	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "docker compose [\"up\", \"-d\", \"--remove-orphans\"] succeeded for ") {
			inScenarioBlock = strings.Contains(line, scenarioMarker)
			continue
		}
		if !inScenarioBlock || !strings.Contains(line, exportPrefix) {
			continue
		}
		idx := strings.Index(line, exportPrefix)
		if idx < 0 {
			continue
		}
		lastURL = strings.TrimSpace(line[idx+len(exportPrefix):])
	}

	if strings.TrimSpace(lastURL) == "" {
		t.Fatalf("manager logs did not include export %s url for scenario %s:\n%s", exportName, scenarioID, logs)
	}
	return lastURL
}

func summarizeTaskArtifacts(task *a2a.Task) []taskArtifactTrace {
	if task == nil {
		return nil
	}

	var traces []taskArtifactTrace
	for _, artifact := range task.Artifacts {
		if artifact == nil {
			continue
		}
		trace := taskArtifactTrace{
			ArtifactID: string(artifact.ID),
			ItemType:   metadataString(artifact.Metadata, "itemType"),
			ItemID:     metadataString(artifact.Metadata, "itemId"),
		}
		trace.TaskStartedAt, trace.TaskStartedOK = metadataTime(artifact.Metadata, "taskStartedAt")
		trace.ItemStartedAt, trace.ItemStartedOK = metadataTime(artifact.Metadata, "itemStartedAt")
		trace.EmittedAt, trace.EmittedAtOK = metadataTime(artifact.Metadata, "emittedAt")
		trace.Description = describeArtifact(trace, artifact)
		traces = append(traces, trace)
	}

	sort.SliceStable(traces, func(i, j int) bool {
		left, right := traces[i], traces[j]
		switch {
		case left.ItemStartedOK && right.ItemStartedOK && !left.ItemStartedAt.Equal(right.ItemStartedAt):
			return left.ItemStartedAt.Before(right.ItemStartedAt)
		case left.EmittedAtOK && right.EmittedAtOK && !left.EmittedAt.Equal(right.EmittedAt):
			return left.EmittedAt.Before(right.EmittedAt)
		default:
			return left.ArtifactID < right.ArtifactID
		}
	})
	return traces
}

func describeArtifact(trace taskArtifactTrace, artifact *a2a.Artifact) string {
	prefix := formatArtifactTiming(trace)
	switch trace.ItemType {
	case "reasoning":
		return prefix + " reasoning " + summarizeReasoningArtifact(artifact)
	case "mcpToolCall":
		return prefix + " mcpToolCall " + summarizeToolCallArtifact(artifact)
	case "agentMessage":
		return prefix + " agentMessage " + summarizeAssistantArtifact(artifact)
	case "webSearch":
		return prefix + " webSearch " + summarizeDataArtifact(artifact)
	default:
		return prefix + " " + strings.TrimSpace(trace.ItemType+" "+summarizeArtifactFallback(artifact))
	}
}

func formatArtifactTiming(trace taskArtifactTrace) string {
	parts := []string{}
	if trace.TaskStartedOK && trace.ItemStartedOK {
		parts = append(parts, "item_start=+"+trace.ItemStartedAt.Sub(trace.TaskStartedAt).Round(time.Millisecond).String())
	}
	if trace.TaskStartedOK && trace.EmittedAtOK {
		parts = append(parts, "emitted=+"+trace.EmittedAt.Sub(trace.TaskStartedAt).Round(time.Millisecond).String())
	}
	if trace.ItemStartedOK && trace.EmittedAtOK {
		parts = append(parts, "item_duration="+trace.EmittedAt.Sub(trace.ItemStartedAt).Round(time.Millisecond).String())
	}
	if trace.ItemID != "" {
		parts = append(parts, "item_id="+trace.ItemID)
	}
	if len(parts) == 0 {
		return "artifact"
	}
	return strings.Join(parts, " ")
}

func summarizeReasoningArtifact(artifact *a2a.Artifact) string {
	data := firstDataPart(artifact)
	if data == nil {
		return summarizeArtifactFallback(artifact)
	}
	summary := trimOneLine(jsonValueString(data["summary"]))
	if summary != "" {
		return "summary=" + quoteSummary(summary)
	}
	content := trimOneLine(jsonValueString(data["content"]))
	if content != "" {
		return "content=" + quoteSummary(content)
	}
	return summarizeDataArtifact(artifact)
}

func summarizeToolCallArtifact(artifact *a2a.Artifact) string {
	data := firstDataPart(artifact)
	if data == nil {
		return summarizeArtifactFallback(artifact)
	}

	parts := []string{}
	if server := trimOneLine(jsonValueString(data["server"])); server != "" {
		parts = append(parts, "server="+server)
	}
	if tool := trimOneLine(jsonValueString(data["tool"])); tool != "" {
		parts = append(parts, "tool="+tool)
	}
	if phase := trimOneLine(jsonValueString(data["phase"])); phase != "" {
		parts = append(parts, "phase="+phase)
	}
	if result, ok := data["result"]; ok && result != nil {
		parts = append(parts, "result="+quoteSummary(trimOneLine(jsonValueString(result))))
	}
	if errText := trimOneLine(jsonValueString(data["error"])); errText != "" {
		parts = append(parts, "error="+quoteSummary(errText))
	}
	if len(parts) == 0 {
		return summarizeDataArtifact(artifact)
	}
	return strings.Join(parts, " ")
}

func summarizeAssistantArtifact(artifact *a2a.Artifact) string {
	for _, part := range artifact.Parts {
		switch typed := part.(type) {
		case a2a.TextPart:
			return "text=" + quoteSummary(trimOneLine(typed.Text))
		case *a2a.TextPart:
			return "text=" + quoteSummary(trimOneLine(typed.Text))
		}
	}
	return summarizeArtifactFallback(artifact)
}

func summarizeDataArtifact(artifact *a2a.Artifact) string {
	data := firstDataPart(artifact)
	if data == nil {
		return summarizeArtifactFallback(artifact)
	}
	return "data=" + quoteSummary(trimOneLine(jsonValueString(data)))
}

func summarizeArtifactFallback(artifact *a2a.Artifact) string {
	if artifact == nil {
		return ""
	}
	for _, part := range artifact.Parts {
		switch typed := part.(type) {
		case a2a.TextPart:
			if text := trimOneLine(typed.Text); text != "" {
				return quoteSummary(text)
			}
		case *a2a.TextPart:
			if text := trimOneLine(typed.Text); text != "" {
				return quoteSummary(text)
			}
		case a2a.DataPart:
			return quoteSummary(trimOneLine(jsonValueString(typed.Data)))
		case *a2a.DataPart:
			return quoteSummary(trimOneLine(jsonValueString(typed.Data)))
		}
	}
	return ""
}

func firstDataPart(artifact *a2a.Artifact) map[string]any {
	if artifact == nil {
		return nil
	}
	for _, part := range artifact.Parts {
		switch typed := part.(type) {
		case a2a.DataPart:
			return typed.Data
		case *a2a.DataPart:
			return typed.Data
		}
	}
	return nil
}

func metadataString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	return trimOneLine(jsonValueString(meta[key]))
}

func metadataTime(meta map[string]any, key string) (time.Time, bool) {
	if meta == nil {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(jsonValueString(meta[key]))
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func jsonValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case map[string]any, []any:
		blob, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(blob)
	default:
		blob, err := json.Marshal(typed)
		if err == nil {
			return string(blob)
		}
		return fmt.Sprintf("%v", typed)
	}
}

func trimOneLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func quoteSummary(value string) string {
	if len(value) > 240 {
		value = value[:237] + "..."
	}
	return fmt.Sprintf("%q", value)
}

func logProvisionerProfileLines(t *testing.T, scenarioID, ownerMatrixUserID string) {
	t.Helper()

	containerID := scenarioContainerIDByImagePrefix(t, scenarioID, "ghcr.io/ricelines/onboarding:")
	output, err := runCommand(20*time.Second, "docker", "logs", containerID)
	if err != nil {
		t.Fatalf("docker logs %s: %v\n%s", containerID, err, output)
	}
	var matched bool
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "provision_initial owner="+ownerMatrixUserID+" ") {
			continue
		}
		matched = true
		t.Logf("profile provisioner %s", strings.TrimSpace(line))
	}
	if !matched {
		t.Logf("profile provisioner no lines matched owner=%s scenario=%s", ownerMatrixUserID, scenarioID)
	}
}

func scenarioContainerIDByImagePrefix(t *testing.T, scenarioID, imagePrefix string) string {
	t.Helper()

	projectName := "amber_" + scenarioID
	output, err := runCommand(
		20*time.Second,
		"docker",
		"ps",
		"--filter", "label=com.docker.compose.project="+projectName,
		"--format", "{{.ID}} {{.Image}}",
	)
	if err != nil {
		t.Fatalf("list containers for scenario %s: %v\n%s", scenarioID, err, output)
	}
	for _, line := range splitNonEmptyLines(output) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.HasPrefix(fields[1], imagePrefix) {
			return fields[0]
		}
	}
	t.Fatalf("scenario %s has no container with image prefix %q in output:\n%s", scenarioID, imagePrefix, output)
	return ""
}

func assertScenarioUsesResponsesProvider(t *testing.T, detail managerclient.ScenarioDetailResponse, providerScenarioID string) {
	t.Helper()

	slot, ok := detail.ExternalSlots["responses_api"]
	if !ok {
		t.Fatalf("scenario %s is missing responses_api slot binding", detail.ScenarioID)
	}
	if slot.ProviderScenarioID != providerScenarioID {
		t.Fatalf(
			"scenario %s responses_api provider = %q, want %q",
			detail.ScenarioID,
			slot.ProviderScenarioID,
			providerScenarioID,
		)
	}
}

func assertScenarioModelConfig(t *testing.T, detail managerclient.ScenarioDetailResponse, model string, reasoning string) {
	t.Helper()

	if got := stringValue(detail.RootConfig["model"]); got != model {
		t.Fatalf("scenario %s root_config.model = %q, want %q", detail.ScenarioID, got, model)
	}
	if got := stringValue(detail.RootConfig["model_reasoning_effort"]); got != reasoning {
		t.Fatalf("scenario %s root_config.model_reasoning_effort = %q, want %q", detail.ScenarioID, got, reasoning)
	}
}

func assertCodexContainerEnv(t *testing.T, scenarioID, model, reasoning string) {
	t.Helper()

	containerName := findCodexContainerName(t, scenarioID)
	output, err := runCommand(
		20*time.Second,
		"docker",
		"exec",
		containerName,
		"sh",
		"-lc",
		`tr '\0' '\n' </proc/1/environ`,
	)
	if err != nil {
		t.Fatalf("read env for container %s: %v\n%s", containerName, err, output)
	}

	env := parseEnvLines(output)
	if got := normalizeEnvValue(env["AMBER_CONFIG_MODEL"]); got != model {
		t.Fatalf("container %s AMBER_CONFIG_MODEL = %q, want %q", containerName, got, model)
	}
	if got := normalizeEnvValue(env["AMBER_CONFIG_MODEL_REASONING_EFFORT"]); got != reasoning {
		t.Fatalf("container %s AMBER_CONFIG_MODEL_REASONING_EFFORT = %q, want %q", containerName, got, reasoning)
	}
}

func findCodexContainerName(t *testing.T, scenarioID string) string {
	t.Helper()

	prefix := "amber_" + scenarioID + "-"
	output, err := runCommand(20*time.Second, "docker", "ps", "--format", "{{.Names}} {{.Image}}")
	if err != nil {
		t.Fatalf("list docker containers: %v\n%s", err, output)
	}

	var matches []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		image := parts[1]
		if strings.HasPrefix(name, prefix) && strings.Contains(image, "codex-a2a") {
			matches = append(matches, name)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("codex container matches for scenario %s = %#v, want exactly 1", scenarioID, matches)
	}
	return matches[0]
}

func parseEnvLines(output string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func normalizeEnvValue(value string) string {
	return strings.TrimSpace(strings.Trim(value, `"`))
}

func containsAllFold(body string, parts ...string) bool {
	body = strings.ToLower(body)
	for _, part := range parts {
		if !strings.Contains(body, strings.ToLower(strings.TrimSpace(part))) {
			return false
		}
	}
	return true
}

func looksLikeGreeting(body string) bool {
	lowered := strings.ToLower(strings.TrimSpace(body))
	for _, prefix := range []string{"hi", "hello", "hey"} {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}
