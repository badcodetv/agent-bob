package connections

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// A credential obtained through the console (a Google refresh token) is the
// one connection credential that lives in Postgres, and only sealed by this
// file: AES-256-GCM under AGENTKIT_CONNECTIONS_KEY, a key that exists only in
// agentd's environment. The point is that a database backup on its own is
// useless — see the design addendum's amended rule
// (design/2026-09-11-project-connections.md, "Connect Google button").

// connectionsKeyEnv is named in every key error so an operator knows which
// variable to fix. Errors name the variable and never any byte of its value:
// they end up in boot logs.
const connectionsKeyEnv = "AGENTKIT_CONNECTIONS_KEY"

// sealKeyLen is AES-256's key size. sealNonceLen is GCM's standard nonce size;
// Open checks it explicitly because cipher.AEAD.Open panics on any other
// length, and a nonce read back from the database is untrusted input.
const (
	sealKeyLen   = 32
	sealNonceLen = 12
)

// errOpen is deliberately one fixed message for every Open failure (wrong
// key, wrong project/account, tampering): GCM cannot tell them apart, and a
// message built from the inputs could carry ciphertext into a log. A wrong
// key is diagnosed earlier and readably, by comparing KeyID with the key id
// stored beside the row.
var errOpen = errors.New("connections: stored credential could not be decrypted (wrong key, wrong project or account, or corrupted)")

// ParseKey decodes AGENTKIT_CONNECTIONS_KEY: standard base64 of exactly 32
// bytes (`openssl rand -base64 32`). Any other shape is an error naming the
// variable, never the value. It is strict on purpose — no trimming, no
// URL-safe or hex fallback — because a key that is not what the operator
// thinks it is would only show up later, as every stored connection refusing
// to open after the next deploy normalises it differently.
func ParseKey(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, fmt.Errorf("%s is empty: set it to the output of `openssl rand -base64 32`", connectionsKeyEnv)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(b64)
	if err != nil {
		// err is not wrapped: base64.CorruptInputError is only an offset
		// today, but nothing promises it will never quote the input.
		return nil, fmt.Errorf("%s is not standard base64: set it to the output of `openssl rand -base64 32`", connectionsKeyEnv)
	}
	if len(key) != sealKeyLen {
		return nil, fmt.Errorf("%s must decode to %d bytes, got %d: set it to the output of `openssl rand -base64 32`", connectionsKeyEnv, sealKeyLen, len(key))
	}
	return key, nil
}

// Sealer encrypts and decrypts stored connection credentials under one key.
// It holds the ready cipher and the derived key id and state key, never
// exposing the key itself.
type Sealer struct {
	aead     cipher.AEAD
	keyID    string
	stateKey []byte
}

// NewSealer builds a Sealer from a 32-byte key (normally ParseKey's result).
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != sealKeyLen {
		return nil, fmt.Errorf("connections: %s must be %d bytes, got %d", connectionsKeyEnv, sealKeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("connections: %s: %w", connectionsKeyEnv, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("connections: %s: %w", connectionsKeyEnv, err)
	}

	// Domain-separated hash, not a raw hash of the key: the id is stored in
	// plain text beside every ciphertext, so it must not double as a
	// fingerprint usable anywhere else. 16 hex chars is plenty to tell "this
	// row was sealed with another key" apart from "this row is corrupt".
	idSum := sha256.Sum256(append([]byte("bob-connections-key-id\x00"), key...))

	// The OAuth state is signed with a key derived from, never equal to, the
	// encryption key, so no signature ever exposes a MAC under the key that
	// protects stored tokens.
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("bob-connect-state-v1"))

	return &Sealer{
		aead:     aead,
		keyID:    hex.EncodeToString(idSum[:])[:16],
		stateKey: mac.Sum(nil),
	}, nil
}

// KeyID is the first 16 hex chars of SHA-256("bob-connections-key-id\x00" ||
// key). Stored beside the ciphertext so "wrong key" is a readable reason, not
// a GCM failure.
func (s *Sealer) KeyID() string { return s.keyID }

// StateKey derives the OAuth-state HMAC key: HMAC-SHA256(key,
// "bob-connect-state-v1"). Never equal to the encryption key. A copy is
// returned so a caller cannot mutate the Sealer's.
func (s *Sealer) StateKey() []byte { return append([]byte(nil), s.stateKey...) }

// Seal encrypts plaintext with a fresh random 12-byte nonce. aad should be
// AccountAAD(project, account): it is authenticated but not stored, so a row
// copied into another project or account will not Open.
func (s *Sealer) Seal(plaintext, aad []byte) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, sealNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("connections: generating nonce: %w", err)
	}
	return nonce, s.aead.Seal(nil, nonce, plaintext, aad), nil
}

// Open decrypts what Seal produced, given the same aad. Every failure is the
// same fixed error (see errOpen).
func (s *Sealer) Open(nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(nonce) != sealNonceLen {
		return nil, errOpen
	}
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errOpen
	}
	return plaintext, nil
}

// AccountAAD is the additional data binding a sealed credential to its
// (project, account): "bob-connection-credential/v1\x00"+project+"\x00"+account.
// The NUL separators stop ("en", "cgoogle") aliasing ("enc", "google"): an
// account follows agentdb.ValidateConnectionName, so it holds no NUL, and the
// last NUL therefore always marks where the project ends.
func AccountAAD(project, account string) []byte {
	return []byte("bob-connection-credential/v1\x00" + project + "\x00" + account)
}
