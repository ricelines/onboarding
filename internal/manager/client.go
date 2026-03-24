package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrAllowlistEntryMissing = errors.New("scenario source allowlist entry not present")
	ErrScenarioMissing       = errors.New("scenario not found")
)

type ExternalSlotBindingRequest struct {
	BindableServiceID string `json:"bindable_service_id"`
}

type ExportPublishRequest struct {
	Listen string `json:"listen"`
}

type ExportRequest struct {
	Publish *ExportPublishRequest `json:"publish,omitempty"`
}

type CreateScenarioRequest struct {
	SourceURL     string                                `json:"source_url"`
	RootConfig    map[string]any                        `json:"root_config"`
	ExternalSlots map[string]ExternalSlotBindingRequest `json:"external_slots"`
	Exports       map[string]ExportRequest              `json:"exports,omitempty"`
	Metadata      map[string]any                        `json:"metadata"`
	StoreBundle   bool                                  `json:"store_bundle"`
	Start         bool                                  `json:"start"`
}

type UpgradeScenarioRequest struct {
	SourceURL     *string                               `json:"source_url,omitempty"`
	RootConfig    map[string]any                        `json:"root_config,omitempty"`
	ExternalSlots map[string]ExternalSlotBindingRequest `json:"external_slots,omitempty"`
	Exports       map[string]ExportRequest              `json:"exports,omitempty"`
	Metadata      map[string]any                        `json:"metadata,omitempty"`
	StoreBundle   bool                                  `json:"store_bundle"`
}

type EnqueueOperationResponse struct {
	ScenarioID  string `json:"scenario_id"`
	OperationID string `json:"operation_id"`
}

type OperationStatusResponse struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	LastError   string `json:"last_error"`
}

type ScenarioSummaryResponse struct {
	ScenarioID    string         `json:"scenario_id"`
	SourceURL     string         `json:"source_url"`
	ObservedState string         `json:"observed_state"`
	Metadata      map[string]any `json:"metadata"`
}

type ExternalSlotBindingResponse struct {
	BindableServiceID  string `json:"bindable_service_id"`
	ProviderScenarioID string `json:"provider_scenario_id,omitempty"`
}

type ExportResponse struct {
	Publish           *ExportPublishRequest `json:"publish,omitempty"`
	BindableServiceID string                `json:"bindable_service_id,omitempty"`
	Available         bool                  `json:"available"`
}

type ScenarioDetailResponse struct {
	ScenarioID            string                                 `json:"scenario_id"`
	SourceURL             string                                 `json:"source_url"`
	DesiredState          string                                 `json:"desired_state"`
	ObservedState         string                                 `json:"observed_state"`
	Metadata              map[string]any                         `json:"metadata"`
	RootConfig            map[string]any                         `json:"root_config"`
	SecretRootConfigPaths []string                               `json:"secret_root_config_paths"`
	ExternalSlots         map[string]ExternalSlotBindingResponse `json:"external_slots"`
	Exports               map[string]ExportResponse              `json:"exports"`
	BundleStored          bool                                   `json:"bundle_stored"`
	LastError             string                                 `json:"last_error"`
}

type BindableServiceResponse struct {
	BindableServiceID string `json:"bindable_service_id"`
	DisplayName       string `json:"display_name,omitempty"`
	Available         bool   `json:"available"`
	ScenarioID        string `json:"scenario_id,omitempty"`
	Export            string `json:"export,omitempty"`
}

type allowlistEntryRequest struct {
	SourceURL string `json:"source_url"`
}

type apiError struct {
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("manager API returned %d: %s", e.StatusCode, e.Message)
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) RemoveAllowlistEntry(ctx context.Context, sourceURL string) error {
	err := c.postJSON(ctx, "/v1/manager/scenario-source-allowlist/remove", allowlistEntryRequest{
		SourceURL: sourceURL,
	}, nil)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return ErrAllowlistEntryMissing
	}
	return err
}

func (c *Client) ListBindableServices(ctx context.Context) ([]BindableServiceResponse, error) {
	var services []BindableServiceResponse
	if err := c.getJSON(ctx, "/v1/bindable-services", &services); err != nil {
		return nil, err
	}
	return services, nil
}

func (c *Client) CreateScenario(ctx context.Context, request CreateScenarioRequest) (EnqueueOperationResponse, error) {
	if request.ExternalSlots == nil {
		request.ExternalSlots = map[string]ExternalSlotBindingRequest{}
	}
	var response EnqueueOperationResponse
	if err := c.postJSON(ctx, "/v1/scenarios", request, &response); err != nil {
		return EnqueueOperationResponse{}, err
	}
	return response, nil
}

func (c *Client) UpgradeScenario(ctx context.Context, scenarioID string, request UpgradeScenarioRequest) (EnqueueOperationResponse, error) {
	var response EnqueueOperationResponse
	if err := c.postJSON(ctx, "/v1/scenarios/"+scenarioID+"/upgrade", request, &response); err != nil {
		return EnqueueOperationResponse{}, err
	}
	return response, nil
}

func (c *Client) GetOperation(ctx context.Context, operationID string) (OperationStatusResponse, error) {
	var response OperationStatusResponse
	if err := c.getJSON(ctx, "/v1/operations/"+operationID, &response); err != nil {
		return OperationStatusResponse{}, err
	}
	return response, nil
}

func (c *Client) ListScenarios(ctx context.Context) ([]ScenarioSummaryResponse, error) {
	var scenarios []ScenarioSummaryResponse
	if err := c.getJSON(ctx, "/v1/scenarios", &scenarios); err != nil {
		return nil, err
	}
	return scenarios, nil
}

func (c *Client) GetScenario(ctx context.Context, scenarioID string) (ScenarioDetailResponse, error) {
	var response ScenarioDetailResponse
	if err := c.getJSON(ctx, "/v1/scenarios/"+scenarioID, &response); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return ScenarioDetailResponse{}, ErrScenarioMissing
		}
		return ScenarioDetailResponse{}, err
	}
	return response, nil
}

func (c *Client) postJSON(ctx context.Context, path string, request any, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	return decodeResponse(resp, response)
}

func (c *Client) getJSON(ctx context.Context, path string, response any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	return decodeResponse(resp, response)
}

func decodeResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		var payload struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error) != "" {
			message = payload.Error
		}
		return &apiError{StatusCode: resp.StatusCode, Message: message}
	}

	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
