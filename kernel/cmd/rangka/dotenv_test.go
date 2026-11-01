package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("# comment\nRK_A=one\nexport RK_B=\"two words\"\nRK_C=three # note\nRK_SET=file\n\nbad line\n"), 0o644)
	t.Setenv("RK_SET", "env")
	for _, k := range []string{"RK_A", "RK_B", "RK_C"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	loadDotEnv(p)
	for k, want := range map[string]string{"RK_A": "one", "RK_B": "two words", "RK_C": "three", "RK_SET": "env"} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
