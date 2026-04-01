package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultUserAgentDeveloperPromptCoversHistoryRecovery(t *testing.T) {
	t.Parallel()

	body := readModuleFile(t, "prompts/default-user-agent-developer-instructions.md")
	for _, needle := range []string{
		"Do not rely on knowing why the context is incomplete",
		"matrix.v1.rooms.list",
		"matrix.v1.timeline.messages.list",
		"Do not claim that you lack prior DM or channel context",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("developer prompt missing %q", needle)
		}
	}
}

func TestDefaultUserAgentDeveloperPromptCoversTurnTakingAndEdits(t *testing.T) {
	t.Parallel()

	body := readModuleFile(t, "prompts/default-user-agent-developer-instructions.md")
	for _, needle := range []string{
		"normalize the raw batch into logical conversational acts",
		"A Matrix edit usually replaces an earlier utterance",
		"You are not required to reply to every batch or every message",
		"human-like participant in the chat, not an obsequious chatbot",
		"If the user asks for brevity, be brief",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("developer prompt missing %q", needle)
		}
	}
}

func TestDefaultUserAgentAgentsInstructionsCoverHistoryRecovery(t *testing.T) {
	t.Parallel()

	body := readModuleFile(t, "agents/default-user-agent.md")
	for _, needle := range []string{
		"source of truth for earlier conversation state",
		"whenever your immediate context is incomplete",
		"inspect Matrix first",
		"matrix.v1.rooms.list",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("AGENTS instructions missing %q", needle)
		}
	}
}

func TestDefaultUserAgentAgentsInstructionsCoverTurnTakingAndEdits(t *testing.T) {
	t.Parallel()

	body := readModuleFile(t, "agents/default-user-agent.md")
	for _, needle := range []string{
		"Normalize raw events into logical conversational acts",
		"Treat edits as replacements of earlier messages",
		"You are not required to reply to every batch",
		"avoid butting into conversations",
		"Follow explicit user instructions closely",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("AGENTS instructions missing %q", needle)
		}
	}
}

func readModuleFile(t *testing.T, rel string) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(body)
}
