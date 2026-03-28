package e2e

import "testing"

func TestCanonicalToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain matrix tool",
			in:   "matrix.v1.rooms.join",
			want: "matrix.v1.rooms.join",
		},
		{
			name: "prefixed matrix create room tool",
			in:   "mcp__0__matrix_v1_rooms_create",
			want: "matrix.v1.rooms.create",
		},
		{
			name: "prefixed matrix tool",
			in:   "mcp__0__matrix_v1_rooms_join",
			want: "matrix.v1.rooms.join",
		},
		{
			name: "prefixed onboarding tool",
			in:   "mcp__1__onboarding_v1_user_agents_provision_initial",
			want: "onboarding.v1.user_agents.provision_initial",
		},
		{
			name: "unknown tool",
			in:   "mcp__0__unknown_tool",
			want: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := canonicalToolName(tc.in); got != tc.want {
				t.Fatalf("canonicalToolName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestToolCatalogFromPayload(t *testing.T) {
	t.Parallel()

	payloadTools := []any{
		map[string]any{"name": "mcp__0__matrix_v1_rooms_create"},
		map[string]any{"name": "mcp__0__matrix_v1_rooms_join"},
		map[string]any{"name": "mcp__1__onboarding_v1_user_agents_provision_initial"},
	}

	got := toolCatalogFromPayload(payloadTools)
	if got["matrix.v1.rooms.create"] != "mcp__0__matrix_v1_rooms_create" {
		t.Fatalf("rooms.create mapping = %#v", got)
	}
	if got["matrix.v1.rooms.join"] != "mcp__0__matrix_v1_rooms_join" {
		t.Fatalf("rooms.join mapping = %#v", got)
	}
	if got["onboarding.v1.user_agents.provision_initial"] != "mcp__1__onboarding_v1_user_agents_provision_initial" {
		t.Fatalf("provision_initial mapping = %#v", got)
	}
}

func TestDetectAgentKindFromPayloadFallsBackToDeveloperPrompt(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "developer",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": "You are the onboarding agent for a Matrix homeserver.",
					},
				},
			},
		},
	}

	if got := detectAgentKindFromPayload(payload, map[string]string{"matrix.v1.rooms.join": "mcp__0__matrix_v1_rooms_join"}); got != agentKindOnboarding {
		t.Fatalf("detectAgentKindFromPayload(...) = %q, want %q", got, agentKindOnboarding)
	}
}

func TestEmitToolCallUsesAdvertisedToolName(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{}
	state := responsesConversationState{
		ToolNames: map[string]string{
			"matrix.v1.rooms.join": "mcp__0__matrix_v1_rooms_join",
		},
	}

	_, item, nextState := server.emitToolCall(state, "matrix.v1.rooms.join", map[string]any{"room": "!dm:test"})
	if got, _ := item["name"].(string); got != "mcp__0__matrix_v1_rooms_join" {
		t.Fatalf("emitted tool name = %q, want %q", got, "mcp__0__matrix_v1_rooms_join")
	}
	if nextState.PendingTool != "matrix.v1.rooms.join" {
		t.Fatalf("pending tool = %q, want canonical name", nextState.PendingTool)
	}
}

func TestPlanResponseFallsBackToCallState(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{
		states: make(map[string]responsesConversationState),
		calls:  make(map[string]responsesConversationState),
	}
	server.calls["call-join"] = responsesConversationState{
		AgentKind:     agentKindOnboarding,
		RoomID:        "!dm:test",
		RoomIsDM:      true,
		PendingTool:   "matrix.v1.rooms.join",
		PendingCallID: "call-join",
		ToolNames: map[string]string{
			"matrix.v1.messages.send_text": "mcp__0__matrix_v1_messages_send_text",
		},
	}

	payload := map[string]any{
		"input": []any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call-join",
				"output":  map[string]any{"room_id": "!dm:test"},
			},
		},
		"tools": []any{
			map[string]any{"name": "mcp__0__matrix_v1_messages_send_text"},
			map[string]any{"name": "mcp__1__onboarding_v1_user_agents_provision_initial"},
		},
	}

	_, item, state := server.planResponse(payload)
	if got, _ := item["name"].(string); got != "mcp__0__matrix_v1_messages_send_text" {
		t.Fatalf("follow-up tool name = %q, want %q", got, "mcp__0__matrix_v1_messages_send_text")
	}
	if state.PendingTool != "matrix.v1.messages.send_text" {
		t.Fatalf("pending tool after join = %q, want %q", state.PendingTool, "matrix.v1.messages.send_text")
	}
}

func TestPlanResponseIgnoresStaleFunctionOutputWhenNewEventArrives(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{
		states: make(map[string]responsesConversationState),
		calls:  make(map[string]responsesConversationState),
	}
	server.calls["call-old"] = responsesConversationState{
		AgentKind:     agentKindOnboarding,
		RoomID:        "!dm:test",
		RoomIsDM:      true,
		PendingTool:   "",
		PendingCallID: "",
		ToolNames: map[string]string{
			"onboarding.v1.user_agents.provision_initial": "mcp__1__onboarding_v1_user_agents_provision_initial",
		},
	}

	payload := map[string]any{
		"input": []any{
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call-old",
				"output":  map[string]any{"event_id": "$welcome"},
			},
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": `{"kind":"matrix_room_update","room_id":"!dm:test","updates":[{"room_section":"join","state":[{"event_id":"$owner","room_id":"!dm:test","sender":"@owner:test","state_key":"@owner:test","type":"m.room.member","content":{"membership":"join"}},{"event_id":"$bot","room_id":"!dm:test","sender":"@onboarding:test","state_key":"@onboarding:test","type":"m.room.member","content":{"membership":"join"}}],"timeline":[{"event_id":"$yes","room_id":"!dm:test","sender":"@owner:test","type":"m.room.message","content":{"body":"yes"}}]}]}`,
					},
				},
			},
		},
		"tools": []any{
			map[string]any{"name": "mcp__1__onboarding_v1_user_agents_provision_initial"},
		},
	}

	_, item, state := server.planResponse(payload)
	if got, _ := item["name"].(string); got != "mcp__1__onboarding_v1_user_agents_provision_initial" {
		t.Fatalf("emitted tool name = %q, want provisioning tool", got)
	}
	if state.PendingTool != "onboarding.v1.user_agents.provision_initial" {
		t.Fatalf("pending tool = %q, want provisioning tool", state.PendingTool)
	}
}

func TestRecordStateRemovesConsumedCall(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{
		states: make(map[string]responsesConversationState),
		calls: map[string]responsesConversationState{
			"call-old": {
				AgentKind:     agentKindOnboarding,
				PendingTool:   "matrix.v1.messages.send_text",
				PendingCallID: "call-old",
			},
		},
	}

	server.recordState("resp-next", responsesConversationState{
		AgentKind: agentKindOnboarding,
	}, "call-old")

	if _, ok := server.calls["call-old"]; ok {
		t.Fatal("consumed call remained in call-state map")
	}
	if _, ok := server.states["resp-next"]; !ok {
		t.Fatal("next response state was not stored")
	}
}

func TestPlanResponseFallsBackToRoomStateWithoutPreviousResponseID(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{
		states:     make(map[string]responsesConversationState),
		roomStates: make(map[string]responsesConversationState),
		calls:      make(map[string]responsesConversationState),
	}
	server.roomStates[roomConversationKey(agentKindOnboarding, "!dm:test")] = responsesConversationState{
		AgentKind:   agentKindOnboarding,
		RoomID:      "!dm:test",
		RoomIsDM:    true,
		OwnerUserID: "@owner:test",
		ToolNames: map[string]string{
			"onboarding.v1.user_agents.provision_initial": "mcp__1__onboarding_v1_user_agents_provision_initial",
		},
	}

	payload := map[string]any{
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": `{"kind":"matrix_room_update","room_id":"!dm:test","updates":[{"room_section":"join","timeline":[{"event_id":"$yes","room_id":"!dm:test","sender":"@owner:test","type":"m.room.message","content":{"body":"yes"}}]}]}`,
					},
				},
			},
		},
		"tools": []any{
			map[string]any{"name": "mcp__1__onboarding_v1_user_agents_provision_initial"},
		},
	}

	_, item, state := server.planResponse(payload)
	if got, _ := item["name"].(string); got != "mcp__1__onboarding_v1_user_agents_provision_initial" {
		t.Fatalf("emitted tool name = %q, want provisioning tool", got)
	}
	if state.PendingTool != "onboarding.v1.user_agents.provision_initial" {
		t.Fatalf("pending tool = %q, want provisioning tool", state.PendingTool)
	}
}

func TestPlanResponseIgnoresOnboardingRequestInNonDMRoom(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{
		states: make(map[string]responsesConversationState),
		calls:  make(map[string]responsesConversationState),
	}

	payload := map[string]any{
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": `{"kind":"matrix_room_update","room_id":"!room:test","updates":[{"room_section":"join","state":[{"event_id":"$owner","room_id":"!room:test","sender":"@owner:test","state_key":"@owner:test","type":"m.room.member","content":{"membership":"join"}},{"event_id":"$bot","room_id":"!room:test","sender":"@onboarding:test","state_key":"@onboarding:test","type":"m.room.member","content":{"membership":"join"}},{"event_id":"$other","room_id":"!room:test","sender":"@other:test","state_key":"@other:test","type":"m.room.member","content":{"membership":"join"}}],"timeline":[{"event_id":"$msg","room_id":"!room:test","sender":"@owner:test","type":"m.room.message","content":{"body":"I want a new agent"}}]}]}`,
					},
				},
			},
		},
		"tools": []any{
			map[string]any{"name": "mcp__1__onboarding_v1_user_agents_provision_initial"},
		},
	}

	_, item, state := server.planResponse(payload)
	if got, _ := item["type"].(string); got != "message" {
		t.Fatalf("response item type = %q, want message", got)
	}
	if state.PendingTool != "" {
		t.Fatalf("pending tool = %q, want none", state.PendingTool)
	}
	if state.RoomIsDM {
		t.Fatal("non-DM room was marked as direct message")
	}
}

func TestPlanResponseIgnoresInviteToNonDMRoom(t *testing.T) {
	t.Parallel()

	server := &mockResponsesServer{
		states: make(map[string]responsesConversationState),
		calls:  make(map[string]responsesConversationState),
	}

	payload := map[string]any{
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": `{"kind":"matrix_room_update","room_id":"!room:test","updates":[{"room_section":"invite","state":[{"event_id":"$owner","room_id":"!room:test","sender":"@owner:test","state_key":"@owner:test","type":"m.room.member","content":{"membership":"join"}},{"event_id":"$other","room_id":"!room:test","sender":"@other:test","state_key":"@other:test","type":"m.room.member","content":{"membership":"join"}},{"event_id":"$invite","room_id":"!room:test","sender":"@owner:test","state_key":"@onboarding:test","type":"m.room.member","content":{"membership":"invite"}}]}]}`,
					},
				},
			},
		},
		"tools": []any{
			map[string]any{"name": "mcp__1__onboarding_v1_user_agents_provision_initial"},
			map[string]any{"name": "mcp__0__matrix_v1_rooms_join"},
		},
	}

	_, item, state := server.planResponse(payload)
	if got, _ := item["type"].(string); got != "message" {
		t.Fatalf("response item type = %q, want message", got)
	}
	if state.PendingTool != "" {
		t.Fatalf("pending tool = %q, want none", state.PendingTool)
	}
	if state.RoomIsDM {
		t.Fatal("non-DM invite was marked as direct message")
	}
}

func TestActivationOwnerUserID(t *testing.T) {
	t.Parallel()

	body := activationMessage("@alice:test")
	if got := activationOwnerUserID(body); got != "@alice:test" {
		t.Fatalf("activationOwnerUserID(%q) = %q", body, got)
	}
	if got := activationOwnerUserID("hello"); got != "" {
		t.Fatalf("activationOwnerUserID(non-activation) = %q, want empty", got)
	}
}

func TestDecodeRoomIDOutput(t *testing.T) {
	t.Parallel()

	if got, ok := decodeRoomIDOutput(map[string]any{"room_id": "!room:test"}); !ok || got != "!room:test" {
		t.Fatalf("decodeRoomIDOutput(map) = (%q, %v)", got, ok)
	}
	if got, ok := decodeRoomIDOutput(`{"room_id":"!room:test"}`); !ok || got != "!room:test" {
		t.Fatalf("decodeRoomIDOutput(string) = (%q, %v)", got, ok)
	}
	if got, ok := decodeRoomIDOutput("not json"); ok || got != "" {
		t.Fatalf("decodeRoomIDOutput(invalid) = (%q, %v), want empty/false", got, ok)
	}
	if got, ok := decodeRoomIDOutput(map[string]any{
		"structuredContent": map[string]any{"room_id": "!room:test"},
	}); !ok || got != "!room:test" {
		t.Fatalf("decodeRoomIDOutput(structuredContent) = (%q, %v)", got, ok)
	}
}

func TestDecodeProvisionInitialOutputStructuredContent(t *testing.T) {
	t.Parallel()

	got, ok := decodeProvisionInitialOutput(map[string]any{
		"structuredContent": map[string]any{
			"created":        true,
			"scenario_id":    "scn_test",
			"bot_user_id":    "@bot:test",
			"bot_username":   "bot",
			"bot_password":   "pass-bot",
			"already_exists": false,
		},
	})
	if !ok {
		t.Fatal("decodeProvisionInitialOutput(structuredContent) = not ok")
	}
	if got.BotUserID != "@bot:test" || got.BotUsername != "bot" || got.BotPassword != "pass-bot" {
		t.Fatalf("decodeProvisionInitialOutput(structuredContent) = %#v", got)
	}
}
