package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var issuer string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": issuer + "/certs"})
	})
	mux.HandleFunc("/certs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	issuer = server.URL
	validator := NewValidator(issuer, "agentgate-workspace")
	sign := func(claims jwt.MapClaims, signer *rsa.PrivateKey, typ string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test-key"
		if typ != "" {
			token.Header["typ"] = typ
		}
		raw, err := token.SignedString(signer)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := jwt.MapClaims{
		"iss": issuer, "aud": "agentgate-workspace", "exp": time.Now().Add(time.Minute).Unix(),
		"sub": "subject-1", "preferred_username": "ada", "name": "Ada",
		"realm_access": map[string]any{"roles": []any{"developer"}},
	}
	claims, err := validator.Validate(context.Background(), sign(valid, key, "JWT"))
	if err != nil || claims.Subject != "subject-1" || claims.Username != "ada" || len(claims.Roles) != 1 {
		t.Fatalf("valid token: %+v %v", claims, err)
	}
	cases := []struct {
		name string
		raw  string
	}{
		{name: "expired", raw: sign(jwt.MapClaims{"iss": issuer, "aud": "agentgate-workspace", "exp": time.Now().Add(-time.Minute).Unix(), "sub": "subject-1"}, key, "JWT")},
		{name: "wrong audience", raw: sign(jwt.MapClaims{"iss": issuer, "aud": "other", "exp": time.Now().Add(time.Minute).Unix(), "sub": "subject-1"}, key, "JWT")},
		{name: "wrong issuer", raw: sign(jwt.MapClaims{"iss": "http://other.example", "aud": "agentgate-workspace", "exp": time.Now().Add(time.Minute).Unix(), "sub": "subject-1"}, key, "JWT")},
		{name: "bad signature", raw: sign(valid, other, "JWT")},
		{name: "refresh type", raw: sign(valid, key, "Refresh")},
		{name: "missing", raw: ""},
	}
	for _, tc := range cases {
		if _, err := validator.Validate(context.Background(), tc.raw); err == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
	}
	role, ok := HighestRole([]string{"viewer", "developer"})
	if !ok || role != "developer" || IsApprover([]string{"developer"}) || !IsApprover([]string{"approver"}) {
		t.Fatalf("role selection %s %v", role, ok)
	}
}
