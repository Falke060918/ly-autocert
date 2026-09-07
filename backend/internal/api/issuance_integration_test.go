package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/auth"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

func TestIssuanceAPISecretAndRoleBoundaries(t *testing.T) {
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	env := newTestEnv(t, func(s *Server) { s.issuance = issuance.New(s.store, key) })
	ctx := context.Background()
	tokens := map[domain.Role]string{}
	for _, role := range []domain.Role{domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleSecurityAdministrator, domain.RoleViewer} {
		pw := randomPassword(t)
		name := "issue-" + string(role)
		if _, e := env.svc.CreateUser(ctx, name, pw, nil, role, nil, auth.AuditMeta{}); e != nil {
			t.Fatal(e)
		}
		token, status := env.login(t, name, pw)
		if status != 200 {
			t.Fatal("login")
		}
		tokens[role] = token
	}
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := hex.EncodeToString(secretBytes)
	appBytes := make([]byte, 8)
	rand.Read(appBytes)
	body := map[string]any{"name": "test connection", "base_url": "https://ca.example.test/api", "app_id": hex.EncodeToString(appBytes), "secret": secret, "subscriber": map[string]any{"existing_subscriber_id": 1, "contact": map[string]any{"existing_contact_id": 1}}}
	res := env.do(t, "POST", "/api/v1/ca-connections", tokens[domain.RolePKIAdministrator], body)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("create CA HTTP %d: %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), hex.EncodeToString(appBytes)) || strings.Contains(string(b), `"app_id"`) {
		t.Fatal("CA response disclosed app id")
	}
	if strings.Contains(string(b), secret) || strings.Contains(string(b), "secret_sealed") {
		t.Fatal("CA response disclosed secret")
	}
	var out struct {
		Connection store.CAConnection `json:"connection"`
	}
	if e := json.Unmarshal(b, &out); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { env.pool.Exec(`DELETE FROM ca_connections WHERE id=$1::uuid`, out.Connection.ID) })
	var appSealed, appNonce []byte
	var plain *string
	if e := env.pool.QueryRow(`SELECT app_id,app_id_sealed,app_id_nonce FROM ca_connections WHERE id=$1::uuid`, out.Connection.ID).Scan(&plain, &appSealed, &appNonce); e != nil || plain != nil || len(appSealed) != 32 || len(appNonce) != 12 {
		t.Fatal("app id not sealed", e)
	}
	body["app_id"] = ""
	body["secret"] = ""
	rp := env.do(t, "PATCH", "/api/v1/ca-connections/"+out.Connection.ID, tokens[domain.RolePKIAdministrator], body)
	rp.Body.Close()
	if rp.StatusCode != 200 {
		t.Fatal("blank credential update failed", rp.StatusCode)
	}
	var kept []byte
	env.pool.QueryRow(`SELECT app_id_sealed FROM ca_connections WHERE id=$1::uuid`, out.Connection.ID).Scan(&kept)
	if !bytes.Equal(kept, appSealed) {
		t.Fatal("blank update replaced app id")
	}
	for _, role := range []domain.Role{domain.RoleViewer, domain.RoleSecurityAdministrator} {
		for _, path := range []string{"/api/v1/ca-connections", "/api/v1/agents/tokens", "/api/v1/issuance/orders", "/api/v1/deployment/targets"} {
			r := env.do(t, "POST", path, tokens[role], map[string]any{})
			r.Body.Close()
			if r.StatusCode != http.StatusForbidden {
				t.Errorf("role %s path %s status %d", role, path, r.StatusCode)
			}
		}
	}
	for _, path := range []string{"/api/v1/agents", "/api/v1/deployment/targets"} {
		r := env.do(t, "GET", path, tokens[domain.RolePKIAdministrator], nil)
		r.Body.Close()
		if r.StatusCode != 403 {
			t.Errorf("PKI deployment administration allowed: %s", path)
		}
	}
	r := env.do(t, "GET", "/api/v1/issuance/orders", tokens[domain.RoleViewer], nil)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("viewer cannot view orders")
	}
	r = env.do(t, "GET", "/api/v1/issuance/orders?page_size=201", tokens[domain.RoleViewer], nil)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("page limit not enforced")
	}
	r = env.do(t, "GET", "/api/v1/ca-connections", tokens[domain.RolePKIAdministrator], nil)
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if strings.Contains(string(b), secret) {
		t.Fatal("CA list disclosed secret")
	}
	var sealed []byte
	if e := env.pool.QueryRow(`SELECT secret_sealed FROM ca_connections WHERE id=$1::uuid`, out.Connection.ID).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(sealed), secret) {
		t.Fatal("secret persisted in plaintext")
	}
}
