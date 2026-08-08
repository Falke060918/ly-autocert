package sensor

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "https://autocert.example:3000", want: "https://autocert.example:3000"},
		{in: "https://autocert.example:3000/", want: "https://autocert.example:3000"},
		{in: "  https://10.0.0.1:8443  ", want: "https://10.0.0.1:8443"},
		{in: "http://autocert.example:3000", wantErr: true}, // mTLS 1경로 — https만
		{in: "https://autocert.example:3000/api", wantErr: true},
		{in: "autocert.example", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := normalizeServerURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeServerURL(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeServerURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeServerURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLeaseNoContent는 서버 204를 (nil, nil)로 옮기는지와 mTLS·X-Request-ID 규약을 본다.
func TestLeaseNoContent(t *testing.T) {
	ca := newTestCA(t)
	id := newEnrolledIdentity(t, ca, t.TempDir(), time.Now().Add(24*time.Hour))

	gotRequestID := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sensor/v1/jobs/lease", func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotRequestID <- r.Header.Get("X-Request-ID"):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ts := newMTLSGateway(t, ca, mux)

	client, err := NewClient(ts.URL, id)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	job, err := client.Lease(context.Background())
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if job != nil {
		t.Fatalf("expected nil job on 204, got %+v", job)
	}
	if rid := <-gotRequestID; rid == "" {
		t.Fatal("X-Request-ID header missing")
	}
}
