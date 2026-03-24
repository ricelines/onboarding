package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	var baseURL string
	var taskID string

	flag.StringVar(&baseURL, "base-url", "", "A2A agent base URL")
	flag.StringVar(&taskID, "task-id", "", "A2A task ID")
	flag.Parse()

	if baseURL == "" || taskID == "" {
		log.Fatal("--base-url and --task-id are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	requestBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "onboarding-a2a-get-task",
		"method":  "tasks/get",
		"params": map[string]any{
			"id": taskID,
		},
	})
	if err != nil {
		log.Fatalf("marshal request: %v", err)
	}

	invokeURL := strings.TrimRight(baseURL, "/") + "/invoke"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, invokeURL, bytes.NewReader(requestBody))
	if err != nil {
		log.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("perform request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("read response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Fatalf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		log.Fatalf("decode response envelope: %v\n%s", err, string(body))
	}
	if envelope.Error != nil {
		log.Fatalf("rpc error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		log.Fatalf("rpc response did not include result: %s", string(body))
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(json.RawMessage(envelope.Result)); err != nil {
		log.Fatalf("write task: %v", err)
	}
}
