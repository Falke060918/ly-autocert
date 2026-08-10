package domain

import "time"

// AdminIPRule은 관리 콘솔 접근을 허용하는 출발지 IP 규칙 1건이다 (마이그레이션 0014).
//
// 활성 규칙이 하나도 없으면 제한 자체가 없다(전체 허용) — 기본 설치가 스스로
// 잠기지 않게 하는 전제이며, 미들웨어·API·화면이 모두 이 규약을 공유한다.
//
// CIDR은 항상 정규화된 표기다: 단일 IP도 /32(IPv6는 /128)로 저장한다.
type AdminIPRule struct {
	ID          string    `json:"id"`
	CIDR        string    `json:"cidr"`
	Description string    `json:"description"`
	IsActive    bool      `json:"is_active"`
	CreatedBy   *string   `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
