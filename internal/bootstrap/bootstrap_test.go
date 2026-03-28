package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
)

func TestEnsureManagedScenarioUsesPersistedScenarioID(t *testing.T) {
	t.Parallel()

	req := ensureScenarioRequest{
		Kind:               metadataKindProvisioner,
		ExistingScenarioID: "scn_persisted",
		SourceURL:          "file:///scenario.json5",
		RootConfig: map[string]any{
			"foo": "bar",
		},
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"matrix": {BindableServiceID: "svc_matrix"},
		},
		Metadata: map[string]any{
			"kind": metadataKindProvisioner,
		},
	}

	detail := managerclient.ScenarioDetailResponse{
		ScenarioID:    "scn_persisted",
		SourceURL:     req.SourceURL,
		ObservedState: "running",
		Metadata:      req.Metadata,
		RootConfig:    req.RootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingResponse{
			"matrix": {BindableServiceID: "svc_matrix"},
		},
		BundleStored: true,
	}

	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.Method+" "+r.URL.Path]++
		mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios/scn_persisted":
			writeJSON(t, w, detail)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios":
			t.Fatalf("ListScenarios should not be called when persisted scenario ID is valid")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/scenarios":
			t.Fatalf("CreateScenario should not be called when persisted scenario ID is valid")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/scenarios/scn_persisted/upgrade":
			t.Fatalf("UpgradeScenario should not be called when scenario already matches")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	runner := &Runner{manager: managerclient.NewClient(server.URL)}
	scenarioID, err := runner.ensureManagedScenario(context.Background(), req)
	if err != nil {
		t.Fatalf("ensureManagedScenario() error = %v", err)
	}
	if scenarioID != "scn_persisted" {
		t.Fatalf("scenarioID = %q, want scn_persisted", scenarioID)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts["GET /v1/scenarios/scn_persisted"] != 2 {
		t.Fatalf("GET /v1/scenarios/scn_persisted count = %d, want 2", counts["GET /v1/scenarios/scn_persisted"])
	}
}

func TestEnsureManagedScenarioAcceptsProviderScenarioWhenBindableServiceIDDrifts(t *testing.T) {
	t.Parallel()

	req := ensureScenarioRequest{
		Kind:               metadataKindOnboarding,
		ExistingScenarioID: "scn_existing",
		SourceURL:          "file:///scenario.json5",
		RootConfig: map[string]any{
			"foo": "bar",
		},
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"provisioning_mcp": {BindableServiceID: "svc_new"},
		},
		ExternalSlotProviderScenarios: map[string]string{
			"provisioning_mcp": "scn_provider",
		},
		Metadata: map[string]any{
			"kind": metadataKindOnboarding,
		},
	}

	detail := managerclient.ScenarioDetailResponse{
		ScenarioID:    "scn_existing",
		SourceURL:     req.SourceURL,
		ObservedState: "running",
		Metadata:      req.Metadata,
		RootConfig:    req.RootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingResponse{
			"provisioning_mcp": {
				BindableServiceID:  "svc_old",
				ProviderScenarioID: "scn_provider",
			},
		},
		BundleStored: true,
	}

	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.Method+" "+r.URL.Path]++
		mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios/scn_existing":
			writeJSON(t, w, detail)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/scenarios/scn_existing/upgrade":
			t.Fatalf("UpgradeScenario should not be called when provider scenario still matches")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	runner := &Runner{manager: managerclient.NewClient(server.URL)}
	scenarioID, err := runner.ensureManagedScenario(context.Background(), req)
	if err != nil {
		t.Fatalf("ensureManagedScenario() error = %v", err)
	}
	if scenarioID != "scn_existing" {
		t.Fatalf("scenarioID = %q, want scn_existing", scenarioID)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts["GET /v1/scenarios/scn_existing"] != 2 {
		t.Fatalf("GET /v1/scenarios/scn_existing count = %d, want 2", counts["GET /v1/scenarios/scn_existing"])
	}
}

func TestEnsureManagedScenarioFallsBackWhenPersistedScenarioIDIsMissing(t *testing.T) {
	t.Parallel()

	req := ensureScenarioRequest{
		Kind:               metadataKindProvisioner,
		ExistingScenarioID: "scn_stale",
		SourceURL:          "file:///scenario.json5",
		RootConfig: map[string]any{
			"foo": "bar",
		},
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"matrix": {BindableServiceID: "svc_matrix"},
		},
		Metadata: map[string]any{
			"kind": metadataKindProvisioner,
		},
	}

	detail := managerclient.ScenarioDetailResponse{
		ScenarioID:    "scn_live",
		SourceURL:     req.SourceURL,
		ObservedState: "running",
		Metadata:      req.Metadata,
		RootConfig:    req.RootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingResponse{
			"matrix": {BindableServiceID: "svc_matrix"},
		},
		BundleStored: true,
	}

	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.Method+" "+r.URL.Path]++
		mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios/scn_stale":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"missing"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios":
			writeJSON(t, w, []managerclient.ScenarioSummaryResponse{{
				ScenarioID:    "scn_live",
				SourceURL:     req.SourceURL,
				ObservedState: "running",
				Metadata:      req.Metadata,
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios/scn_live":
			writeJSON(t, w, detail)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/scenarios":
			t.Fatalf("CreateScenario should not be called when kind lookup finds a live scenario")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/scenarios/scn_live/upgrade":
			t.Fatalf("UpgradeScenario should not be called when recovered scenario already matches")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	runner := &Runner{manager: managerclient.NewClient(server.URL)}
	scenarioID, err := runner.ensureManagedScenario(context.Background(), req)
	if err != nil {
		t.Fatalf("ensureManagedScenario() error = %v", err)
	}
	if scenarioID != "scn_live" {
		t.Fatalf("scenarioID = %q, want scn_live", scenarioID)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts["GET /v1/scenarios/scn_stale"] != 1 {
		t.Fatalf("GET /v1/scenarios/scn_stale count = %d, want 1", counts["GET /v1/scenarios/scn_stale"])
	}
	if counts["GET /v1/scenarios"] != 1 {
		t.Fatalf("GET /v1/scenarios count = %d, want 1", counts["GET /v1/scenarios"])
	}
	if counts["GET /v1/scenarios/scn_live"] != 2 {
		t.Fatalf("GET /v1/scenarios/scn_live count = %d, want 2", counts["GET /v1/scenarios/scn_live"])
	}
}

func TestEnsureManagedScenarioUpgradeOmitsUnchangedSourceURL(t *testing.T) {
	t.Parallel()

	req := ensureScenarioRequest{
		Kind:               metadataKindProvisioner,
		ExistingScenarioID: "scn_existing",
		SourceURL:          "file:///scenario.json5",
		RootConfig: map[string]any{
			"foo": "bar",
		},
		ExternalSlots: map[string]managerclient.ExternalSlotBindingRequest{
			"matrix": {BindableServiceID: "svc_matrix"},
		},
		Metadata: map[string]any{
			"kind": "updated-kind",
		},
	}

	detail := managerclient.ScenarioDetailResponse{
		ScenarioID:    "scn_existing",
		SourceURL:     req.SourceURL,
		ObservedState: "starting",
		Metadata: map[string]any{
			"kind": metadataKindProvisioner,
		},
		RootConfig: req.RootConfig,
		ExternalSlots: map[string]managerclient.ExternalSlotBindingResponse{
			"matrix": {BindableServiceID: "svc_matrix"},
		},
		BundleStored: true,
	}

	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		mu.Lock()
		counts[key]++
		callCount := counts[key]
		mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/scenarios/scn_existing":
			if callCount == 1 {
				writeJSON(t, w, detail)
				return
			}
			writeJSON(t, w, managerclient.ScenarioDetailResponse{
				ScenarioID:    "scn_existing",
				SourceURL:     req.SourceURL,
				ObservedState: "running",
				Metadata:      req.Metadata,
				RootConfig:    req.RootConfig,
				ExternalSlots: map[string]managerclient.ExternalSlotBindingResponse{
					"matrix": {BindableServiceID: "svc_matrix"},
				},
				BundleStored: true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/scenarios/scn_existing/upgrade":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read upgrade request: %v", err)
			}
			var payload managerclient.UpgradeScenarioRequest
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode upgrade request: %v", err)
			}
			if payload.SourceURL != nil {
				t.Fatalf("upgrade source_url = %q, want omitted for unchanged source", *payload.SourceURL)
			}
			writeJSON(t, w, managerclient.EnqueueOperationResponse{
				ScenarioID:  "scn_existing",
				OperationID: "op_upgrade",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/op_upgrade":
			writeJSON(t, w, managerclient.OperationStatusResponse{
				OperationID: "op_upgrade",
				Status:      "succeeded",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	runner := &Runner{manager: managerclient.NewClient(server.URL)}
	scenarioID, err := runner.ensureManagedScenario(context.Background(), req)
	if err != nil {
		t.Fatalf("ensureManagedScenario() error = %v", err)
	}
	if scenarioID != "scn_existing" {
		t.Fatalf("scenarioID = %q, want scn_existing", scenarioID)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts["POST /v1/scenarios/scn_existing/upgrade"] != 1 {
		t.Fatalf("upgrade count = %d, want 1", counts["POST /v1/scenarios/scn_existing/upgrade"])
	}
}

func TestRootConfigMapsEqualIgnoresSecretPaths(t *testing.T) {
	t.Parallel()

	current := map[string]any{
		"matrix_username": "onboarding",
		"model":           "mock-model",
	}
	want := map[string]any{
		"matrix_username": "onboarding",
		"matrix_password": "secret-pass",
		"model":           "mock-model",
	}

	if !rootConfigMapsEqual(current, []string{"matrix_password"}, want) {
		t.Fatal("rootConfigMapsEqual() should ignore secret root-config paths")
	}
}

func TestEnsureWelcomeRoomCreatesAndPublishesPublicRoom(t *testing.T) {
	t.Parallel()

	var createReq mautrix.ReqCreateRoom
	var directoryReq map[string]string
	var joinRulesReq map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodedPath, err := url.PathUnescape(r.URL.Path)
		if err != nil {
			t.Fatalf("decode request path: %v", err)
		}

		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(decodedPath, "/directory/room/#welcome:test"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"missing"}`))
		case r.Method == http.MethodPost && decodedPath == "/_matrix/client/v3/createRoom":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read create room request: %v", err)
			}
			if err := json.Unmarshal(body, &createReq); err != nil {
				t.Fatalf("decode create room request: %v", err)
			}
			writeJSON(t, w, map[string]string{"room_id": "!welcome:test"})
		case r.Method == http.MethodPut && decodedPath == "/_matrix/client/v3/rooms/!welcome:test/state/m.room.join_rules/":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read join rules request: %v", err)
			}
			if err := json.Unmarshal(body, &joinRulesReq); err != nil {
				t.Fatalf("decode join rules request: %v", err)
			}
			writeJSON(t, w, map[string]string{"event_id": "$joinrules"})
		case r.Method == http.MethodPut && decodedPath == "/_matrix/client/v3/directory/list/room/!welcome:test":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read directory request: %v", err)
			}
			if err := json.Unmarshal(body, &directoryReq); err != nil {
				t.Fatalf("decode directory request: %v", err)
			}
			writeJSON(t, w, map[string]string{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, decodedPath)
		}
	}))
	defer server.Close()

	adminClient, err := mautrix.NewClient(server.URL, "", "")
	if err != nil {
		t.Fatalf("new matrix client: %v", err)
	}
	runner := &Runner{cfg: Config{
		WelcomeRoomAliasLocalpart: "welcome",
		MatrixServerName:          "test",
	}}

	roomID, err := runner.ensureWelcomeRoom(context.Background(), adminClient, State{})
	if err != nil {
		t.Fatalf("ensureWelcomeRoom() error = %v", err)
	}
	if roomID != "!welcome:test" {
		t.Fatalf("roomID = %q, want !welcome:test", roomID)
	}
	if createReq.Visibility != "public" {
		t.Fatalf("create room visibility = %q, want public", createReq.Visibility)
	}
	if createReq.Preset != "public_chat" {
		t.Fatalf("create room preset = %q, want public_chat", createReq.Preset)
	}
	if createReq.RoomAliasName != "welcome" {
		t.Fatalf("create room alias = %q, want welcome", createReq.RoomAliasName)
	}
	if joinRulesReq["join_rule"] != "public" {
		t.Fatalf("join rule = %q, want public", joinRulesReq["join_rule"])
	}
	if directoryReq["visibility"] != "public" {
		t.Fatalf("directory visibility = %q, want public", directoryReq["visibility"])
	}
}

func TestEnsureWelcomeRoomMakesExistingRoomPublic(t *testing.T) {
	t.Parallel()

	var createRoomCalled bool
	var directoryReq map[string]string
	var joinRulesReq map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodedPath, err := url.PathUnescape(r.URL.Path)
		if err != nil {
			t.Fatalf("decode request path: %v", err)
		}

		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(decodedPath, "/directory/room/#welcome:test"):
			writeJSON(t, w, map[string]any{
				"room_id": "!welcome:test",
				"servers": []string{"test"},
			})
		case r.Method == http.MethodPost && decodedPath == "/_matrix/client/v3/createRoom":
			createRoomCalled = true
			t.Fatalf("CreateRoom should not be called when the welcome alias already exists")
		case r.Method == http.MethodPut && decodedPath == "/_matrix/client/v3/rooms/!welcome:test/state/m.room.join_rules/":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read join rules request: %v", err)
			}
			if err := json.Unmarshal(body, &joinRulesReq); err != nil {
				t.Fatalf("decode join rules request: %v", err)
			}
			writeJSON(t, w, map[string]string{"event_id": "$joinrules"})
		case r.Method == http.MethodPut && decodedPath == "/_matrix/client/v3/directory/list/room/!welcome:test":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read directory request: %v", err)
			}
			if err := json.Unmarshal(body, &directoryReq); err != nil {
				t.Fatalf("decode directory request: %v", err)
			}
			writeJSON(t, w, map[string]string{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, decodedPath)
		}
	}))
	defer server.Close()

	adminClient, err := mautrix.NewClient(server.URL, "", "")
	if err != nil {
		t.Fatalf("new matrix client: %v", err)
	}
	runner := &Runner{cfg: Config{
		WelcomeRoomAliasLocalpart: "welcome",
		MatrixServerName:          "test",
	}}

	roomID, err := runner.ensureWelcomeRoom(context.Background(), adminClient, State{})
	if err != nil {
		t.Fatalf("ensureWelcomeRoom() error = %v", err)
	}
	if roomID != id.RoomID("!welcome:test") {
		t.Fatalf("roomID = %q, want !welcome:test", roomID)
	}
	if createRoomCalled {
		t.Fatal("CreateRoom was called unexpectedly")
	}
	if joinRulesReq["join_rule"] != "public" {
		t.Fatalf("join rule = %q, want public", joinRulesReq["join_rule"])
	}
	if directoryReq["visibility"] != "public" {
		t.Fatalf("directory visibility = %q, want public", directoryReq["visibility"])
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
