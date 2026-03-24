package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	BootstrapAdminUserID             string `json:"bootstrap_admin_user_id,omitempty"`
	OnboardingBotUserID              string `json:"onboarding_bot_user_id,omitempty"`
	WelcomeRoomID                    string `json:"welcome_room_id,omitempty"`
	SharedResponsesBindableServiceID string `json:"shared_responses_bindable_service_id,omitempty"`
	AuthProxyScenarioID              string `json:"auth_proxy_scenario_id,omitempty"`
	ProvisionerScenarioID            string `json:"provisioner_scenario_id,omitempty"`
	OnboardingScenarioID             string `json:"onboarding_scenario_id,omitempty"`
	UpdatedAt                        string `json:"updated_at,omitempty"`
}

func LoadState(path string) (State, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read bootstrap state: %w", err)
	}
	if len(blob) == 0 {
		return State{}, nil
	}
	var state State
	if err := json.Unmarshal(blob, &state); err != nil {
		return State{}, fmt.Errorf("decode bootstrap state: %w", err)
	}
	return state, nil
}

func SaveState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create bootstrap state dir: %w", err)
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	blob, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode bootstrap state: %w", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o600); err != nil {
		return fmt.Errorf("write bootstrap state: %w", err)
	}
	return nil
}
