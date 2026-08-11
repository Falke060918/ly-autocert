package certparse

// presented_chain.go — 엔드포인트가 **제시한** 체인 자체의 판정 (0018).
//
// 인벤토리의 다른 인증서로 보완하지 않는다. 보완하면 판정이 무의미해진다 —
// 다른 엔드포인트가 제시한 중간 CA가 인벤토리에 있다는 이유로 체인이 완성돼
// 보이는 것이 0018 이전의 상태였다. 브라우저는 AIA로 자동 보완하지만
// curl·Java·Python·모바일 클라이언트는 중간 CA가 빠지면 즉시 실패한다.
//
// 이 파일은 DB·네트워크에 의존하지 않는 순수 함수다.
// 입력(인증서 바이트)은 오류·로그에 싣지 않는다 (보안 하드 규칙 3).

import (
	"bytes"
	"crypto/x509"
)

// maxPresentedChainDepth는 탐색 깊이 상한이다. 방문 집합과 함께 교차 서명 순환을 막는다
// (store.maxChainDepth와 같은 값·같은 목적).
const maxPresentedChainDepth = 8

// PresentedChain은 관측 1건의 체인 판정 결과다.
type PresentedChain struct {
	// Length는 서버가 제시한 인증서 장수다.
	Length int
	// Complete는 제시된 것만으로 리프에서 위로 끊김 없이 이어지고, 마지막에 도달한
	// 인증서가 CA(또는 자체 서명 루트)인지다.
	//
	// **루트를 보내지 않는 것은 불완전이 아니다.** 루트는 클라이언트 신뢰 저장소에
	// 있어야 검증되는 것이고 서버가 보내는 것은 대역폭 낭비라 보내지 않는 쪽이
	// 표준 관행이다. 이것을 불완전으로 세면 제대로 설정된 서버가 전부 danger가 되고
	// 판정 자체가 무시된다(certrisk 패키지 주석의 "전건 빨간불" 실패 모드).
	// 잡으려는 것은 중간 CA 누락(풀체인 미배포)이다: 리프만 보내거나 중간이 빠져
	// 위로 이어지지 않는 경우다.
	Complete bool
}

// AnalyzePresentedChain은 제시된 체인을 판정한다. 체인이 비어 있으면 Length 0 ·
// Complete false이며, 호출부는 이 경우 저장하지 않는다(관측 자체가 없다).
func AnalyzePresentedChain(chain []*x509.Certificate) PresentedChain {
	out := PresentedChain{Length: len(chain)}
	if len(chain) == 0 {
		return out
	}

	// 리프 = 첫 비-CA 인증서(전부 CA면 [0]). store의 위치 부착 규약과 같다 —
	// 판정 대상과 위치가 붙는 인증서가 갈리면 안 된다.
	cur := chain[0]
	for _, c := range chain {
		if !c.IsCA {
			cur = c
			break
		}
	}

	visited := map[*x509.Certificate]bool{cur: true}
	for depth := 0; depth < maxPresentedChainDepth; depth++ {
		if isSelfSigned(cur) {
			out.Complete = true // 루트까지 제시됨
			return out
		}
		parent := findPresentedIssuer(chain, cur, visited)
		if parent == nil {
			// 더 올라갈 수 없다. 여기까지 온 인증서가 CA면 "루트만 생략"이고,
			// 아니면(리프 혼자·중간이 빠짐) 중간 CA 누락이다.
			out.Complete = cur.IsCA
			return out
		}
		visited[parent] = true
		cur = parent
	}
	// 깊이 상한까지 이어졌다면 끊긴 곳은 없다.
	out.Complete = true
	return out
}

// isSelfSigned는 자체 서명(=루트) 여부다. DN이 같아도 AKI·SKI가 모두 있고 값이
// 다르면 자체 서명이 아니다 (store.chainRow.selfSigned와 같은 규칙 — 동일 DN으로
// 교차 서명된 경우를 루트로 착각하지 않는다).
func isSelfSigned(c *x509.Certificate) bool {
	if !bytes.Equal(c.RawSubject, c.RawIssuer) {
		return false
	}
	if len(c.AuthorityKeyId) > 0 && len(c.SubjectKeyId) > 0 &&
		!bytes.Equal(c.AuthorityKeyId, c.SubjectKeyId) {
		return false
	}
	return true
}

// findPresentedIssuer는 제시된 체인 안에서 child의 상위를 찾는다. 없으면 nil.
// 매칭 규칙은 체인 조회(store.findIssuer)와 같다:
//  1. child의 AKI = 후보의 SKI 정확 일치 (우선)
//  2. 키 식별자가 없으면 issuer DN = subject DN 폴백
//
// 양쪽 다 키 식별자를 갖고 있는데 값이 다르면 DN이 같아도 상위로 보지 않는다.
func findPresentedIssuer(chain []*x509.Certificate, child *x509.Certificate, visited map[*x509.Certificate]bool) *x509.Certificate {
	var fallback *x509.Certificate
	for _, c := range chain {
		if visited[c] || !bytes.Equal(c.RawSubject, child.RawIssuer) {
			continue
		}
		if len(child.AuthorityKeyId) > 0 && len(c.SubjectKeyId) > 0 {
			if bytes.Equal(child.AuthorityKeyId, c.SubjectKeyId) {
				return c
			}
			continue // 키 식별자가 양쪽에 있는데 다르다 — 상위가 아니다.
		}
		if fallback == nil {
			fallback = c
		}
	}
	return fallback
}
