package main

import (
	"encoding/hex"
	"testing"
)

// TestProtocolConstantsNeverChange pins the OUTPUTS of the two protocol
// constants that still carry the product's former name. They survived the
// Orange→Bob rename on purpose (design/2026-09-09-orange-to-bob-rename.md):
//
//   - configChangedNamespace derives every config.changed event id; a new seed
//     makes every past config event emittable a second time.
//   - sessionSecretLabel derives the session signing key; a new label
//     invalidates every outstanding session token.
//
// A red test here means one of them changed. Put it back; do not update the
// expected values.
func TestProtocolConstantsNeverChange(t *testing.T) {
	if got, want := configChangedNamespace.String(), "c81f40b6-1a13-5262-a3a0-7e85a43e4c89"; got != want {
		t.Errorf("configChangedNamespace = %s, want %s", got, want)
	}

	cases := []struct {
		apiSecret string
		want      string
	}{
		{"", "497a753eeec58d3a27466fb98d80981bd298de0043728747ef14771808e65ba1"}, // dev-open fallback
		{"pin-test-secret", "0ac1f3911f40258843ec4c6ee4e444293fc363775a00e89ce40a6fd4ac08d016"},
	}
	for _, c := range cases {
		getenv := func(k string) string {
			if k == apiSecretEnv {
				return c.apiSecret
			}
			return ""
		}
		secret, explicit := resolveSessionSecret(getenv)
		if explicit {
			t.Fatalf("apiSecret %q: derived key reported as explicit", c.apiSecret)
		}
		if got := hex.EncodeToString(secret); got != c.want {
			t.Errorf("apiSecret %q: derived session key = %s, want %s", c.apiSecret, got, c.want)
		}
	}
}
