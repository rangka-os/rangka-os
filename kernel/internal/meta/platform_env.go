package meta

import "strings"

// platformEnvPrefixes and platformEnvNames are the environment variables the kernel itself, Rangka Cloud or the
// host sets for the platform: the kernel's own settings (RANGKA_*: master key, internal token, the assistant's
// bearer RANGKA_AI_SECRET), the Raka sidecar and LLM keys (RAKA_*, ANTHROPIC_*, OPENAI_*), the database
// (DATABASE_URL, libpq's PG*), Vault (VAULT_*) and the cloud runtime (AWS_*, ECS_*). A company's system.rk may not
// bind them to a connector field or an upstream sign-in, since the value would then reach the company: as a
// connector field's value in GET /api/v1/connectors/<c>/auth, or in a request the company's connector or issuer
// receives (audit business-raka F1, contract 0017 §2).
var (
	platformEnvPrefixes = []string{"RANGKA_", "RAKA_", "ANTHROPIC_", "OPENAI_", "VAULT_", "AWS_", "ECS_", "PG"}
	platformEnvNames    = map[string]bool{"DATABASE_URL": true}
)

// PlatformEnv reports whether name is an environment variable of the platform, which ${VAR} bindings in
// system.rk connector_auth and auth.upstream may not read.
func PlatformEnv(name string) bool {
	if platformEnvNames[name] {
		return true
	}
	for _, p := range platformEnvPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
