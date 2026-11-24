package authn

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Subject  string
	Username string
	Name     string
	Roles    []string
}

type Validator struct {
	Issuer   string
	Audience string
	Client   *http.Client
	now      func() time.Time
	mu       sync.Mutex
	keys     map[string]*rsa.PublicKey
	fetched  time.Time
}

func NewValidator(issuer, audience string) *Validator {
	return &Validator{Issuer: strings.TrimRight(issuer, "/"), Audience: audience, Client: &http.Client{Timeout: 5 * time.Second}, now: time.Now, keys: map[string]*rsa.PublicKey{}}
}

func (v *Validator) Validate(ctx context.Context, raw string) (Claims, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.Issuer), jwt.WithAudience(v.Audience), jwt.WithExpirationRequired())
	token, err := parser.Parse(raw, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing key id")
		}
		return v.key(ctx, kid)
	})
	if err != nil || !token.Valid {
		return Claims{}, errors.New("token rejected")
	}
	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, errors.New("token rejected")
	}
	if typ, _ := token.Header["typ"].(string); strings.EqualFold(typ, "Refresh") {
		return Claims{}, errors.New("token rejected")
	}
	if value, ok := mapClaims["typ"].(string); ok && strings.EqualFold(value, "Refresh") {
		return Claims{}, errors.New("token rejected")
	}
	subject, _ := mapClaims["sub"].(string)
	if subject == "" {
		return Claims{}, errors.New("token rejected")
	}
	username, _ := mapClaims["preferred_username"].(string)
	name, _ := mapClaims["name"].(string)
	return Claims{Subject: subject, Username: username, Name: name, Roles: realmRoles(mapClaims)}, nil
}

func realmRoles(claims jwt.MapClaims) []string {
	access, _ := claims["realm_access"].(map[string]any)
	raw, _ := access["roles"].([]any)
	roles := make([]string, 0, len(raw))
	for _, role := range raw {
		if value, ok := role.(string); ok {
			roles = append(roles, value)
		}
	}
	return roles
}

func (v *Validator) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if key, ok := v.keys[kid]; ok && v.now().Sub(v.fetched) < 5*time.Minute {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		if key, ok := v.keys[kid]; ok {
			return key, nil
		}
		return nil, err
	}
	key, ok := v.keys[kid]
	if !ok {
		return nil, errors.New("unknown signing key")
	}
	return key, nil
}

func (v *Validator) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return err
	}
	res, err := v.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var discovered struct {
		JWKSURI string `json:"jwks_uri"`
		Issuer  string `json:"issuer"`
	}
	if err := json.NewDecoder(res.Body).Decode(&discovered); err != nil {
		return err
	}
	if discovered.Issuer != "" && discovered.Issuer != v.Issuer {
		return errors.New("issuer mismatch")
	}
	jwksURL := discovered.JWKSURI
	if jwksURL == "" {
		jwksURL = v.Issuer + "/protocol/openid-connect/certs"
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	res, err = v.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var document struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&document); err != nil {
		return err
	}
	next := map[string]*rsa.PublicKey{}
	for _, key := range document.Keys {
		if key.Kty != "RSA" || key.Kid == "" {
			continue
		}
		public, err := rsaKey(key.N, key.E)
		if err != nil {
			continue
		}
		next[key.Kid] = public
	}
	if len(next) == 0 {
		return fmt.Errorf("no signing keys")
	}
	v.keys = next
	v.fetched = v.now()
	return nil
}

func rsaKey(n, e string) (*rsa.PublicKey, error) {
	modulus, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil {
		return nil, err
	}
	exponent, err := base64.RawURLEncoding.DecodeString(e)
	if err != nil {
		return nil, err
	}
	value := 0
	for _, b := range exponent {
		value = value<<8 + int(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: value}, nil
}

func HighestRole(roles []string) (string, bool) {
	has := map[string]bool{}
	for _, role := range roles {
		has[role] = true
	}
	for _, role := range []string{"admin", "developer", "viewer"} {
		if has[role] {
			return role, true
		}
	}
	return "", false
}

func IsApprover(roles []string) bool {
	for _, role := range roles {
		if role == "approver" {
			return true
		}
	}
	return false
}
