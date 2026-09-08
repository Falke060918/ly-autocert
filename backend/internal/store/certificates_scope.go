package store

// CertificateScope selects inventory or the last verified current server bindings.
// An absent API scope preserves existing inventory behavior.
type CertificateScope string

const (
	CertificateScopeInventory CertificateScope = "inventory"
	CertificateScopeServer    CertificateScope = "server"
)

func selectedCertificateScope(scopes []CertificateScope) CertificateScope {
	if len(scopes) > 0 {
		return scopes[0]
	}
	return CertificateScopeInventory
}

// alias is an internal SQL identifier, never request input. EXISTS keeps one
// certificate from being counted repeatedly across endpoints and deployments.
func certificateScopeSQL(scope CertificateScope, alias string) string {
	if scope != CertificateScopeServer {
		return "TRUE"
	}
	return `(` + alias + `.certificate_type <> 'ca' AND (
		EXISTS (SELECT 1 FROM endpoint_monitors em
		 WHERE em.current_fingerprint = ` + alias + `.fingerprint_sha256
		   AND em.last_success_at IS NOT NULL AND em.retired_at IS NULL)
		OR EXISTS (SELECT 1 FROM deployment_targets dt
		 WHERE dt.active AND dt.current_certificate_id = ` + alias + `.id
		   AND EXISTS (SELECT 1 FROM deploy_jobs dj
		     WHERE dj.deployment_target_id = dt.id AND dj.certificate_id = ` + alias + `.id
		       AND dj.status = 'completed' AND dj.observed_fingerprint = ` + alias + `.fingerprint_sha256))
	))`
}

func certificateAggregateScopeSQL(scope CertificateScope, alias string) string {
	if scope == CertificateScopeServer {
		return certificateScopeSQL(scope, alias)
	}
	return alias + `.status <> 'replaced'`
}
