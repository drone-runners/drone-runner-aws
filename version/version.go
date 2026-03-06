package version

import "fmt"

// Build information injected via ldflags at compile time
var (
	// Version is the runner version
	Version string

	// Commit is the git commit SHA
	Commit string

	// BuildTime is the build timestamp
	BuildTime string

	// Branch is the git branch name
	Branch string
)

// Binary versions injected via ldflags at compile time
// These versions must match the binaries bundled in harness-vm-runner-binaries image
var (
	// LiteEngineVersion is the version of lite-engine binary
	LiteEngineVersion string

	// PluginVersion is the version of plugin binary
	PluginVersion string

	// AutoInjectionVersion is the version of auto-injection binary
	AutoInjectionVersion string

	// HCliVersion is the version of hcli binary
	HCliVersion string

	// TmateVersion is the version of tmate binary
	TmateVersion string
)

// Binary registry configuration injected via ldflags at compile time
var (
	// BinaryRegistry specifies where to pull binaries from
	// Values: "gar" (Google Artifact Registry) or "docker" (Docker Hub)
	BinaryRegistry string
)

const (
	// GARRegistry is the Google Artifact Registry prefix
	GARRegistry = "us-west1-docker.pkg.dev/gar-setup/docker"

	// DockerRegistry is the Docker Hub prefix
	DockerRegistry = "harness"
)

// GetBinaryImagePrefix returns the registry prefix based on BinaryRegistry setting
func GetBinaryImagePrefix() string {
	if BinaryRegistry == "docker" {
		return DockerRegistry
	}
	return GARRegistry
}

// GetBinaryImage returns the full image path for a binary
func GetBinaryImage(binaryName string) string {
	prefix := GetBinaryImagePrefix()
	return prefix + "/harness-vm-runner-" + binaryName
}

// GetBinariesBundleImage returns the full image path for the binaries bundle
func GetBinariesBundleImage() string {
	prefix := GetBinaryImagePrefix()
	return prefix + "/harness-vm-runner-binaries"
}

// GetBinariesBundleTag returns the version tag for the binaries bundle
// The binaries bundle version should match the runner version for compatibility
func GetBinariesBundleTag() string {
	return Version
}

// ValidateBinariesCompatibility checks if the binaries bundle version matches the runner version
// Returns error if versions are incompatible
func ValidateBinariesCompatibility(binariesBundleVersion string) error {
	if binariesBundleVersion == "" {
		return fmt.Errorf("binaries bundle version is empty")
	}

	if Version == "" || Version == "dev" {
		// Development builds - allow any binaries version
		return nil
	}

	if binariesBundleVersion != Version {
		return fmt.Errorf("binaries bundle version mismatch: runner=%s, binaries=%s, this may cause compatibility issues", Version, binariesBundleVersion)
	}

	return nil
}

// BinaryVersionInfo holds version information for a binary pulled from image labels
type BinaryVersionInfo struct {
	LiteEngineVersion    string
	PluginVersion        string
	AutoInjectionVersion string
	HCliVersion          string
	TmateVersion         string
}

// ValidateIndividualBinaries checks if individual binary versions from image labels match expected versions
// This is used when binaries are pulled separately (not as a bundle)
func ValidateIndividualBinaries(pulled *BinaryVersionInfo) []error {
	var errs []error

	if LiteEngineVersion != "" && pulled.LiteEngineVersion != "" && pulled.LiteEngineVersion != LiteEngineVersion {
		errs = append(errs, fmt.Errorf("lite-engine version mismatch: expected=%s, pulled=%s", LiteEngineVersion, pulled.LiteEngineVersion))
	}

	if PluginVersion != "" && pulled.PluginVersion != "" && pulled.PluginVersion != PluginVersion {
		errs = append(errs, fmt.Errorf("plugin version mismatch: expected=%s, pulled=%s", PluginVersion, pulled.PluginVersion))
	}

	if AutoInjectionVersion != "" && pulled.AutoInjectionVersion != "" && pulled.AutoInjectionVersion != AutoInjectionVersion {
		errs = append(errs, fmt.Errorf("auto-injection version mismatch: expected=%s, pulled=%s", AutoInjectionVersion, pulled.AutoInjectionVersion))
	}

	if HCliVersion != "" && pulled.HCliVersion != "" && pulled.HCliVersion != HCliVersion {
		errs = append(errs, fmt.Errorf("hcli version mismatch: expected=%s, pulled=%s", HCliVersion, pulled.HCliVersion))
	}

	if TmateVersion != "" && pulled.TmateVersion != "" && pulled.TmateVersion != TmateVersion {
		errs = append(errs, fmt.Errorf("tmate version mismatch: expected=%s, pulled=%s", TmateVersion, pulled.TmateVersion))
	}

	return errs
}

// GetExpectedVersions returns the binary versions compiled into this runner
func GetExpectedVersions() BinaryVersionInfo {
	return BinaryVersionInfo{
		LiteEngineVersion:    LiteEngineVersion,
		PluginVersion:        PluginVersion,
		AutoInjectionVersion: AutoInjectionVersion,
		HCliVersion:          HCliVersion,
		TmateVersion:         TmateVersion,
	}
}
