package upstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func credential(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		t.Fatal(e)
	}
	return hex.EncodeToString(b)
}
func TestTLSManagerContractAndTokenRefresh(t *testing.T) {
	app, secret, token := credential(t, 8), credential(t, 16), credential(t, 16)
	logins, gets := 0, 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/login":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["app_id"] != app || in["secret"] != secret {
				t.Error("login mapping")
			}
			logins++
			json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in_seconds": 3600})
		case "/v1/products":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("bearer header")
			}
			gets++
			if gets == 1 {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []Product{{ID: 3, Name: "test"}}})
		case "/v1/orders":
			var in struct {
				Product     int `json:"product_id"`
				Certificate struct {
					CSR      string `json:"csr"`
					Validity struct {
						Type string `json:"type"`
						Days int    `json:"length_in_days"`
					} `json:"validity"`
				} `json:"certificate"`
				Validations []Validation `json:"domain_validations"`
			}
			if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
				t.Fatal(e)
			}
			if in.Certificate.CSR == "" || in.Certificate.Validity.Type != "length" || in.Certificate.Validity.Days != 90 || in.Validations[0].Method != "txt_dns_record" {
				t.Error("place order mapping")
			}
			json.NewEncoder(w).Encode(map[string]string{"order_code": "test-code", "order_status": "pending"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c, e := NewTLSManager(srv.URL, app, secret)
	if e != nil {
		t.Fatal(e)
	}
	c.client = srv.Client()
	ps, e := c.ListProducts(context.Background())
	if e != nil || len(ps) != 1 || logins != 2 {
		t.Fatalf("products: %v logins=%d", e, logins)
	}
	_, e = c.PlaceOrder(context.Background(), Spec{ProductID: 3, ValidityDays: 90, Validations: []Validation{{Name: "app.example.test", Method: "txt_dns_record"}}}, "generated-public-csr", Subscriber{})
	if e != nil {
		t.Fatal(e)
	}
	if logins != 2 {
		t.Fatal("token was not cached")
	}
}
func TestTLSManagerRedactsErrorsAndDoesNotRedirect(t *testing.T) {
	app, secret := credential(t, 8), credential(t, 16)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); w.Write([]byte(secret)) }))
	defer srv.Close()
	c, _ := NewTLSManager(srv.URL, app, secret)
	c.client.Transport = srv.Client().Transport
	e := c.TestConnection(context.Background())
	if e == nil || strings.Contains(e.Error(), secret) {
		t.Fatal("error disclosure")
	}
	var he *HTTPError
	if !errors.As(e, &he) || !he.Temporary() {
		t.Fatal("retry classification")
	}
	if _, e = NewTLSManager("http://example.test", app, secret); e == nil {
		t.Fatal("plain HTTP accepted")
	}
	if _, e = NewTLSManager("https://user:pass@example.test", app, secret); e == nil {
		t.Fatal("URL credentials accepted")
	}
}
