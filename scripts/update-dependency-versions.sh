#!/usr/bin/env bash

# update-dependency-versions.sh
#
# Syncs Dockerfile ARG versions from config/binary-versions.yaml (single source of truth).
# This ensures binary versions are consistent across all files.
#
# Usage:
#   ./scripts/update-dependency-versions.sh
#
# This script:
#   1. Reads binary versions from config/binary-versions.yaml
#   2. Updates Dockerfile-harness-vm-runner ARGs
#   3. Updates Dockerfile-harness-vm-runner-binaries ARGs and registry
#   4. Ensures binary-versions.yaml is the single source of truth

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BINARY_VERSIONS_YAML="${REPO_ROOT}/config/binary-versions.yaml"
RUNNER_DOCKERFILE="${REPO_ROOT}/docker/Dockerfile-harness-vm-runner"
BINARIES_DOCKERFILE="${REPO_ROOT}/docker/Dockerfile-harness-vm-runner-binaries"

echo "=== Syncing binary versions from config/binary-versions.yaml ==="
echo ""

# Check if binary-versions.yaml exists
if [[ ! -f "${BINARY_VERSIONS_YAML}" ]]; then
    echo "ERROR: binary-versions.yaml not found at ${BINARY_VERSIONS_YAML}"
    exit 1
fi

# Check if yq is available for YAML parsing
if ! command -v yq &> /dev/null; then
    echo "ERROR: yq is required to parse YAML. Install it with: brew install yq"
    exit 1
fi

# Extract registry setting
REGISTRY=$(yq eval '.registry' "${BINARY_VERSIONS_YAML}")
echo "Registry: ${REGISTRY}"

# Extract binary versions
LITE_ENGINE_VERSION=$(yq eval '.binaries.lite-engine.version' "${BINARY_VERSIONS_YAML}")
PLUGIN_VERSION=$(yq eval '.binaries.plugin.version' "${BINARY_VERSIONS_YAML}")
AUTO_INJECTION_VERSION=$(yq eval '.binaries.auto-injection.version' "${BINARY_VERSIONS_YAML}")
HCLI_VERSION=$(yq eval '.binaries.hcli.version' "${BINARY_VERSIONS_YAML}")
TMATE_VERSION=$(yq eval '.binaries.tmate.version' "${BINARY_VERSIONS_YAML}")
ENVMAN_VERSION=$(yq eval '.binaries.envman.version' "${BINARY_VERSIONS_YAML}")

echo ""
echo "Binary versions from config/binary-versions.yaml:"
echo "  lite-engine:     ${LITE_ENGINE_VERSION}"
echo "  plugin:          ${PLUGIN_VERSION}"
echo "  auto-injection:  ${AUTO_INJECTION_VERSION}"
echo "  hcli:            ${HCLI_VERSION}"
echo "  tmate:           ${TMATE_VERSION}"
echo "  envman:          ${ENVMAN_VERSION}"
echo ""

# Determine registry prefix
if [[ "${REGISTRY}" == "docker" ]]; then
    REGISTRY_PREFIX="harness"
else
    REGISTRY_PREFIX="us-west1-docker.pkg.dev/gar-setup/docker"
fi

echo "Registry prefix: ${REGISTRY_PREFIX}"
echo ""

# Function to update ARG in Dockerfile
update_arg() {
    local dockerfile=$1
    local arg_name=$2
    local arg_value=$3

    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        sed -i '' "s|^ARG ${arg_name}=.*|ARG ${arg_name}=${arg_value}|" "${dockerfile}"
    else
        # Linux
        sed -i "s|^ARG ${arg_name}=.*|ARG ${arg_name}=${arg_value}|" "${dockerfile}"
    fi
}

# Function to update FROM lines in Dockerfile to use correct registry
update_from_registry() {
    local dockerfile=$1
    local registry_prefix=$2

    # Update all FROM lines that reference harness-vm-runner-* images
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        sed -i '' -E "s|FROM .*/harness-vm-runner-|FROM ${registry_prefix}/harness-vm-runner-|g" "${dockerfile}"
    else
        # Linux
        sed -i -E "s|FROM .*/harness-vm-runner-|FROM ${registry_prefix}/harness-vm-runner-|g" "${dockerfile}"
    fi
}

# Update Dockerfile-harness-vm-runner
if [[ -f "${RUNNER_DOCKERFILE}" ]]; then
    echo "Updating ${RUNNER_DOCKERFILE}..."

    update_arg "${RUNNER_DOCKERFILE}" "LITE_ENGINE_VERSION" "${LITE_ENGINE_VERSION}"
    update_arg "${RUNNER_DOCKERFILE}" "PLUGIN_VERSION" "${PLUGIN_VERSION}"
    update_arg "${RUNNER_DOCKERFILE}" "AUTO_INJECTION_VERSION" "${AUTO_INJECTION_VERSION}"
    update_arg "${RUNNER_DOCKERFILE}" "HCLI_VERSION" "${HCLI_VERSION}"
    update_arg "${RUNNER_DOCKERFILE}" "TMATE_VERSION" "${TMATE_VERSION}"
    update_arg "${RUNNER_DOCKERFILE}" "ENVMAN_VERSION" "${ENVMAN_VERSION}"
    update_arg "${RUNNER_DOCKERFILE}" "BINARY_REGISTRY" "${REGISTRY}"

    echo "✓ Updated ${RUNNER_DOCKERFILE}"
else
    echo "WARNING: ${RUNNER_DOCKERFILE} not found"
fi

# Update Dockerfile-harness-vm-runner-binaries
if [[ -f "${BINARIES_DOCKERFILE}" ]]; then
    echo "Updating ${BINARIES_DOCKERFILE}..."

    update_arg "${BINARIES_DOCKERFILE}" "LITE_ENGINE_VERSION" "${LITE_ENGINE_VERSION}"
    update_arg "${BINARIES_DOCKERFILE}" "PLUGIN_VERSION" "${PLUGIN_VERSION}"
    update_arg "${BINARIES_DOCKERFILE}" "AUTO_INJECTION_VERSION" "${AUTO_INJECTION_VERSION}"
    update_arg "${BINARIES_DOCKERFILE}" "HCLI_VERSION" "${HCLI_VERSION}"
    update_arg "${BINARIES_DOCKERFILE}" "TMATE_VERSION" "${TMATE_VERSION}"
    update_arg "${BINARIES_DOCKERFILE}" "ENVMAN_VERSION" "${ENVMAN_VERSION}"

    # Update FROM lines to use correct registry
    update_from_registry "${BINARIES_DOCKERFILE}" "${REGISTRY_PREFIX}"

    echo "✓ Updated ${BINARIES_DOCKERFILE}"
else
    echo "WARNING: ${BINARIES_DOCKERFILE} not found"
fi

echo ""
echo "=== Summary ==="
echo "Source of truth: ${BINARY_VERSIONS_YAML}"
echo "Registry: ${REGISTRY} (${REGISTRY_PREFIX})"
echo ""
echo "Updated versions:"
echo "  lite-engine:     ${LITE_ENGINE_VERSION}"
echo "  plugin:          ${PLUGIN_VERSION}"
echo "  auto-injection:  ${AUTO_INJECTION_VERSION}"
echo "  hcli:            ${HCLI_VERSION}"
echo "  tmate:           ${TMATE_VERSION}"
echo "  envman:          ${ENVMAN_VERSION}"
echo ""
echo "Updated files:"
echo "  • ${RUNNER_DOCKERFILE}"
echo "  • ${BINARIES_DOCKERFILE}"
echo ""
echo "Next steps:"
echo "  1. Review changes: git diff docker/"
echo "  2. Build runner: docker build -f docker/Dockerfile-harness-vm-runner ."
echo "  3. Build binaries: docker build -f docker/Dockerfile-harness-vm-runner-binaries ."
echo "  4. Commit: git add docker/ && git commit -m 'sync: update binary versions from config/binary-versions.yaml'"
