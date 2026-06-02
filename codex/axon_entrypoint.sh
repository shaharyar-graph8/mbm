#!/bin/bash
# axon_entrypoint.sh — Axon agent image interface implementation for
# OpenAI Codex CLI.
#
# Interface contract:
#   - First argument ($1): the task prompt
#   - AXON_MODEL env var: model name (optional)
#   - CODEX_AUTH_JSON env var: ChatGPT-subscription auth blob (optional).
#     When set (oauth credential type), it is materialized to
#     ~/.codex/auth.json so `codex exec` authenticates against the pooled
#     subscription (flat-rate) instead of CODEX_API_KEY (pay-per-token).
#     Mirrors how Agent OS's codex-runner.sh loads the pooled auth.json.
#   - CODEX_API_KEY env var: OpenAI API key (api-key credential type).
#   - UID 61100: shared between git-clone init container and agent
#   - Working directory: /workspace/repo when a workspace is configured

set -uo pipefail

PROMPT="${1:?Prompt argument is required}"

# Subscription auth: if the controller injected a CODEX_AUTH_JSON blob
# (oauth credential type), write it to the codex auth file so the CLI
# uses the pooled ChatGPT subscription. Done before `codex exec` runs.
# CODEX_HOME defaults to ~/.codex (created in the image).
if [ -n "${CODEX_AUTH_JSON:-}" ]; then
    CODEX_DIR="${CODEX_HOME:-$HOME/.codex}"
    mkdir -p "$CODEX_DIR"
    printf '%s' "$CODEX_AUTH_JSON" > "$CODEX_DIR/auth.json"
    chmod 600 "$CODEX_DIR/auth.json"
fi

ARGS=(
    "exec"
    "--dangerously-bypass-approvals-and-sandbox"
    "--json"
    "$PROMPT"
)

if [ -n "${AXON_MODEL:-}" ]; then
    ARGS+=("--model" "$AXON_MODEL")
fi

codex "${ARGS[@]}"
AGENT_EXIT_CODE=$?

/axon/capture-outputs.sh

exit $AGENT_EXIT_CODE
