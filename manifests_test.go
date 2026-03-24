package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAmberManifestsValidate(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("amber"); err != nil {
		t.Skip("amber CLI is required for manifest validation")
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(file)
	manifests := []string{
		"amber/agent-provisioner.json5",
		"amber/onboarding-agent.json5",
	}
	for _, manifest := range manifests {
		manifest := manifest
		t.Run(manifest, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command("amber", "check", manifest)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("amber check %s failed: %v\n%s", manifest, err, output)
			}
		})
	}
}
