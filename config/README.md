# Binary Versions Configuration

## Overview

The `binary-versions.yaml` file controls which versions of runtime binaries are bundled with harness-vm-runner and where they are pulled from.

## Configuration

### Registry Setting

The `registry` field controls where binary images are pulled from:

- **`gar`** (Google Artifact Registry): Used during development/build time
  - Registry: `us-west1-docker.pkg.dev/gar-setup/docker`
  - Example: `us-west1-docker.pkg.dev/gar-setup/docker/harness-vm-runner-lite-engine:0.5.155`

- **`docker`** (Docker Hub): Used during release time
  - Registry: `harness`
  - Example: `harness/harness-vm-runner-lite-engine:0.5.155`

### Binary Versions

Each binary has:
- `version`: Semantic version (MAJOR.MINOR.PATCH)
- `image`: Docker image name (without registry prefix)
- `description`: Human-readable description

## Workflow

**`config/binary-versions.yaml` is the single source of truth for all binary versions and registry settings.**

### Step-by-step: Updating Binary Versions

1. **Edit `config/binary-versions.yaml`**
   ```bash
   vim config/binary-versions.yaml
   # Update version numbers for any binaries
   # Update registry setting if needed (gar or docker)
   ```

2. **Run sync script** (requires `yq` - install with `brew install yq`)
   ```bash
   ./scripts/update-dependency-versions.sh
   ```

   This automatically syncs versions from `binary-versions.yaml` to:
   - `docker/Dockerfile-harness-vm-runner` (all ARG versions + BINARY_REGISTRY)
   - `docker/Dockerfile-harness-vm-runner-binaries` (all ARG versions + FROM registry paths)

3. **Review changes**
   ```bash
   git diff docker/
   ```

4. **Build images**
   ```bash
   # Build runner
   docker build -f docker/Dockerfile-harness-vm-runner .

   # Build binaries bundle
   docker build -f docker/Dockerfile-harness-vm-runner-binaries .
   ```

5. **Commit changes**
   ```bash
   git add config/ docker/
   git commit -m "chore: update binary versions"
   ```

### Switching between registries

To switch from GAR to Docker Hub (e.g., for a release):

1. Edit `config/binary-versions.yaml`: change `registry: "gar"` to `registry: "docker"`
2. Run `./scripts/update-dependency-versions.sh` to update Dockerfiles
3. Rebuild images

The script automatically updates FROM lines in `Dockerfile-harness-vm-runner-binaries`:
- `registry: "gar"` → `FROM us-west1-docker.pkg.dev/gar-setup/docker/harness-vm-runner-*`
- `registry: "docker"` → `FROM harness/harness-vm-runner-*`

## Runtime Usage

At runtime, the runner can access version information:

```go
import "github.com/drone-runners/drone-runner-aws/version"

// Get binary versions
liteEngineVer := version.LiteEngineVersion    // "0.5.155"
pluginVer := version.PluginVersion             // "1.0.0"

// Get registry setting
registry := version.BinaryRegistry             // "gar" or "docker"

// Get full image paths
prefix := version.GetBinaryImagePrefix()       // "us-west1-docker.pkg.dev/gar-setup/docker"
image := version.GetBinaryImage("lite-engine") // "us-west1-docker.pkg.dev/gar-setup/docker/harness-vm-runner-lite-engine"
```

## Build-time Injection

All version information is injected at Docker build time via ARG → ldflags:

```dockerfile
ARG LITE_ENGINE_VERSION=0.5.155
ARG BINARY_REGISTRY=gar

RUN go build -ldflags "-X 'github.com/drone-runners/drone-runner-aws/version.LiteEngineVersion=${LITE_ENGINE_VERSION}' \
                        -X 'github.com/drone-runners/drone-runner-aws/version.BinaryRegistry=${BINARY_REGISTRY}'"
```

This ensures the compiled binary knows exactly which versions and registry to use.
