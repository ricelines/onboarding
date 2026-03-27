package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	agentKindOnboarding = "onboarding"
	agentKindDefault    = "user-agent"
)

type mockResponsesServer struct {
	URL string

	listener net.Listener
	server   *http.Server

	counter uint64

	mu     sync.Mutex
	reqs   []capturedResponsesRequest
	states map[string]responsesConversationState
	calls  map[string]responsesConversationState
	rooms  map[string]activationRoomState
	debug  bool
}

type capturedResponsesRequest struct {
	Header http.Header
	Body   []byte
	At     time.Time
}

type responsesConversationState struct {
	AgentKind      string
	RoomID         string
	RoomIsDM       bool
	OwnerUserID    string
	TriggerEvent   string
	BotUserID      string
	BotUsername    string
	BotPassword    string
	OwnerDMRoomID  string
	OwnerIntroSent bool
	PendingTool    string
	PendingCallID  string
	ToolNames      map[string]string
}

type activationRoomState struct {
	OwnerUserID    string
	BotUserID      string
	ActivationSent bool
}

type functionCallOutput struct {
	CallID string
	Output any
}

type provisionInitialOutput struct {
	Created       bool   `json:"created"`
	AlreadyExists bool   `json:"already_exists"`
	ScenarioID    string `json:"scenario_id"`
	BotUserID     string `json:"bot_user_id"`
	BotUsername   string `json:"bot_username"`
	BotPassword   string `json:"bot_password"`
}

type matrixRoomUpdateEnvelope struct {
	Kind    string                 `json:"kind"`
	RoomID  string                 `json:"room_id"`
	Updates []matrixRoomUpdatePart `json:"updates"`
}

type matrixRoomUpdatePart struct {
	RoomSection string                `json:"room_section"`
	State       []matrixRoomUpdateEvt `json:"state,omitempty"`
	Timeline    []matrixRoomUpdateEvt `json:"timeline,omitempty"`
}

type matrixRoomUpdateEvt struct {
	EventID  string         `json:"event_id"`
	RoomID   string         `json:"room_id"`
	Sender   string         `json:"sender"`
	StateKey string         `json:"state_key"`
	Type     string         `json:"type"`
	Content  map[string]any `json:"content"`
}

type matrixRoomUpdateEntry struct {
	RoomSection string
	Event       matrixRoomUpdateEvt
}

func startMockResponsesServer(t *testing.T) *mockResponsesServer {
	t.Helper()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock responses server: %v", err)
	}

	srv := &mockResponsesServer{
		URL:      "http://" + listener.Addr().String(),
		listener: listener,
		states:   make(map[string]responsesConversationState),
		calls:    make(map[string]responsesConversationState),
		rooms:    make(map[string]activationRoomState),
		debug:    os.Getenv("ONBOARDING_DEBUG_RESPONSES") != "",
	}
	srv.server = &http.Server{Handler: http.HandlerFunc(srv.handle)}
	go func() {
		err := srv.server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
	return srv
}

func (s *mockResponsesServer) Close() {
	_ = s.server.Close()
	_ = s.listener.Close()
}

func (s *mockResponsesServer) dumpRequests() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.reqs) == 0 {
		return "<none>"
	}

	var out strings.Builder
	for idx, req := range s.reqs {
		if idx > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "request %d at %s\n%s", idx+1, req.At.UTC().Format(time.RFC3339Nano), string(req.Body))
	}
	return out.String()
}

func (s *mockResponsesServer) port() int {
	if s == nil || s.listener == nil {
		return 0
	}
	addr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

func (s *mockResponsesServer) handle(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.URL.Path != "/v1/responses" {
		http.NotFound(rw, req)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(rw, fmt.Sprintf("decode request: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.reqs = append(s.reqs, capturedResponsesRequest{
		Header: req.Header.Clone(),
		Body:   append([]byte(nil), body...),
		At:     time.Now(),
	})
	s.mu.Unlock()

	responseID, item, state := s.planResponse(payload)
	if s.debug {
		s.logPlan(payload, state, item)
	}
	consumedCallID := ""
	if output, ok := latestFunctionCallOutput(payload["input"]); ok {
		consumedCallID = output.CallID
	}
	s.recordState(responseID, state, consumedCallID)

	itemJSON, err := json.Marshal(item)
	if err != nil {
		http.Error(rw, fmt.Sprintf("encode response item: %v", err), http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(rw, fmt.Sprintf(
		"event: response.created\n"+
			"data: {\"type\":\"response.created\",\"response\":{\"id\":%q}}\n\n"+
			"event: response.output_item.done\n"+
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n"+
			"event: response.completed\n"+
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"output\":[%s]}}\n\n",
		responseID,
		itemJSON,
		responseID,
		itemJSON,
	))
}

func (s *mockResponsesServer) recordState(responseID string, state responsesConversationState, consumedCallID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if consumedCallID != "" {
		delete(s.calls, consumedCallID)
	}
	if state.AgentKind == "" {
		return
	}
	s.states[responseID] = state
	if state.PendingCallID != "" {
		s.calls[state.PendingCallID] = state
	}
}

func (s *mockResponsesServer) logPlan(payload map[string]any, state responsesConversationState, item map[string]any) {
	output, hasOutput := latestFunctionCallOutput(payload["input"])
	update, hasUpdate := latestMatrixRoomUpdate(payload["input"])
	entry, hasEntry := latestRoomUpdateEntry(update)

	parts := []string{
		fmt.Sprintf("prev_response_id=%q", stringValue(payload["previous_response_id"])),
		fmt.Sprintf("agent=%q", state.AgentKind),
		fmt.Sprintf("pending=%q", state.PendingTool),
		fmt.Sprintf("emitted_type=%q", stringValue(item["type"])),
		fmt.Sprintf("emitted_name=%q", stringValue(item["name"])),
	}
	if hasOutput {
		parts = append(parts, fmt.Sprintf("function_output_call_id=%q", output.CallID))
		parts = append(parts, fmt.Sprintf("function_output=%s", compactJSON(output.Output)))
	}
	if hasUpdate {
		parts = append(parts, fmt.Sprintf("room_id=%q", update.RoomID))
	}
	if hasEntry {
		parts = append(parts,
			fmt.Sprintf("room_section=%q", entry.RoomSection),
			fmt.Sprintf("event_type=%q", entry.Event.Type),
			fmt.Sprintf("sender=%q", entry.Event.Sender),
			fmt.Sprintf("body=%q", messageBody(entry.Event)),
		)
	}
	log.Printf("mock responses: %s", strings.Join(parts, " "))
}

func (s *mockResponsesServer) planResponse(payload map[string]any) (string, map[string]any, responsesConversationState) {
	prevState := s.stateFor(stringValue(payload["previous_response_id"]))
	state := prevState
	state = state.withToolCatalog(toolCatalogFromPayload(payload["tools"]))
	if state.AgentKind == "" {
		state.AgentKind = detectAgentKindFromPayload(payload, state.ToolNames)
	}

	if output, ok := latestFunctionCallOutput(payload["input"]); ok {
		outputState := state
		if outputState.PendingCallID == "" || outputState.PendingCallID != output.CallID {
			if callState := s.stateForCall(output.CallID); callState.AgentKind != "" {
				outputState = callState.withToolCatalog(toolCatalogFromPayload(payload["tools"]))
			}
		}
		if outputState.PendingTool != "" && outputState.PendingCallID == output.CallID {
			return s.planFromToolOutput(outputState, output)
		}
	}

	update, ok := latestMatrixRoomUpdate(payload["input"])
	if !ok {
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	}

	switch state.AgentKind {
	case agentKindOnboarding:
		return s.planOnboardingEvent(state, update)
	case agentKindDefault:
		return s.planDefaultAgentEvent(state, update)
	default:
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	}
}

func (s *mockResponsesServer) planFromToolOutput(state responsesConversationState, output functionCallOutput) (string, map[string]any, responsesConversationState) {
	switch state.PendingTool {
	case "matrix.v1.rooms.join":
		state.PendingTool = ""
		state.PendingCallID = ""
		if state.AgentKind == agentKindOnboarding {
			return s.emitToolCall(state, "matrix.v1.messages.send_text", map[string]any{
				"room_id": state.RoomID,
				"body":    "Welcome. Do you want a new agent?",
			})
		}
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	case "matrix.v1.rooms.create":
		state.PendingTool = ""
		state.PendingCallID = ""

		roomID, ok := decodeRoomIDOutput(output.Output)
		if !ok {
			return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
		}

		switch state.AgentKind {
		case agentKindOnboarding:
			s.setActivationRoom(roomID, activationRoomState{
				OwnerUserID: state.OwnerUserID,
				BotUserID:   state.BotUserID,
			})
			return s.emitToolCall(state, "matrix.v1.messages.send_text", map[string]any{
				"room_id": state.RoomID,
				"body":    fmt.Sprintf("Created %s with password %s.", state.BotUsername, state.BotPassword),
			})
		case agentKindDefault:
			state.OwnerDMRoomID = roomID
			return s.emitToolCall(state, "matrix.v1.messages.send_text", map[string]any{
				"room_id": roomID,
				"body":    "Hi",
			})
		default:
			return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
		}
	case "matrix.v1.messages.send_text":
		state.PendingTool = ""
		state.PendingCallID = ""
		if state.AgentKind == agentKindDefault && state.OwnerDMRoomID != "" && !state.OwnerIntroSent {
			state.OwnerIntroSent = true
		}
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	case "onboarding.v1.user_agents.provision_initial":
		state.PendingTool = ""
		state.PendingCallID = ""

		provisioned, ok := decodeProvisionInitialOutput(output.Output)
		if !ok {
			return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
		}
		state.BotUserID = provisioned.BotUserID
		state.BotUsername = provisioned.BotUsername
		if provisioned.BotPassword != "" {
			state.BotPassword = provisioned.BotPassword
		}

		if provisioned.AlreadyExists {
			return s.emitToolCall(state, "matrix.v1.messages.send_text", map[string]any{
				"room_id": state.RoomID,
				"body":    fmt.Sprintf("You already have a default agent at %s.", state.BotUsername),
			})
		}
		return s.emitToolCall(state, "matrix.v1.rooms.create", map[string]any{
			"invite":    []string{state.BotUserID},
			"is_direct": true,
		})
	default:
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	}
}

func (s *mockResponsesServer) planOnboardingEvent(state responsesConversationState, update matrixRoomUpdateEnvelope) (string, map[string]any, responsesConversationState) {
	state.RoomID = update.RoomID
	entries := roomUpdateEntries(update)
	if roomIsDM, ok := classifyTwoPartyRoom(entries); ok {
		state.RoomIsDM = roomIsDM
	}
	if activation, ok := s.activationRoom(update.RoomID); ok {
		for _, entry := range entries {
			if entry.Event.Type == "m.room.member" &&
				stringValue(entry.Event.Content["membership"]) == "join" &&
				entry.Event.Sender == activation.BotUserID &&
				!activation.ActivationSent {
				activation.ActivationSent = true
				s.setActivationRoom(update.RoomID, activation)
				state.OwnerUserID = activation.OwnerUserID
				state.BotUserID = activation.BotUserID
				return s.emitToolCall(state, "matrix.v1.messages.send_text", map[string]any{
					"room_id": update.RoomID,
					"body":    activationMessage(activation.OwnerUserID),
				})
			}
		}
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	}
	if state.OwnerUserID == "" {
		if sender := latestExternalSender(entries); sender != "" {
			state.OwnerUserID = sender
		}
	}

	if invite, ok := firstInviteEntry(entries); ok {
		if !state.RoomIsDM {
			return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
		}
		state.TriggerEvent = invite.Event.EventID
		return s.emitToolCall(state, "matrix.v1.rooms.join", map[string]any{
			"room": update.RoomID,
		})
	}

	if !state.RoomIsDM {
		return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
	}

	if message, ok := latestMessageEntry(entries, wantsNewAgent); ok {
		state.OwnerUserID = message.Event.Sender
		state.TriggerEvent = message.Event.EventID
		return s.emitToolCall(state, "onboarding.v1.user_agents.provision_initial", map[string]any{
			"owner_matrix_user_id": state.OwnerUserID,
		})
	}

	return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
}

func (s *mockResponsesServer) planDefaultAgentEvent(state responsesConversationState, update matrixRoomUpdateEnvelope) (string, map[string]any, responsesConversationState) {
	state.RoomID = update.RoomID
	entries := roomUpdateEntries(update)
	if _, ok := firstInviteEntry(entries); ok {
		return s.emitToolCall(state, "matrix.v1.rooms.join", map[string]any{
			"room": update.RoomID,
		})
	}
	if message, ok := latestMessageEntry(entries, func(body string) bool { return activationOwnerUserID(body) != "" }); ok {
		if ownerUserID := activationOwnerUserID(messageBody(message.Event)); ownerUserID != "" {
			state.OwnerUserID = ownerUserID
			return s.emitToolCall(state, "matrix.v1.rooms.create", map[string]any{
				"invite":    []string{ownerUserID},
				"is_direct": true,
			})
		}
	}
	return s.nextResponseID(), assistantMessage(s.nextMessageID(), "ok"), state
}

func (s *mockResponsesServer) emitToolCall(state responsesConversationState, name string, args map[string]any) (string, map[string]any, responsesConversationState) {
	responseID := s.nextResponseID()
	callID := s.nextCallID()
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintf("encode tool args: %v", err))
	}
	state.PendingTool = name
	state.PendingCallID = callID
	emittedName := state.resolveToolName(name)
	return responseID, map[string]any{
		"id":        s.nextFunctionCallID(),
		"type":      "function_call",
		"call_id":   callID,
		"name":      emittedName,
		"arguments": string(encodedArgs),
		"status":    "completed",
	}, state
}

func (s *mockResponsesServer) stateFor(responseID string) responsesConversationState {
	if responseID == "" {
		return responsesConversationState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[responseID]
}

func (s *mockResponsesServer) stateForCall(callID string) responsesConversationState {
	if callID == "" {
		return responsesConversationState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[callID]
}

func (s *mockResponsesServer) activationRoom(roomID string) (activationRoomState, bool) {
	if strings.TrimSpace(roomID) == "" {
		return activationRoomState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.rooms[roomID]
	return state, ok
}

func (s *mockResponsesServer) setActivationRoom(roomID string, state activationRoomState) {
	if strings.TrimSpace(roomID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rooms[roomID] = state
}

func (s *mockResponsesServer) nextResponseID() string {
	return fmt.Sprintf("resp-%d", atomic.AddUint64(&s.counter, 1))
}

func (s *mockResponsesServer) nextMessageID() string {
	return fmt.Sprintf("msg-%d", atomic.AddUint64(&s.counter, 1))
}

func (s *mockResponsesServer) nextCallID() string {
	return fmt.Sprintf("call-%d", atomic.AddUint64(&s.counter, 1))
}

func (s *mockResponsesServer) nextFunctionCallID() string {
	return fmt.Sprintf("fc-%d", atomic.AddUint64(&s.counter, 1))
}

func assistantMessage(id, text string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{{
			"type": "output_text",
			"text": text,
		}},
	}
}

func detectAgentKind(toolNames map[string]string) string {
	for name := range toolNames {
		if strings.HasPrefix(name, "onboarding.v1.user_agents.") {
			return agentKindOnboarding
		}
	}
	return agentKindDefault
}

func detectAgentKindFromPayload(payload map[string]any, toolNames map[string]string) string {
	if kind := detectAgentKind(toolNames); kind == agentKindOnboarding {
		return kind
	}

	text := strings.ToLower(strings.Join(payloadInputTexts(payload["input"]), "\n"))
	if strings.Contains(text, "you are the onboarding agent") {
		return agentKindOnboarding
	}
	if strings.Contains(text, "you are created by") {
		return agentKindDefault
	}
	return detectAgentKind(toolNames)
}

func payloadInputTexts(raw any) []string {
	items, _ := raw.([]any)
	var texts []string
	for _, item := range items {
		message, _ := item.(map[string]any)
		if message == nil {
			continue
		}
		content, _ := message["content"].([]any)
		for _, part := range content {
			typedPart, _ := part.(map[string]any)
			if typedPart == nil || stringValue(typedPart["type"]) != "input_text" {
				continue
			}
			if text := stringValue(typedPart["text"]); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

func extractToolNames(raw any) []string {
	items, _ := raw.([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		tool, _ := item.(map[string]any)
		if tool == nil {
			continue
		}
		if name := stringValue(tool["name"]); name != "" {
			names = append(names, name)
			continue
		}
		function, _ := tool["function"].(map[string]any)
		if name := stringValue(function["name"]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func toolCatalogFromPayload(raw any) map[string]string {
	actualNames := extractToolNames(raw)
	if len(actualNames) == 0 {
		return nil
	}

	catalog := make(map[string]string, len(actualNames))
	for _, actual := range actualNames {
		canonical := canonicalToolName(actual)
		if canonical == "" {
			continue
		}
		catalog[canonical] = actual
	}
	if len(catalog) == 0 {
		return nil
	}
	return catalog
}

func canonicalToolName(name string) string {
	if name == "" {
		return ""
	}

	if strings.HasPrefix(name, "matrix.") || strings.HasPrefix(name, "onboarding.") {
		return name
	}

	trimmed := name
	if strings.HasPrefix(trimmed, "mcp__") {
		parts := strings.SplitN(trimmed, "__", 3)
		if len(parts) == 3 {
			trimmed = parts[2]
		}
	}

	switch trimmed {
	case "matrix_v1_rooms_create":
		return "matrix.v1.rooms.create"
	case "matrix_v1_rooms_join":
		return "matrix.v1.rooms.join"
	case "matrix_v1_messages_send_text":
		return "matrix.v1.messages.send_text"
	case "matrix_v1_rooms_invite":
		return "matrix.v1.rooms.invite"
	case "matrix_v1_rooms_leave":
		return "matrix.v1.rooms.leave"
	case "onboarding_v1_user_agents_provision_initial":
		return "onboarding.v1.user_agents.provision_initial"
	default:
		return ""
	}
}

func (s responsesConversationState) withToolCatalog(toolNames map[string]string) responsesConversationState {
	if len(toolNames) == 0 {
		return s
	}
	if s.ToolNames == nil {
		s.ToolNames = make(map[string]string, len(toolNames))
	}
	for canonical, actual := range toolNames {
		s.ToolNames[canonical] = actual
	}
	return s
}

func (s responsesConversationState) resolveToolName(canonical string) string {
	if actual := s.ToolNames[canonical]; actual != "" {
		return actual
	}
	return canonical
}

func latestFunctionCallOutput(raw any) (functionCallOutput, bool) {
	items, _ := raw.([]any)
	for idx := len(items) - 1; idx >= 0; idx-- {
		item, _ := items[idx].(map[string]any)
		if item == nil || stringValue(item["type"]) != "function_call_output" {
			continue
		}
		return functionCallOutput{
			CallID: stringValue(item["call_id"]),
			Output: item["output"],
		}, true
	}
	return functionCallOutput{}, false
}

func latestMatrixRoomUpdate(raw any) (matrixRoomUpdateEnvelope, bool) {
	items, _ := raw.([]any)
	for idx := len(items) - 1; idx >= 0; idx-- {
		item, _ := items[idx].(map[string]any)
		if item == nil || stringValue(item["type"]) != "message" || stringValue(item["role"]) != "user" {
			continue
		}
		content, _ := item["content"].([]any)
		for contentIdx := len(content) - 1; contentIdx >= 0; contentIdx-- {
			part, _ := content[contentIdx].(map[string]any)
			if part == nil || stringValue(part["type"]) != "input_text" {
				continue
			}
			text := stringValue(part["text"])
			var update matrixRoomUpdateEnvelope
			if json.Unmarshal([]byte(text), &update) == nil && update.Kind == "matrix_room_update" && update.RoomID != "" {
				return update, true
			}
		}
	}
	return matrixRoomUpdateEnvelope{}, false
}

func decodeProvisionInitialOutput(raw any) (provisionInitialOutput, bool) {
	raw = unwrapToolOutput(raw)
	var blob []byte
	switch typed := raw.(type) {
	case string:
		blob = []byte(typed)
	case map[string]any:
		var err error
		blob, err = json.Marshal(typed)
		if err != nil {
			return provisionInitialOutput{}, false
		}
	default:
		return provisionInitialOutput{}, false
	}

	var output provisionInitialOutput
	if json.Unmarshal(blob, &output) != nil {
		return provisionInitialOutput{}, false
	}
	if output.BotUserID == "" && output.BotUsername == "" {
		return provisionInitialOutput{}, false
	}
	return output, true
}

func latestRoomUpdateEntry(update matrixRoomUpdateEnvelope) (matrixRoomUpdateEntry, bool) {
	entries := roomUpdateEntries(update)
	if len(entries) == 0 {
		return matrixRoomUpdateEntry{}, false
	}
	return entries[len(entries)-1], true
}

func roomUpdateEntries(update matrixRoomUpdateEnvelope) []matrixRoomUpdateEntry {
	entries := make([]matrixRoomUpdateEntry, 0, len(update.Updates)*2)
	for _, segment := range update.Updates {
		for _, evt := range segment.State {
			entries = append(entries, matrixRoomUpdateEntry{
				RoomSection: segment.RoomSection,
				Event:       evt,
			})
		}
		for _, evt := range segment.Timeline {
			entries = append(entries, matrixRoomUpdateEntry{
				RoomSection: segment.RoomSection,
				Event:       evt,
			})
		}
	}
	return entries
}

func firstInviteEntry(entries []matrixRoomUpdateEntry) (matrixRoomUpdateEntry, bool) {
	for _, entry := range entries {
		if entry.RoomSection == "invite" && stringValue(entry.Event.Content["membership"]) == "invite" {
			return entry, true
		}
	}
	return matrixRoomUpdateEntry{}, false
}

func latestMessageEntry(entries []matrixRoomUpdateEntry, predicate func(string) bool) (matrixRoomUpdateEntry, bool) {
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if entry.Event.Type != "m.room.message" {
			continue
		}
		body := messageBody(entry.Event)
		if predicate(body) {
			return entry, true
		}
	}
	return matrixRoomUpdateEntry{}, false
}

func latestExternalSender(entries []matrixRoomUpdateEntry) string {
	for idx := len(entries) - 1; idx >= 0; idx-- {
		if sender := strings.TrimSpace(entries[idx].Event.Sender); sender != "" {
			return sender
		}
	}
	return ""
}

func classifyTwoPartyRoom(entries []matrixRoomUpdateEntry) (bool, bool) {
	members := make(map[string]string)
	for _, entry := range entries {
		if entry.Event.Type != "m.room.member" {
			continue
		}
		membership := strings.TrimSpace(stringValue(entry.Event.Content["membership"]))
		if membership == "" || membership == "leave" {
			continue
		}
		userID := strings.TrimSpace(entry.Event.StateKey)
		if userID == "" {
			userID = strings.TrimSpace(entry.Event.Sender)
		}
		if userID == "" {
			continue
		}
		members[userID] = membership
	}
	if len(members) == 0 {
		return false, false
	}
	return len(members) == 2, true
}

func messageBody(event matrixRoomUpdateEvt) string {
	return stringValue(event.Content["body"])
}

func wantsNewAgent(body string) bool {
	body = strings.ToLower(strings.TrimSpace(body))
	if body == "yes" || body == "yes please" {
		return true
	}
	return strings.Contains(body, "new agent")
}

func decodeRoomIDOutput(raw any) (string, bool) {
	raw = unwrapToolOutput(raw)
	var output map[string]any
	switch typed := raw.(type) {
	case string:
		if json.Unmarshal([]byte(typed), &output) != nil {
			return "", false
		}
	case map[string]any:
		output = typed
	default:
		return "", false
	}
	roomID := strings.TrimSpace(stringValue(output["room_id"]))
	if roomID == "" {
		return "", false
	}
	return roomID, true
}

func unwrapToolOutput(raw any) any {
	switch typed := raw.(type) {
	case map[string]any:
		if structured, ok := typed["structuredContent"]; ok {
			return unwrapToolOutput(structured)
		}
		if structured, ok := typed["structured_content"]; ok {
			return unwrapToolOutput(structured)
		}
		if content, ok := typed["content"].([]any); ok {
			for _, part := range content {
				partMap, _ := part.(map[string]any)
				if partMap == nil {
					continue
				}
				if text := stringValue(partMap["text"]); text != "" {
					return unwrapToolOutput(text)
				}
			}
		}
		return typed
	case string:
		var decoded any
		if json.Unmarshal([]byte(typed), &decoded) == nil {
			return unwrapToolOutput(decoded)
		}
		return typed
	default:
		return raw
	}
}

func activationMessage(ownerUserID string) string {
	return fmt.Sprintf("Send your creator %s a DM to introduce yourself.", ownerUserID)
}

func activationOwnerUserID(body string) string {
	const prefix = "Send your creator "
	const suffix = " a DM to introduce yourself."
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, prefix) || !strings.HasSuffix(body, suffix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body, prefix), suffix))
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func prettyJSON(value any) string {
	blob, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(blob)
}

func compactJSON(value any) string {
	blob, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(bytes.TrimSpace(blob))
}
