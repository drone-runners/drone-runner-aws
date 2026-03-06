# Binary Version Validation

## Overview

harness-vm-runner needs to ensure that the binaries downloaded and used in cloud-init are compatible with the runner version. This document explains the version validation strategy.

## Problem

At runtime, harness-vm-runner creates VMs and uses cloud-init to download binaries from Docker registries. These binaries must be compatible with the runner, otherwise:

- API mismatches can cause failures
- Unexpected behavior due to version skew
- Silent failures that are hard to debug

## Solution

### 1. Compile-Time Version Embedding

All binary versions are embedded into the runner binary at compile time via ldflags:

```dockerfile
-X 'github.com/drone-runners/drone-runner-aws/version.Version=${BUILD_VERSION}'
-X 'github.com/drone-runners/drone-runner-aws/version.LiteEngineVersion=${LITE_ENGINE_VERSION}'
-X 'github.com/drone-runners/drone-runner-aws/version.PluginVersion=${PLUGIN_VERSION}'
-X 'github.com/drone-runners/drone-runner-aws/version.AutoInjectionVersion=${AUTO_INJECTION_VERSION}'
-X 'github.com/drone-runners/drone-runner-aws/version.HCliVersion=${HCLI_VERSION}'
-X 'github.com/drone-runners/drone-runner-aws/version.TmateVersion=${TMATE_VERSION}'
-X 'github.com/drone-runners/drone-runner-aws/version.BinaryRegistry=${BINARY_REGISTRY}'
```

### 2. Startup Validation & Logging

When the runner starts, it logs all expected binary versions:

```
INFO[0000] harness-vm-runner started - expected binary versions
  runner_version=1.0.0
  lite_engine_version=1.0.0-main-822a72
  plugin_version=1.0.0
  auto_injection_version=1.0.0
  hcli_version=1.0.0
  tmate_version=1.0.0
  binary_registry=gar

INFO[0000] binary images to be used in cloud-init
  lite_engine=us-west1-docker.pkg.dev/gar-setup/docker/harness-vm-runner-lite-engine:1.0.0-main-822a72
  plugin=us-west1-docker.pkg.dev/gar-setup/docker/harness-vm-runner-plugin:1.0.0
  ...
```

This allows operators to immediately see:
- Which binary versions the runner expects
- Which registry (GAR vs Docker Hub) will be used
- Full image paths for troubleshooting

### 3. Runtime Validation Options

#### Option A: harness-vm-runner-binaries Bundle (Recommended for Production)

**Approach**: Build a single `harness-vm-runner-binaries` image that bundles all 5 binaries with the same version as the runner.

**Validation**:
```go
import "github.com/drone-runners/drone-runner-aws/version"

// In cloud-init code
binariesImage := version.GetBinariesBundleImage() + ":" + version.GetBinariesBundleTag()
// Returns: us-west1-docker.pkg.dev/gar-setup/docker/harness-vm-runner-binaries:1.0.0

// Validate version matches
err := version.ValidateBinariesCompatibility(pulledVersion)
if err != nil {
    logrus.WithError(err).Warn("binaries bundle version mismatch detected")
}
```

**Advantages**:
- Single version number to manage
- Atomic updates (all binaries updated together)
- Guaranteed compatibility

**When to use**:
- Production releases
- When all binaries are updated together
- When strict version parity is required

#### Option B: Individual Binary Images (For Development/Testing)

**Approach**: Pull each binary separately with potentially different versions.

**Validation**:
```go
// Pull individual binaries
liteEngineImage := version.GetBinaryImage("lite-engine") + ":" + version.LiteEngineVersion
pluginImage := version.GetBinaryImage("plugin") + ":" + version.PluginVersion
// ... etc

// After pulling, extract versions from image labels
pulledVersions := version.BinaryVersionInfo{
    LiteEngineVersion: extractedLiteEngineVersion,
    PluginVersion: extractedPluginVersion,
    // ... etc
}

// Validate
errs := version.ValidateIndividualBinaries(pulledVersions)
for _, err := range errs {
    logrus.WithError(err).Warn("binary version mismatch detected")
}
```

**Advantages**:
- Flexible - can test custom versions of individual binaries
- Useful during development
- Allows independent binary updates

**When to use**:
- Development/testing
- When testing a custom lite-engine version
- When binaries evolve independently

### 4. Decoupled Go Library vs Runtime Binary Versions

For **lite-engine only**, the Go library version (in go.mod) may differ from the runtime binary version:

**Why?**
- `go.mod` defines the **Go library** imported to compile the runner
- Runtime binary version defines which **binary** is downloaded in cloud-init
- During development, you may want to test a custom binary while keeping a stable API

**Example**:
```
go.mod:           github.com/harness/lite-engine v0.5.155
Runtime version:  lite-engine:1.0.0-main-822a72
```

**Validation is disabled** for this case - the Dockerfile does NOT enforce go.mod == runtime version.

**When to use**:
- Testing custom lite-engine binaries during development
- Hot-fixing lite-engine binary bugs without API changes
- Gradual rollout of lite-engine changes

**Risks**:
- API incompatibility if the binary uses different interfaces
- Harder to debug version mismatches

**Mitigation**:
- Only use for lite-engine (other binaries don't have Go library dependency)
- Test thoroughly before production use
- Use Option A (binaries bundle) for production releases

## Monitoring Version Mismatches

### At Startup

Check logs for:
```
INFO[0000] harness-vm-runner started - expected binary versions
```

Verify the versions match your deployment expectations.

### At Runtime

If version validation detects a mismatch, you'll see:
```
WARN[...] binaries bundle version mismatch detected
  error="binaries bundle version mismatch: runner=1.0.0, binaries=1.0.1. This may cause compatibility issues."
```

Or for individual binaries:
```
WARN[...] binary version mismatch detected
  error="lite-engine version mismatch: expected=1.0.0-main-822a72, pulled=1.0.0"
```

### Recommended Actions

1. **Production**: Mismatches should be treated as errors - investigate and fix immediately
2. **Development**: Mismatches are warnings - acceptable for testing but document why
3. **Always**: Log the mismatch with full context (runner version, binary version, registry used)

## Best Practices

1. **Use binaries bundle for production**: Ensures all binaries are tested together
2. **Use individual binaries for development**: Allows testing custom versions
3. **Always check startup logs**: Verify versions before running workloads
4. **Monitor for warnings**: Set up alerting on version mismatch warnings
5. **Document exceptions**: If using decoupled versions, document why in git commit

## Version Update Workflow

### For Production Release

1. Update binary-versions.yaml with new versions
2. Run `./scripts/update-dependency-versions.sh` (syncs lite-engine from go.mod)
3. Build runner: `docker build ... --build-arg BUILD_VERSION=1.2.0`
4. Build binaries bundle: `docker build ... -f Dockerfile-harness-vm-runner-binaries --build-arg BUILD_VERSION=1.2.0`
5. Both images have matching VERSION labels
6. Deploy runner - startup logs show expected versions

### For Development Testing

1. Keep go.mod at stable version
2. Override binary version in Dockerfile ARG: `--build-arg LITE_ENGINE_VERSION=1.0.0-main-822a72`
3. Build runner with decoupled version
4. Startup logs show custom binary version
5. Test and iterate

## See Also

- [Binary Versions Configuration](../config/README.md)
- [Update Dependency Versions Script](../scripts/update-dependency-versions.sh)
