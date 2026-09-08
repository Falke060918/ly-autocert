package api

import (
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
	"net/http"
)

func parseCertificateScope(r *http.Request) (store.CertificateScope, *paramError) {
	values, ok := r.URL.Query()["scope"]
	if !ok {
		return store.CertificateScopeInventory, nil
	}
	if len(values) == 1 {
		switch store.CertificateScope(values[0]) {
		case store.CertificateScopeInventory, store.CertificateScopeServer:
			return store.CertificateScope(values[0]), nil
		}
	}
	return "", &paramError{"invalid_scope", "scope는 server 또는 inventory여야 합니다."}
}
