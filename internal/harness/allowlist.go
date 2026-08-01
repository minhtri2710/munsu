package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

// ModelAllowlistKey is the config key holding the optional munsu-owned model
// allowlist. The value is one canonical <harness>:<model> identity per line;
// blank lines and # comments are ignored.
const ModelAllowlistKey = "model-allowlist"

// ModelAllowlistOverrideEnv is the environment override for the allowlist,
// consistent with the MUNSU_<KEY>_OVERRIDE convention in config.Get.
const ModelAllowlistOverrideEnv = "MUNSU_MODEL_ALLOWLIST_OVERRIDE"

// ModelAllowlistPath returns the config file path for the allowlist under homeDir.
func ModelAllowlistPath(homeDir string) string {
	return filepath.Join(config.ConfigDir(homeDir), ModelAllowlistKey)
}

// PolicyHome resolves the home whose allowlist policy applies to work running
// in homeDir. Captain homes carry config/parent-home pointing at their General;
// captain-context model identities are inherited from the General's config
// surface, so the policy is read there. A home without parent-home is its own
// policy root.
func PolicyHome(homeDir string) string {
	if v, err := config.Get(homeDir, "parent-home"); err == nil && v != "" {
		return v
	}
	return homeDir
}

// ModelAllowlistPresent reports whether an allowlist policy is configured for
// homeDir (config file or environment override). Absence is not an error.
func ModelAllowlistPresent(homeDir string) (bool, error) {
	if _, ok := os.LookupEnv(ModelAllowlistOverrideEnv); ok {
		return true, nil
	}
	path := ModelAllowlistPath(PolicyHome(homeDir))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("model allowlist: checking %s: %w", path, err)
	}
	return true, nil
}

// ValidateModelAllowlist parses allowlist policy content and returns an error
// for the first malformed identity. An empty policy (no valid identities) is
// accepted at write time — it denotes deny-all and fail-closes at enforcement.
func ValidateModelAllowlist(raw string) error {
	if _, err := parseModelAllowlist(raw); err != nil {
		return err
	}
	return nil
}

// parseModelAllowlist parses raw policy content into canonical identities.
func parseModelAllowlist(raw string) ([]string, error) {
	var identities []string
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		identity, err := canonicalModelIdentity(line)
		if err != nil {
			return nil, fmt.Errorf("model allowlist line %d: %w", i+1, err)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

// canonicalModelIdentity validates and canonicalizes one <harness>:<model>
// identity line against the known harness registry. The harness side is
// canonicalized (case/whitespace) so registry aliases or case variants can
// never match or bypass a canonical entry.
func canonicalModelIdentity(line string) (string, error) {
	harnessName, model, ok := strings.Cut(line, ":")
	if !ok {
		return "", fmt.Errorf("entry %q must be <harness>:<model>", line)
	}
	harnessName = strings.TrimSpace(harnessName)
	model = strings.TrimSpace(model)
	canonical, ok := CanonicalHarness(harnessName)
	if !ok {
		return "", fmt.Errorf("entry %q references unknown harness %q (known: %v)", line, harnessName, KnownHarnesses)
	}
	if model == "" {
		return "", fmt.Errorf("entry %q has an empty model", line)
	}
	return canonical + ":" + model, nil
}

// LoadModelAllowlist loads the allowlist policy for homeDir.
// Returns (allowed identities, present, error):
//   - absent policy: (nil, false, nil) — no enforcement, compatibility preserved
//   - empty policy (present but zero valid identities): error — fail closed
//   - malformed identity: error — fail closed
func LoadModelAllowlist(homeDir string) (map[string]bool, bool, error) {
	policyHome := PolicyHome(homeDir)
	path := ModelAllowlistPath(policyHome)
	var raw string
	if val, ok := os.LookupEnv(ModelAllowlistOverrideEnv); ok {
		raw = val
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("model allowlist: reading %s: %w", path, err)
		}
		raw = string(data)
	}
	identities, err := parseModelAllowlist(raw)
	if err != nil {
		return nil, true, err
	}
	if len(identities) == 0 {
		return nil, true, fmt.Errorf("model allowlist policy at %s is empty — it denies every model; add <harness>:<model> identities or remove the policy to disable enforcement", path)
	}
	allowed := make(map[string]bool, len(identities))
	for _, id := range identities {
		allowed[id] = true
	}
	return allowed, true, nil
}

// CheckModelAllowed enforces the optional model allowlist for a concrete
// harness:model identity. It returns nil when the policy is absent (backward
// compatibility) or the identity is allowed. Denied identities fail closed
// with the allowed values and the correction. When a policy is present but the
// identity is unresolved (empty harness or model), it fails closed as well: a
// runtime default cannot be verified against the policy and must not bypass it.
// The harness side is canonicalized against the registry before lookup.
func CheckModelAllowed(homeDir, harnessName, model string) error {
	allowed, present, err := LoadModelAllowlist(homeDir)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if harnessName == "" || model == "" {
		return fmt.Errorf("model allowlist policy present but the effective identity is unresolved (harness %q, model %q): an active policy denies unresolved identities because a runtime default cannot be verified; set a concrete model or remove config/%s to disable enforcement", harnessName, model, ModelAllowlistKey)
	}
	canonical, ok := CanonicalHarness(harnessName)
	if !ok {
		return fmt.Errorf("model %q for harness %q is not a canonical registry harness (known: %v); allowlist identities must use the canonical harness name", model, harnessName, KnownHarnesses)
	}
	identity := canonical + ":" + model
	if allowed[identity] {
		return nil
	}
	return fmt.Errorf("model %q for harness %q is not in the munsu model allowlist (config/%s); allowed identities: %s; to allow it run: munsu config set %s \"<harness>:<model>\" (one identity per line), or remove config/%s to disable enforcement",
		model, canonical, ModelAllowlistKey, formatAllowedIdentities(allowed), ModelAllowlistKey, ModelAllowlistKey)
}

func formatAllowedIdentities(allowed map[string]bool) string {
	keys := make([]string, 0, len(allowed))
	for k := range allowed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "(none)"
	}
	return strings.Join(keys, ", ")
}
