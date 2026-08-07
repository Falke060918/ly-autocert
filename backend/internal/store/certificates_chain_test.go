package store

// 인증서 체인 관계 조회(M3) store 계층 테스트.
// 픽스처는 실행 시점에 합성한다 — 실제 키·인증서를 커밋하지 않는다 (보안 하드 규칙 4).
// fingerprint·SKI/AKI는 결정적 SHA-256(테스트 라벨)이며 실제 인증서에서 나온 값이 아니다.
// TEST_DATABASE_URL 미설정 시 스킵 (전용 테스트 DB — 메인 DB에 돌리지 않는다).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// chainFixture는 체인 테스트용 인증서 1건이다.
type chainFixture struct {
	cn       string
	subject  string // 비우면 "CN="+cn
	issuer   string
	ctype    domain.CertificateType
	ski, aki *string // nil이면 NULL (0008 이전 데이터 시뮬레이션)
}

// keyID는 라벨에서 결정적 소문자 hex 키 식별자를 만든다 (합성 값).
func keyID(label string) *string {
	sum := sha256.Sum256([]byte("chain-key:" + label))
	s := hex.EncodeToString(sum[:20]) // SKI 관례상 20바이트
	return &s
}

func insertChainFixture(t *testing.T, pool *sql.DB, f chainFixture) string {
	t.Helper()
	subject := f.subject
	if subject == "" {
		subject = "CN=" + f.cn
	}
	sum := sha256.Sum256([]byte("chain-fixture:" + f.cn + ":" + subject))
	var id string
	err := pool.QueryRow(`
		INSERT INTO certificates (
			fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name,
			not_before, not_after, public_key_algorithm, public_key_size,
			signature_algorithm, certificate_type, status, subject_key_id, authority_key_id
		) VALUES (
			$1, $2, $3, $4, $5, now() - interval '30 days', now() + interval '365 days',
			'ECDSA', 256, 'ECDSA-SHA256', $6, 'active', $7, $8
		) RETURNING id`,
		hex.EncodeToString(sum[:]), "cf-"+f.cn, subject, f.issuer, f.cn,
		string(f.ctype), f.ski, f.aki,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert chain fixture %s: %v", f.cn, err)
	}
	return id
}

func chainOf(t *testing.T, st *Store, ctx context.Context, id string) []ChainItem {
	t.Helper()
	d, err := st.GetCertificateDetail(ctx, id)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	return d.Chain
}

func TestCertificateChain(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	t.Run("leaf-intermediate-root by AKI/SKI", func(t *testing.T) {
		rootKey := keyID("root")
		interKey := keyID("inter")
		rootID := insertChainFixture(t, pool, chainFixture{
			cn: "chain-root-ca", issuer: "CN=chain-root-ca",
			ctype: domain.CertTypeCA, ski: rootKey, aki: rootKey})
		interID := insertChainFixture(t, pool, chainFixture{
			cn: "chain-inter-ca", issuer: "CN=chain-root-ca",
			ctype: domain.CertTypeCA, ski: interKey, aki: rootKey})
		leafID := insertChainFixture(t, pool, chainFixture{
			cn: "chain-leaf.example.test", issuer: "CN=chain-inter-ca",
			ctype: domain.CertTypeServer, aki: interKey})

		chain := chainOf(t, st, ctx, leafID)
		if len(chain) != 3 {
			t.Fatalf("chain length = %d, want 3 (leaf→inter→root): %+v", len(chain), chain)
		}
		wantIDs := []string{leafID, interID, rootID}
		for i, want := range wantIDs {
			if !chain[i].Stored || chain[i].ID == nil || *chain[i].ID != want {
				t.Errorf("chain[%d] = %+v, want stored id %s", i, chain[i], want)
			}
		}
		if chain[0].SelfSigned || chain[1].SelfSigned || !chain[2].SelfSigned {
			t.Errorf("self_signed flags = [%v %v %v], want [false false true]",
				chain[0].SelfSigned, chain[1].SelfSigned, chain[2].SelfSigned)
		}
		if chain[2].NotAfter == nil || chain[2].CommonName == nil || *chain[2].CommonName != "chain-root-ca" {
			t.Errorf("root item lacks common_name/not_after: %+v", chain[2])
		}
	})

	t.Run("missing parent yields stored=false placeholder", func(t *testing.T) {
		leafID := insertChainFixture(t, pool, chainFixture{
			cn: "orphan.example.test", issuer: "CN=absent-ca",
			ctype: domain.CertTypeServer, aki: keyID("absent")})

		chain := chainOf(t, st, ctx, leafID)
		if len(chain) != 2 {
			t.Fatalf("chain length = %d, want 2: %+v", len(chain), chain)
		}
		top := chain[1]
		if top.Stored || top.ID != nil {
			t.Errorf("missing parent = %+v, want stored=false id=nil", top)
		}
		if top.SubjectDN != "CN=absent-ca" {
			t.Errorf("missing parent subject_dn = %q, want CN=absent-ca (하위의 issuer_dn)", top.SubjectDN)
		}
	})

	t.Run("legacy rows without key ids fall back to DN matching", func(t *testing.T) {
		// 0008 이전 데이터: SKI/AKI 전부 NULL — issuer_dn = subject_dn으로만 연결.
		rootID := insertChainFixture(t, pool, chainFixture{
			cn: "legacy-root-ca", issuer: "CN=legacy-root-ca", ctype: domain.CertTypeCA})
		leafID := insertChainFixture(t, pool, chainFixture{
			cn: "legacy-leaf.example.test", issuer: "CN=legacy-root-ca", ctype: domain.CertTypeServer})

		chain := chainOf(t, st, ctx, leafID)
		if len(chain) != 2 {
			t.Fatalf("chain length = %d, want 2: %+v", len(chain), chain)
		}
		if !chain[1].Stored || chain[1].ID == nil || *chain[1].ID != rootID || !chain[1].SelfSigned {
			t.Errorf("legacy parent = %+v, want stored root %s (self_signed)", chain[1], rootID)
		}
	})

	t.Run("mixed: new child with AKI matches legacy parent without SKI", func(t *testing.T) {
		rootID := insertChainFixture(t, pool, chainFixture{
			cn: "mixed-root-ca", issuer: "CN=mixed-root-ca", ctype: domain.CertTypeCA})
		leafID := insertChainFixture(t, pool, chainFixture{
			cn: "mixed-leaf.example.test", issuer: "CN=mixed-root-ca",
			ctype: domain.CertTypeServer, aki: keyID("mixed")})

		chain := chainOf(t, st, ctx, leafID)
		if len(chain) != 2 || !chain[1].Stored || *chain[1].ID != rootID {
			t.Errorf("mixed chain = %+v, want DN-fallback parent %s", chain, rootID)
		}
	})

	t.Run("same DN different keys: SKI decides, DN does not mismatch", func(t *testing.T) {
		// 동일 DN으로 재발급된 CA 2개 — AKI가 가리키는 쪽만 상위여야 한다.
		oldKey := keyID("samedn-old")
		newKey := keyID("samedn-new")
		insertChainFixture(t, pool, chainFixture{
			cn: "samedn-old-ca", subject: "CN=samedn-ca", issuer: "CN=samedn-ca",
			ctype: domain.CertTypeCA, ski: oldKey, aki: oldKey})
		newID := insertChainFixture(t, pool, chainFixture{
			cn: "samedn-new-ca", subject: "CN=samedn-ca", issuer: "CN=samedn-ca",
			ctype: domain.CertTypeCA, ski: newKey, aki: newKey})
		leafID := insertChainFixture(t, pool, chainFixture{
			cn: "samedn-leaf.example.test", issuer: "CN=samedn-ca",
			ctype: domain.CertTypeServer, aki: newKey})

		chain := chainOf(t, st, ctx, leafID)
		if len(chain) != 2 || chain[1].ID == nil || *chain[1].ID != newID {
			t.Errorf("chain = %+v, want parent %s (AKI가 가리키는 새 CA)", chain, newID)
		}
	})

	t.Run("self-signed leaf is a single-item chain", func(t *testing.T) {
		selfKey := keyID("selfsigned")
		id := insertChainFixture(t, pool, chainFixture{
			cn: "selfsigned.example.test", issuer: "CN=selfsigned.example.test",
			ctype: domain.CertTypeServer, ski: selfKey, aki: selfKey})

		chain := chainOf(t, st, ctx, id)
		if len(chain) != 1 || !chain[0].SelfSigned {
			t.Errorf("chain = %+v, want single self_signed item", chain)
		}
	})

	t.Run("cross-signed cycle terminates", func(t *testing.T) {
		// 데이터 오류·교차 서명으로 A↔B 순환이 생겨도 탐색이 끝나야 한다.
		aKey := keyID("cycle-a")
		bKey := keyID("cycle-b")
		aID := insertChainFixture(t, pool, chainFixture{
			cn: "cycle-a-ca", issuer: "CN=cycle-b-ca",
			ctype: domain.CertTypeCA, ski: aKey, aki: bKey})
		insertChainFixture(t, pool, chainFixture{
			cn: "cycle-b-ca", issuer: "CN=cycle-a-ca",
			ctype: domain.CertTypeCA, ski: bKey, aki: aKey})

		chain := chainOf(t, st, ctx, aID)
		if len(chain) > maxChainDepth {
			t.Errorf("chain length = %d, exceeds maxChainDepth %d", len(chain), maxChainDepth)
		}
	})
}

// TestImportBackfillsKeyIDs는 fingerprint 중복 재업로드 시 NULL 키 식별자가
// 백필되는지 검증한다 (0008 이전 데이터의 회복 경로).
func TestImportBackfillsKeyIDs(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	clearInventory(t, pool)
	t.Cleanup(func() { clearInventory(t, pool) })

	sum := sha256.Sum256([]byte("backfill-fixture"))
	fp := hex.EncodeToString(sum[:])
	base := domain.Certificate{
		FingerprintSHA256: fp, SerialNumber: "bf-01",
		SubjectDN: "CN=backfill.example.test", IssuerDN: "CN=backfill-ca",
		CommonName: "backfill.example.test", SANs: []string{},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		PublicKeyAlgorithm: "ECDSA", PublicKeySize: 256, SignatureAlgorithm: "ECDSA-SHA256",
		CertificateType: domain.CertTypeServer, Status: domain.CertStatusActive,
	}

	// 1차 업로드: 키 식별자 없음 (0008 이전 파서 결과 시뮬레이션).
	if _, err := st.ImportCertificates(ctx, ImportInput{Certificates: []domain.Certificate{base}}); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// 2차 업로드(중복): 같은 인증서에 AKI가 있으면 백필돼야 한다.
	withKey := base
	withKey.AuthorityKeyID = keyID("backfill-parent")
	res, err := st.ImportCertificates(ctx, ImportInput{Certificates: []domain.Certificate{withKey}})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Certificates[0].Created {
		t.Fatal("second import created a new row, want duplicate reuse")
	}

	var aki *string
	if err := pool.QueryRow(
		`SELECT authority_key_id FROM certificates WHERE fingerprint_sha256 = $1`, fp).Scan(&aki); err != nil {
		t.Fatalf("select aki: %v", err)
	}
	if aki == nil || *aki != *withKey.AuthorityKeyID {
		t.Errorf("authority_key_id = %v, want backfilled %s", aki, *withKey.AuthorityKeyID)
	}
}
