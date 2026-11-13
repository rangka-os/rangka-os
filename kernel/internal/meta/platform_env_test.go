package meta

import "testing"

func TestPlatformEnv(t *testing.T) {
	for name, want := range map[string]bool{
		"RANGKA_AI_SECRET": true, "RANGKA_MASTER_KEY": true, "RANGKA_INTERNAL_TOKEN": true, "DATABASE_URL": true,
		"PGPASSWORD": true, "RAKA_SERVE_TOKEN": true, "ANTHROPIC_API_KEY": true, "OPENAI_API_KEY": true, "VAULT_TOKEN": true,
		"AWS_SECRET_ACCESS_KEY": true, "ECS_CONTAINER_METADATA_URI_V4": true,
		"WA_TOKEN": false, "DOKU_SECRET_KEY": false, "XENDIT_SECRET_KEY": false, "MY_DATABASE_URL": false, "RANGKA": false,
	} {
		if PlatformEnv(name) != want {
			t.Errorf("PlatformEnv(%s) = %v, want %v", name, !want, want)
		}
	}
}
