# onboarding

`onboarding` is the product integration repo for the Matrix onboarding flow.

This first cut adds:

- a singleton product provisioner MCP service
- a durable SQLite-backed provisioning store
- Amber manifests for the provisioner and onboarding agent
- prompt files that later bootstrap code can inject into the Codex runtimes
- bootstrap logic that points the default agent source at the `scenarios` repo's published `user-agent` manifest

The provisioner is intentionally the only agent-facing place that performs initial
account-plus-scenario provisioning. The onboarding agent should call
`onboarding.v1.user_agents.provision_initial` instead of composing raw
Matrix user creation with raw `amber-manager` scenario creation on its own.

## Provisioner runtime

The provisioner runs as an HTTP MCP server and expects these environment
variables:

- `ONBOARDING_LISTEN_ADDR`
- `ONBOARDING_DB_PATH`
- `ONBOARDING_MATRIX_HOMESERVER_URL`
- `ONBOARDING_MANAGER_URL`
- `ONBOARDING_DEFAULT_AGENT_SOURCE_URL`
- `ONBOARDING_SHARED_RESPONSES_BINDABLE_SERVICE_ID`
- `ONBOARDING_DEFAULT_AGENT_MODEL`

Optional:

- `ONBOARDING_REGISTRATION_TOKEN`
- `ONBOARDING_MATRIX_BINDABLE_SERVICE_NAME`
- `ONBOARDING_DEFAULT_AGENT_MODEL_REASONING_EFFORT`
- `ONBOARDING_DEFAULT_AGENT_DEVELOPER_INSTRUCTIONS`
- `ONBOARDING_DEFAULT_AGENT_CONFIG_TOML`
- `ONBOARDING_DEFAULT_AGENT_AGENTS_MD`
- `ONBOARDING_DEFAULT_AGENT_WORKSPACE_AGENTS_MD`
- `ONBOARDING_REVOKED_SOURCE_URLS`

`ONBOARDING_REVOKED_SOURCE_URLS` is a comma- or newline-separated list of
allowlist source URLs that the provisioner should remove from the manager after
bootstrap. This is defense in depth only; the manager config file still remains
the source of truth on restart.

## Tools

The provisioner currently exports:

- `onboarding.v1.user_agents.get`
- `onboarding.v1.user_agents.provision_initial`

The provisioning tool:

- reserves one onboarding-default record per owner
- creates the Matrix account
- creates or adopts the backing user-agent scenario
- waits for the scenario to become running
- clears the stored plaintext password once the workflow is complete

## Prompts

The `prompts/` and `agents/` directories contain the prompt sources intended for
the onboarding and default-agent Codex runtimes. The reusable user-agent
manifest itself lives in the `scenarios` repo and composes published upstream
manifests from `codex-a2a`, `matrix-mcp`, and `matrix-a2a-bridge`. The manifests
accept prompt and `AGENTS.md` contents through config, so bootstrap code can
load these files and pass the file contents explicitly.

## Local Real Stack

For a local end-to-end stack using real Codex auth and the real responses API
through `codex-auth-proxy`, use:

```bash
onboarding/scripts/run-local-real.sh up
```

The script owns the local runtime directory, pulls released dependency images,
rebuilds the local `onboarding` image, starts Tuwunel and a containerized
`amber-manager`, auto-detects a usable Docker socket mount, mounts the local
`onboarding` repo read-only into the manager for its own manifests, and then
runs the onboarding bootstrap. By default it provisions:

- the onboarding agent on `gpt-5.4-mini` with `medium` reasoning
- the child user agent on `gpt-5.4-mini` with `medium` reasoning

It also supports:

- `status`
- `logs`
- `bootstrap`
- `down`
- `reset`

By default it uses `$HOME/.codex/auth.json`. If your auth file lives elsewhere,
pass `--auth-json /absolute/path/to/auth.json`.

By default the script uses published source URLs for:

- the reusable `scenarios` user-agent manifest
- the upstream `codex-a2a` auth-proxy manifest

Override only the auth-proxy source or manager image with:

- `ONBOARDING_LOCAL_AUTH_PROXY_SOURCE_URL`
- `ONBOARDING_LOCAL_AMBER_MANAGER_IMAGE`
- `ONBOARDING_E2E_AUTH_PROXY_SOURCE_URL`
- `ONBOARDING_E2E_AMBER_MANAGER_IMAGE`

For real live e2e runs, set `ONBOARDING_E2E_PROFILE=1` if you want the test to
dump provisioner timings plus live Codex room-task traces. Normal runs keep that
verbose profiling output disabled.

The reusable `scenarios` user-agent manifest is now expected to resolve from
its published GitHub raw URL for both the local script and the live e2e.
