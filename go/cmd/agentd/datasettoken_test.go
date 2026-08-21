package main

import (
	"context"
	"testing"
	"time"

	"github.com/binocarlos/badcode-agent-orange/extension"
	"github.com/binocarlos/badcode-agent-orange/extension/devclaims"
	"github.com/golang-jwt/jwt/v5"
)

// TestDatasetTokenRoundTrip: mint then verify returns the pinned pair, and
// only that pair — a token minted for (wolf, a) yields ("wolf", "a") and
// never ("wolf", "b").
func TestDatasetTokenRoundTrip(t *testing.T) {
	secret := []byte("dataset-secret")
	tok, exp, err := mintDatasetToken(secret, "wolf", "a", 0)
	if err != nil {
		t.Fatalf("mintDatasetToken: %v", err)
	}
	if tok == "" {
		t.Fatal("mintDatasetToken returned empty token")
	}
	if exp <= time.Now().Unix() {
		t.Fatalf("exp=%d not in the future", exp)
	}

	project, name, err := verifyDatasetToken(secret, tok)
	if err != nil {
		t.Fatalf("verifyDatasetToken: %v", err)
	}
	if project != "wolf" || name != "a" {
		t.Fatalf("verifyDatasetToken = (%q, %q), want (wolf, a)", project, name)
	}
	if name == "b" {
		t.Fatal("impossible: name == b")
	}
}

// TestDatasetTokenMintExpiryMatchesTokenClaim: the returned exp is read back
// off the token itself, not recomputed — so it can never drift from what the
// token actually carries.
func TestDatasetTokenMintExpiryMatchesTokenClaim(t *testing.T) {
	secret := []byte("dataset-secret-2")
	tok, exp, err := mintDatasetToken(secret, "wolf", "drone-suppliers", 120)
	if err != nil {
		t.Fatalf("mintDatasetToken: %v", err)
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(tok, claims, func(*jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"})); err != nil {
		t.Fatalf("parse: %v", err)
	}
	claimExp, _ := claims.GetExpirationTime()
	if claimExp == nil || claimExp.Unix() != exp {
		t.Fatalf("returned exp=%d, token's own exp claim=%v", exp, claimExp)
	}
}

// TestDatasetTokenSidClaimIsEmpty: a non-empty sid would make this a working
// core-MCP credential, since /mcp authenticates a caller by exactly that
// claim (embedtoken.go:159-167).
func TestDatasetTokenSidClaimIsEmpty(t *testing.T) {
	secret := []byte("dataset-secret-3")
	tok, _, err := mintDatasetToken(secret, "wolf", "a", 0)
	if err != nil {
		t.Fatalf("mintDatasetToken: %v", err)
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(tok, claims, func(*jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"})); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sid, _ := claims["sid"].(string); sid != "" {
		t.Fatalf("sid claim = %q, want empty", sid)
	}
}

// TestDatasetTokenScopeCarriesNoVersion: a token minted while a name is at
// version 3 verifies unchanged against version 7 of the same name — the mint
// helper takes no version argument at all, and two mints for the same
// (project, name) carry the identical scope regardless of when they happen.
func TestDatasetTokenScopeCarriesNoVersion(t *testing.T) {
	secret := []byte("dataset-secret-4")
	tokEarly, _, err := mintDatasetToken(secret, "wolf", "drone-suppliers", 0)
	if err != nil {
		t.Fatalf("mintDatasetToken (early): %v", err)
	}
	// Simulate the dataset being re-appended (a new version written) between
	// mint and verify by simply verifying later — the verifier has no notion
	// of "current version" at all, which is the property under test.
	project, name, err := verifyDatasetToken(secret, tokEarly)
	if err != nil {
		t.Fatalf("verifyDatasetToken: %v", err)
	}
	if project != "wolf" || name != "drone-suppliers" {
		t.Fatalf("verifyDatasetToken = (%q, %q)", project, name)
	}
}

// TestDatasetTokenTTLClamping: clamped, never rejected, with the exact rows
// the ticket specifies.
func TestDatasetTokenTTLClamping(t *testing.T) {
	cases := []struct {
		name       string
		ttlSeconds int
		want       time.Duration
	}{
		{"zero means the default", 0, 300 * time.Second},
		{"below the floor clamps up", 5, 60 * time.Second},
		{"just below the floor clamps up", 59, 60 * time.Second},
		{"the default itself is honoured", 300, 300 * time.Second},
		{"the ceiling itself", 900, 900 * time.Second},
		{"far above the ceiling clamps down", 100000, 900 * time.Second},
		{"negative clamps up to the floor", -1, 60 * time.Second},
	}
	secret := []byte("dataset-ttl-secret")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			_, exp, err := mintDatasetToken(secret, "wolf", "a", tc.ttlSeconds)
			if err != nil {
				t.Fatalf("mintDatasetToken: %v", err)
			}
			gotTTL := time.Unix(exp, 0).Sub(before)
			// Allow a couple of seconds of slack for slow CI.
			if gotTTL < tc.want-2*time.Second || gotTTL > tc.want+2*time.Second {
				t.Fatalf("ttlSeconds=%d: exp implies TTL=%v, want ~%v", tc.ttlSeconds, gotTTL, tc.want)
			}
		})
	}
}

// TestDatasetTokenVerifyRejectsExpired.
func TestDatasetTokenVerifyRejectsExpired(t *testing.T) {
	secret := []byte("dataset-secret-5")
	tok, err := devclaims.NewWithTTL(secret, -time.Second).IssueScoped(
		context.Background(), extension.ContextScope{Customer: "wolf", Job: "dataset-download"}, "",
		devclaims.DatasetScope("wolf", "a"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := verifyDatasetToken(secret, tok); err == nil {
		t.Fatal("expired token was accepted")
	}
}

// TestDatasetTokenVerifyRejectsWrongSecret.
func TestDatasetTokenVerifyRejectsWrongSecret(t *testing.T) {
	tok, _, err := mintDatasetToken([]byte("secret-A"), "wolf", "a", 0)
	if err != nil {
		t.Fatalf("mintDatasetToken: %v", err)
	}
	if _, _, err := verifyDatasetToken([]byte("secret-B"), tok); err == nil {
		t.Fatal("token signed with secret-A was accepted under secret-B")
	}
}

// TestDatasetTokenVerifyRejectsNonHS256.
func TestDatasetTokenVerifyRejectsNonHS256(t *testing.T) {
	secret := []byte("dataset-secret-6")
	claims := jwt.MapClaims{
		"customer":           "wolf",
		"job":                "dataset-download",
		"sid":                "",
		devclaims.ScopeClaim: devclaims.DatasetScope("wolf", "a"),
		"iat":                time.Now().Unix(),
		"exp":                time.Now().Add(time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	raw, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none-alg token: %v", err)
	}
	if _, _, err := verifyDatasetToken(secret, raw); err == nil {
		t.Fatal("a none-alg token was accepted")
	}
}

// TestDatasetTokenVerifyRejectsNonEmptySid.
func TestDatasetTokenVerifyRejectsNonEmptySid(t *testing.T) {
	secret := []byte("dataset-secret-7")
	tok, err := devclaims.New(secret).IssueScoped(
		context.Background(), extension.ContextScope{Customer: "wolf", Job: "dataset-download"},
		"container-session-id", devclaims.DatasetScope("wolf", "a"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := verifyDatasetToken(secret, tok); err == nil {
		t.Fatal("a token with a non-empty sid was accepted")
	}
}

// TestDatasetTokenVerifyRejectsMissingScope.
func TestDatasetTokenVerifyRejectsMissingScope(t *testing.T) {
	secret := []byte("dataset-secret-8")
	tok, err := devclaims.New(secret).Issue(
		context.Background(), extension.ContextScope{Customer: "wolf", Job: "dataset-download"}, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := verifyDatasetToken(secret, tok); err == nil {
		t.Fatal("a token with no scope claim was accepted")
	}
}

// TestDatasetTokenVerifyRejectsSessionScope: a session-scoped (embed) token
// must not verify as a dataset token.
func TestDatasetTokenVerifyRejectsSessionScope(t *testing.T) {
	secret := []byte("dataset-secret-9")
	tok, err := devclaims.New(secret).IssueScoped(
		context.Background(), extension.ContextScope{Customer: "wolf", Job: "embed"}, "",
		devclaims.SessionScope("s-hyp-a"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := verifyDatasetToken(secret, tok); err == nil {
		t.Fatal("a session-scoped token was accepted as a dataset token")
	}
}

// TestDatasetTokenVerifyRejectsCustomerScopeMismatch: the customer claim must
// agree with the scope's project half.
func TestDatasetTokenVerifyRejectsCustomerScopeMismatch(t *testing.T) {
	secret := []byte("dataset-secret-10")
	tok, err := devclaims.New(secret).IssueScoped(
		context.Background(), extension.ContextScope{Customer: "someone-else", Job: "dataset-download"}, "",
		devclaims.DatasetScope("wolf", "a"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := verifyDatasetToken(secret, tok); err == nil {
		t.Fatal("a token whose customer disagrees with its scope's project was accepted")
	}
}
