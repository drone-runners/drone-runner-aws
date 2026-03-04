#!/bin/bash
# Build script for harness-vm-runner-binaries
# Reads versions from config/binary-versions.yaml and builds the Docker image

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSIONS_FILE="$REPO_ROOT/config/binary-versions.yaml"

# Check if yq is installed
if ! command -v yq &> /dev/null; then
    echo "Error: yq is not installed. Please install it first."
    echo "  brew install yq"
    exit 1
fi

# Read versions from YAML
echo "📖 Reading binary versions from $VERSIONS_FILE"
LITE_ENGINE_VERSION=$(yq '.binaries.lite-engine.version' "$VERSIONS_FILE")
PLUGIN_VERSION=$(yq '.binaries.plugin.version' "$VERSIONS_FILE")
AUTO_INJECTION_VERSION=$(yq '.binaries.auto-injection.version' "$VERSIONS_FILE")
HCLI_VERSION=$(yq '.binaries.hcli.version' "$VERSIONS_FILE")
TMATE_VERSION=$(yq '.binaries.tmate.version' "$VERSIONS_FILE")

echo "Binary versions:"
echo "  lite-engine: $LITE_ENGINE_VERSION"
echo "  plugin: $PLUGIN_VERSION"
echo "  auto-injection: $AUTO_INJECTION_VERSION"
echo "  hcli: $HCLI_VERSION"
echo "  tmate: $TMATE_VERSION"

# Get build version (passed as first argument or default to "dev")
BUILD_VERSION="${1:-dev}"
echo "Build version: $BUILD_VERSION"

# Build the Docker image
echo "🐳 Building harness-vm-runner-binaries:$BUILD_VERSION"
docker build \
  -f "$REPO_ROOT/docker/Dockerfile-harness-vm-runner-binaries" \
  --build-arg LITE_ENGINE_VERSION="$LITE_ENGINE_VERSION" \
  --build-arg PLUGIN_VERSION="$PLUGIN_VERSION" \
  --build-arg AUTO_INJECTION_VERSION="$AUTO_INJECTION_VERSION" \
  --build-arg HCLI_VERSION="$HCLI_VERSION" \
  --build-arg TMATE_VERSION="$TMATE_VERSION" \
  --build-arg BUILD_VERSION="$BUILD_VERSION" \
  --build-arg BUILD_TIME="$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --build-arg COMMIT_SHA="$(git rev-parse HEAD)" \
  --build-arg BRANCH_NAME="$(git rev-parse --abbrev-ref HEAD)" \
  -t "harness/harness-vm-runner-binaries:$BUILD_VERSION" \
  "$REPO_ROOT"

echo "✅ Successfully built harness/harness-vm-runner-binaries:$BUILD_VERSION"
