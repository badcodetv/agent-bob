package connections

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// testKey returns 32 random bytes. Keys are random per test on purpose: a
// fixed test key invites someone to copy it into a real .env.
func testKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func testSealer(t *testing.T) *Sealer {
	t.Helper()
	s, err := NewSealer(testKey(t))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestParseKey_AcceptsBase64Of32Bytes(t *testing.T) {
	key := testKey(t)
	got, err := ParseKey(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	// Compared with bytes.Equal and reported without the values: a failure
	// message is a log line, and key material never goes in one.
	if !bytes.Equal(got, key) {
		t.Fatalf("ParseKey: decoded key differs from the encoded one")
	}
}

func TestParseKey_Refuses(t *testing.T) {
	b64 := func(n int) string {
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			t.Fatalf("rand: %v", err)
		}
		return base64.StdEncoding.EncodeToString(b)
	}
	hexKey := make([]byte, 32)
	if _, err := rand.Read(hexKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"not base64", "%%qz!!x~~#@&*?zq"},
		{"16 bytes", b64(16)},
		{"31 bytes", b64(31)},
		{"33 bytes", b64(33)},
		{"64 bytes", b64(64)},
		// `openssl rand -hex 32` is the likeliest mistake: 64 hex characters
		// are all in the base64 alphabet, so this decodes — to 48 bytes.
		{"hex string", hex.EncodeToString(hexKey)},
		{"url-safe base64", strings.NewReplacer("+", "-", "/", "_").Replace(b64(32)) + "-_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseKey(tc.input)
			if err == nil {
				t.Fatalf("ParseKey accepted a %s key", tc.name)
			}
			msg := err.Error()
			if !strings.Contains(msg, "AGENTKIT_CONNECTIONS_KEY") {
				t.Errorf("error does not name AGENTKIT_CONNECTIONS_KEY")
			}
			if leaksInput(msg, tc.input) {
				// Deliberately not printing msg: it is the thing suspected
				// of carrying key material.
				t.Errorf("error message contains part of the input")
			}
		})
	}
}

// leaksInput reports whether msg contains any 6-character run of input. Six
// is short enough to catch a truncated echo ("…at input 'Zm9vYm…'") and long
// enough that the fixed wording of an error cannot match random base64 by
// chance (at four, a run of this test flaked roughly once in a thousand).
func leaksInput(msg, input string) bool {
	const run = 6
	for i := 0; i+run <= len(input); i++ {
		if strings.Contains(msg, input[i:i+run]) {
			return true
		}
	}
	return false
}

func TestNewSealer_RefusesWrongLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewSealer(make([]byte, n)); err == nil {
			t.Errorf("NewSealer accepted a %d-byte key", n)
		}
	}
}

func TestSealer_RoundTrip(t *testing.T) {
	s := testSealer(t)
	plaintext := []byte("1//a-refresh-token-shaped-value")
	aad := AccountAAD("enc", "google")

	nonce, ct, err := s.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length: want 12, got %d", len(nonce))
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatalf("ciphertext contains the plaintext")
	}
	got, err := s.Open(nonce, ct, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open: round trip returned different bytes")
	}
}

func TestSealer_OpenRefuses(t *testing.T) {
	s := testSealer(t)
	plaintext := []byte("1//a-refresh-token-shaped-value")
	aad := AccountAAD("enc", "google")
	nonce, ct, err := s.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	flip := func(b []byte, i int) []byte {
		c := append([]byte(nil), b...)
		c[i] ^= 0x01
		return c
	}
	other := testSealer(t)

	cases := []struct {
		name   string
		sealer *Sealer
		nonce  []byte
		ct     []byte
		aad    []byte
	}{
		{"different key", other, nonce, ct, aad},
		{"different project", s, nonce, ct, AccountAAD("other", "google")},
		{"different account", s, nonce, ct, AccountAAD("enc", "work")},
		// The separator is what stops ("en","cgoogle") aliasing ("enc","google").
		{"shifted boundary", s, nonce, ct, AccountAAD("en", "cgoogle")},
		{"tampered ciphertext first byte", s, nonce, flip(ct, 0), aad},
		{"tampered ciphertext tag", s, nonce, flip(ct, len(ct)-1), aad},
		{"tampered nonce", s, flip(nonce, 0), ct, aad},
		// cipher.AEAD.Open panics on a wrong-length nonce; a row read back
		// from the database is untrusted input, so this must be an error.
		{"short nonce", s, nonce[:11], ct, aad},
		{"long nonce", s, append(append([]byte(nil), nonce...), 0), ct, aad},
		{"empty ciphertext", s, nonce, nil, aad},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.sealer.Open(tc.nonce, tc.ct, tc.aad)
			if err == nil {
				t.Fatalf("Open succeeded")
			}
			if got != nil {
				t.Errorf("Open returned bytes alongside an error")
			}
			if strings.Contains(err.Error(), string(plaintext)) {
				t.Errorf("error message contains the plaintext")
			}
		})
	}
}

func TestSealer_SealsDifferEachTime(t *testing.T) {
	s := testSealer(t)
	plaintext := []byte("same plaintext")
	aad := AccountAAD("enc", "google")
	n1, c1, err := s.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	n2, c2, err := s.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(n1, n2) {
		t.Errorf("two seals reused a nonce")
	}
	if bytes.Equal(c1, c2) {
		t.Errorf("two seals produced identical ciphertext")
	}
}

func TestSealer_KeyID(t *testing.T) {
	key := testKey(t)
	a, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	b, err := NewSealer(append([]byte(nil), key...))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	id := a.KeyID()
	if len(id) != 16 {
		t.Fatalf("KeyID length: want 16, got %d", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("KeyID is not hex")
	}
	if id != b.KeyID() {
		t.Errorf("KeyID not stable for the same key")
	}
	if id != a.KeyID() {
		t.Errorf("KeyID not stable across calls")
	}
	if id == testSealer(t).KeyID() {
		t.Errorf("KeyID equal for two different keys")
	}
	// The key id is stored in plain text beside the ciphertext; it must not
	// be a prefix of the key itself.
	if strings.HasPrefix(hex.EncodeToString(key), id) {
		t.Errorf("KeyID is a prefix of the key")
	}
}

func TestSealer_StateKey(t *testing.T) {
	key := testKey(t)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	sk := s.StateKey()
	if len(sk) != 32 {
		t.Fatalf("StateKey length: want 32, got %d", len(sk))
	}
	if bytes.Equal(sk, key) {
		t.Errorf("StateKey equals the encryption key")
	}
	if strings.Contains(hex.EncodeToString(sk), s.KeyID()) {
		t.Errorf("StateKey overlaps KeyID")
	}
	if !bytes.Equal(sk, s.StateKey()) {
		t.Errorf("StateKey not stable")
	}
	if bytes.Equal(sk, testSealer(t).StateKey()) {
		t.Errorf("StateKey equal for two different keys")
	}
	// A caller mutating the returned slice must not change the next one.
	sk[0] ^= 0xff
	if bytes.Equal(sk, s.StateKey()) {
		t.Errorf("StateKey returns shared backing memory")
	}
}

func TestAccountAAD(t *testing.T) {
	got := AccountAAD("enc", "google")
	want := []byte("bob-connection-credential/v1\x00enc\x00google")
	if !bytes.Equal(got, want) {
		t.Fatalf("AccountAAD: want %q, got %q", want, got)
	}
}
