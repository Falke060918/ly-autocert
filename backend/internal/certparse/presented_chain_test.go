package certparse

// 픽스처는 전부 테스트 실행 시점에 생성한다 — 실제 키·인증서를 커밋하지 않는다
// (CLAUDE.md 보안 하드 규칙 4).

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// signed는 parent(nil이면 자기 자신)로 서명한 인증서를 만든다.
func signed(t *testing.T, tmpl *x509.Certificate, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t)
	signer, signerKey := parent, parentKey
	if signer == nil {
		signer, signerKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert, key
}

func caTmpl(cn string, ski []byte) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		SubjectKeyId:          ski,
	}
}

func leafTmpl(cn string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}
}

// threeTier는 루트 → 중간 → 리프를 만든다 (제시 순서가 아니라 신뢰 순서다).
func threeTier(t *testing.T) (root, inter, leaf *x509.Certificate) {
	t.Helper()
	root, rootKey := signed(t, caTmpl("presented-root", []byte{0x01, 0x01}), nil, nil)
	inter, interKey := signed(t, caTmpl("presented-inter", []byte{0x02, 0x02}), root, rootKey)
	leaf, _ = signed(t, leafTmpl("presented-leaf.example.test"), inter, interKey)
	return root, inter, leaf
}

func TestAnalyzePresentedChainComplete(t *testing.T) {
	root, inter, leaf := threeTier(t)

	cases := []struct {
		name     string
		chain    []*x509.Certificate
		wantLen  int
		complete bool
	}{
		// 루트까지 제시 — 완전하다.
		{"leaf+inter+root", []*x509.Certificate{leaf, inter, root}, 3, true},
		// 루트 생략은 표준 관행이다. 불완전으로 세면 제대로 설정된 서버가 전부 danger가 된다.
		{"leaf+inter (root omitted)", []*x509.Certificate{leaf, inter}, 2, true},
		// 리프만 제시 — 중간 CA 누락(풀체인 미배포). 이것이 잡으려는 것이다.
		{"leaf only", []*x509.Certificate{leaf}, 1, false},
		// 중간이 빠지고 루트만 붙어 있어도 리프에서 위로 이어지지 않는다.
		{"leaf+root (intermediate missing)", []*x509.Certificate{leaf, root}, 2, false},
		// 자체 서명 리프 하나는 그 자체로 끝이다.
		{"self-signed alone", []*x509.Certificate{root}, 1, true},
		// 관측 자체가 없는 경우.
		{"empty", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzePresentedChain(tc.chain)
			if got.Length != tc.wantLen {
				t.Errorf("Length = %d, want %d", got.Length, tc.wantLen)
			}
			if got.Complete != tc.complete {
				t.Errorf("Complete = %v, want %v", got.Complete, tc.complete)
			}
		})
	}
}

// 인벤토리로 보완하지 않는다는 계약의 반대 방향: 같은 리프라도 관측마다
// 제시된 체인이 다르면 판정도 달라야 한다.
func TestAnalyzePresentedChainIsPerObservation(t *testing.T) {
	_, inter, leaf := threeTier(t)
	if AnalyzePresentedChain([]*x509.Certificate{leaf}).Complete {
		t.Error("리프만 제시한 관측이 complete로 나왔다")
	}
	if !AnalyzePresentedChain([]*x509.Certificate{leaf, inter}).Complete {
		t.Error("중간 CA를 함께 제시한 관측이 incomplete로 나왔다")
	}
}

// 제시 순서가 뒤집혀 있어도(리프가 뒤) 판정은 같아야 한다 — 순서가 아니라
// 발급 관계로 걷기 때문이다.
func TestAnalyzePresentedChainIgnoresOrder(t *testing.T) {
	root, inter, leaf := threeTier(t)
	got := AnalyzePresentedChain([]*x509.Certificate{root, inter, leaf})
	if !got.Complete || got.Length != 3 {
		t.Errorf("역순 체인 판정 = %+v, want complete=true len=3", got)
	}
}

// 무관한 인증서를 아무리 붙여도 리프의 상위가 없으면 불완전이다.
func TestAnalyzePresentedChainUnrelatedFiller(t *testing.T) {
	_, _, leaf := threeTier(t)
	other, _ := signed(t, caTmpl("presented-unrelated", []byte{0x09, 0x09}), nil, nil)
	got := AnalyzePresentedChain([]*x509.Certificate{leaf, other})
	if got.Complete {
		t.Error("무관한 CA가 붙었다는 이유로 complete가 됐다")
	}
	if got.Length != 2 {
		t.Errorf("Length = %d, want 2", got.Length)
	}
}
