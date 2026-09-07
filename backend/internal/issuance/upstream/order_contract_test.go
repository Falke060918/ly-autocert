package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestPlaceOrderDomainValidationContract(t *testing.T) {
	for _, tc := range []struct {
		name        string
		dns         []string
		validations []Validation
		want        []Validation
		conflict    bool
	}{
		{"wildcard", []string{"*.example.test"}, []Validation{{"*.example.test", "txt_dns_record"}}, []Validation{{"example.test", "txt_dns_record"}}, false},
		{"ordinary multi SAN", []string{"app.example.test", "other.example.test"}, []Validation{{"app.example.test", "txt_http_file"}, {"other.example.test", "txt_dns_record"}}, []Validation{{"app.example.test", "txt_http_file"}, {"other.example.test", "txt_dns_record"}}, false},
		{"wildcard before base", []string{"*.example.test", "example.test", "other.example.test"}, []Validation{{"*.example.test", "txt_dns_record"}, {"example.test", "txt_dns_record"}, {"other.example.test", "txt_http_file"}}, []Validation{{"example.test", "txt_dns_record"}, {"other.example.test", "txt_http_file"}}, false},
		{"base before wildcard", []string{"example.test", "*.example.test"}, []Validation{{"example.test", "txt_dns_record"}, {"*.example.test", "txt_dns_record"}}, []Validation{{"example.test", "txt_dns_record"}}, false},
		{"conflicting methods", []string{"example.test", "*.example.test"}, []Validation{{"example.test", "txt_http_file"}, {"*.example.test", "txt_dns_record"}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, orders := 0, 0
			app, secret, token := credential(t, 8), credential(t, 16), credential(t, 16)
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/auth/login":
					json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in_seconds": 3600})
				case "/v1/orders":
					orders++
					var in struct {
						Certificate struct {
							DNSNames []string `json:"dns_names"`
						} `json:"certificate"`
						Validations []Validation `json:"domain_validations"`
					}
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error("invalid order JSON")
					}
					if !reflect.DeepEqual(in.Certificate.DNSNames, tc.dns) {
						t.Error("certificate SANs changed")
					}
					if !reflect.DeepEqual(in.Validations, tc.want) {
						t.Errorf("validation mapping: got %#v want %#v", in.Validations, tc.want)
					}
					json.NewEncoder(w).Encode(map[string]string{"order_code": "local-contract-order", "order_status": "pending"})
				default:
					t.Error("unexpected request")
				}
			}))
			defer srv.Close()
			client, err := NewTLSManager(srv.URL, app, secret)
			if err != nil {
				t.Fatal(err)
			}
			client.client = srv.Client()
			spec := Spec{DNSNames: tc.dns, Algorithm: "rsa2048", ProductID: 1, ValidityDays: 90, SigningCA: "automatic", Validations: tc.validations}
			if err := spec.Validate(); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(spec)
			_, err = client.PlaceOrder(context.Background(), spec, "local-public-csr-placeholder", Subscriber{})
			after, _ := json.Marshal(spec)
			if string(before) != string(after) {
				t.Error("input Spec mutated")
			}
			if tc.conflict {
				if err == nil || !strings.Contains(err.Error(), "검증 방식") {
					t.Error("expected explicit validation-method conflict")
				}
				if requests != 0 {
					t.Errorf("conflict sent %d requests", requests)
				}
			} else if err != nil || orders != 1 {
				t.Errorf("order failed: %v, requests=%d", err, orders)
			}
		})
	}
}

func TestSubscriberOrderApplicantRole(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []string
		valid bool
	}{
		{"organization", []string{"organization"}, true},
		{"multiple roles", []string{"technical", "organization", "billing"}, true},
		{"technical only", []string{"technical"}, false},
		{"billing only", []string{"billing"}, false},
		{"no organization", []string{"technical", "billing"}, false},
		{"invalid extra role", []string{"organization", "unknown"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subscriber := Subscriber{ExistingID: 1, Contact: Contact{New: &NewContact{LastName: "Test", Email: "applicant@example.test", Types: tc.roles}}}
			if got := subscriber.Validate() == nil; got != tc.valid {
				t.Errorf("valid=%v want %v", got, tc.valid)
			}
		})
	}
	if err := (Subscriber{ExistingID: 1, Contact: Contact{ExistingID: 2}}).Validate(); err != nil {
		t.Errorf("existing contact role must be checked by CA: %v", err)
	}
}
