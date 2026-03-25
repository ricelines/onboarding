package managerforwarders

import (
	"slices"
	"testing"
)

func TestForwarderDockerArgs(t *testing.T) {
	t.Parallel()

	args := forwarderDockerArgs(Config{
		ManagerContainerName: "amber-manager-test",
		ForwarderImage:       "ghcr.io/ricelines/onboarding:v0.1",
	}, "forwarder-test", 43007)

	if !slices.Contains(args, "--network") {
		t.Fatalf("expected --network in args: %#v", args)
	}
	if !slices.Contains(args, "container:amber-manager-test") {
		t.Fatalf("expected container network target in args: %#v", args)
	}
	if slices.Contains(args, "--add-host") {
		t.Fatalf("forwarder args must not set --add-host when sharing the manager network namespace: %#v", args)
	}
	if !slices.Contains(args, "--target") {
		t.Fatalf("expected --target in args: %#v", args)
	}
	if !slices.Contains(args, "host.docker.internal:43007") {
		t.Fatalf("expected host.docker.internal target in args: %#v", args)
	}
}
