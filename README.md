# onboarding

`onboarding` owns the first-run product setup for Matrix users.

This repo is the integration layer that turns "a user can talk to the onboarding bot"
into "that user gets exactly one default child agent backed by amber-manager". It
contains:

- the bootstrap logic that creates the product's Matrix-side and manager-side state
- the provisioner MCP service that creates or adopts the user's default agent
- the product manifests for the provisioner and onboarding agent
- the prompt and `AGENTS.md` sources injected into the Codex runtimes

If you are arriving fresh, the most useful thing to do first is run the local stack.

## Start Here

### Prerequisites

For the normal local workflow you need:

- `docker`
- `go`
- `curl`
- `rg`
- a valid Codex auth file at `$HOME/.codex/auth.json`, or an explicit `--auth-json` path

### Bring up the local stack

```bash
scripts/run-local-real.sh up
```

That script is the default entrypoint for development. It:

- pulls the released dependency images it needs
- rebuilds the local `onboarding` image from this checkout
- starts a local Tuwunel Matrix homeserver
- starts `amber-manager` in Docker
- stages this repo's local manifests into the manager
- runs the bootstrap flow

By default the stack comes up at:

- Matrix: `http://127.0.0.1:8008`
- amber-manager: `http://127.0.0.1:8081`
- run directory: `/tmp/onboarding-local-real`

Useful follow-up commands:

```bash
scripts/run-local-real.sh status
scripts/run-local-real.sh logs
scripts/run-local-real.sh logs manager
scripts/run-local-real.sh down
scripts/run-local-real.sh reset
```

Useful flags:

```bash
scripts/run-local-real.sh up --auth-json /absolute/path/to/auth.json
scripts/run-local-real.sh up --rebuild
scripts/run-local-real.sh up --restart
scripts/run-local-real.sh up --run-dir /tmp/somewhere-else
```

### What you should expect after `up`

The bootstrap flow is idempotent. It persists state in `bootstrap-state.json` under
the run directory, so re-running it is normal.

After a successful run you should have:

- a bootstrap admin Matrix user
- an onboarding bot Matrix user
- a private `#welcome:<server>` room containing a link to DM the onboarding bot
- an onboarding scenario in amber-manager
- a provisioner scenario in amber-manager
- an auth-proxy scenario as well, if bootstrap had to create the shared responses service

The script prints the default bootstrap credentials and the Matrix registration token it used.

One thing it does **not** do: create a sample end user. To exercise the flow manually,
register a fresh Matrix user against the local homeserver, open the welcome room, DM the
onboarding bot, and ask for a new agent.

## How The Product Is Split Up

### Bootstrap

`cmd/onboarding-bootstrap` creates and reconciles the product-level state:

- Matrix users for the bootstrap admin and onboarding bot
- the welcome room and welcome message
- the shared responses bindable service, or an auth-proxy scenario if one is not already available
- the provisioner scenario
- the onboarding agent scenario

This is the thing that makes the product exist inside a specific Matrix homeserver plus
amber-manager pair.

### Provisioner

`cmd/onboarding-provisioner` runs an HTTP MCP server. It is the only agent-facing place
that is supposed to perform initial account-plus-scenario provisioning.

It currently exports:

- `onboarding.v1.user_agents.get`
- `onboarding.v1.user_agents.provision_initial`

`provision_initial` is responsible for:

- reserving one onboarding-default record per owner
- creating the Matrix account for the child agent
- creating or adopting the backing user-agent scenario
- waiting for that scenario to be running
- clearing the stored plaintext password once provisioning completes

The onboarding agent should use this service instead of composing raw Matrix user creation
with raw amber-manager scenario creation on its own.

### Default User-Agent Manifest

This repo does **not** own the reusable child-agent manifest itself. The default user-agent
manifest lives in the `scenarios` repo and is consumed through its published source URL.

That distinction matters when you are debugging:

- change this repo when you are working on onboarding, provisioning, product manifests, or prompts
- change `scenarios` when you are working on the reusable child-agent scenario definition

## Repo Map

- `scripts/run-local-real.sh`
  - quickest way to stand up the real local stack
- `cmd/onboarding-bootstrap`
  - idempotent product bootstrap runner
- `cmd/onboarding-provisioner`
  - MCP provisioner service
- `cmd/onboarding-test-manager-forwarders`
  - local/test helper that watches amber-manager and keeps TCP forwarders in sync with exposed scenario ports
- `cmd/onboarding-test-tcp-forwarder`
  - local/test TCP proxy used by the forwarder monitor
- `cmd/onboarding-test-a2a-get-task`
  - test helper for inspecting A2A task state from the command line
- `amber/`
  - product manifests loaded by amber-manager
- `agents/`
  - `AGENTS.md`-style content for the onboarding agent and default child agent
- `prompts/`
  - developer-instruction prompt sources for those agents
- `internal/bootstrap`
  - bootstrap configuration, state persistence, and reconciliation logic
- `internal/provisioner`
  - the provisioner service itself
- `internal/manager`, `internal/matrix`
  - service clients used by bootstrap and provisioning
- `internal/store`
  - SQLite-backed persistent state
- `e2e/`
  - live and mock-backed end-to-end tests

## If You Need To Change Something

- Changing onboarding conversation or behavior:
  - start with `agents/onboarding-agent.md` and `prompts/onboarding-developer-instructions.md`
- Changing the default child-agent prompt or instructions:
  - start with `agents/default-user-agent.md` and `prompts/default-user-agent-developer-instructions.md`
- Changing how the product is wired into amber-manager:
  - start with `amber/`
- Changing bootstrap behavior:
  - start with `internal/bootstrap`
- Changing one-time child-agent provisioning behavior:
  - start with `internal/provisioner`

## Running Components Directly

Most people should use `scripts/run-local-real.sh`. Running pieces directly is mainly useful
when you are debugging one layer in isolation.

### Running the provisioner directly

```bash
go run ./cmd/onboarding-provisioner
```

The provisioner is fully environment-driven. In practice that means you need to supply:

- where to listen and persist state
- how to reach Matrix and amber-manager
- which default child-agent source the provisioner is allowed to create
- which responses service the child agent should bind to
- any prompt or config payloads you want injected into the child runtime

For local development, the helper script already wires this up indirectly through bootstrap.
If you are debugging the provisioner itself, `internal/config/config.go` is the authoritative
definition of the current environment contract and defaults.

### Running bootstrap directly

```bash
go run ./cmd/onboarding-bootstrap
```

Bootstrap is also environment-driven. The important inputs are:

- where to persist bootstrap state
- how to reach the target Matrix homeserver and amber-manager
- the credentials for the bootstrap admin and onboarding bot
- the source URLs for the provisioner, onboarding agent, and default child agent
- either an existing shared responses service, or enough information to create an auth-proxy scenario

The helper script wires all of this for local development. If you run bootstrap manually,
remember that the source URLs must be reachable by amber-manager and present in its source
allowlist. `internal/bootstrap/config.go` is the authoritative definition of that contract.

## Testing And Validation

Run the normal test suite with:

```bash
go test ./...
```

Notes:

- the manifest validation test runs `amber check` if the `amber` CLI is installed
- if `amber` is installed, that validation resolves remote manifest references and therefore needs network access
- live tests are opt-in

Mock-backed live stack:

```bash
ONBOARDING_RUN_LIVE=1 go test ./e2e -run TestLiveBootstrapAndOnboardingWorkflow -count=1
```

Real Codex live stack:

```bash
ONBOARDING_RUN_LIVE_REAL=1 go test ./e2e -run TestLiveBootstrapAndOnboardingWorkflowRealCodex -count=1
```

The live tests also accept overrides for the Codex auth path, the auth-proxy source,
the manager image, model selection, and extra profiling output. Those knobs live in the
top of `e2e/live_test.go`; they are useful when you are tuning or debugging the live path,
but they should not be the first thing a new contributor has to read.
