package store

// 인증서 인벤토리 내보내기 (M3 감사 축 — 02-goals.md 성공 기준 5).
// 목록 조회와 같은 검색·필터·정렬 조건을 받되 페이지네이션 없이 전체를 반환한다.
// 내보내기는 데이터 유출 경로이므로 예외 없이 감사 이벤트를 남긴다
// (00-spec 7.12 "데이터 내보내기", 보안 하드 규칙 5) — 감사 삽입이 실패하면
// 내보내기도 실패한다(같은 트랜잭션, 삽입은 트랜잭션 마지막: advisory lock 규약).

import (
	"context"
	"fmt"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// EventCertificateExport는 데이터 내보내기 감사 이벤트 타입이다.
const EventCertificateExport = "certificate.export"

// maxExportRows는 내보내기 1회 행 상한이다 (메모리 보호. Phase 1 규모 1만 건 여유).
const maxExportRows = 50000

// ExportInput은 내보내기 1회의 조회 조건 + 감사 컨텍스트다.
// List의 Page/PageSize는 무시된다 — 내보내기는 조건 전체를 담는다.
type ExportInput struct {
	List      ListCertificatesInput
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// ExportCertificates는 조건에 맞는 인증서 전체를 조회하고 certificate.export
// 감사 이벤트를 같은 트랜잭션으로 남긴다. 감사 실패 시 내보내기도 실패한다.
// 반환 데이터는 인벤토리 메타데이터뿐이다 — 인증서 원문·개인키는 저장하지 않으므로
// 포함될 수 없다 (보안 하드 규칙 1·3).
func (s *Store) ExportCertificates(ctx context.Context, in ExportInput) ([]domain.Certificate, error) {
	sortKey := in.List.SortField
	if sortKey == "" {
		sortKey = "not_after"
	}
	col, ok := listSortColumns[sortKey]
	if !ok {
		return nil, fmt.Errorf("store: sort field %q not allowed", in.List.SortField)
	}
	dir := "ASC"
	if in.List.SortDesc {
		dir = "DESC"
	}

	conds, args := buildListWhere(in.List)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	query := fmt.Sprintf("SELECT %s FROM certificates%s ORDER BY %s %s, id ASC LIMIT $%d",
		certificateColumns, where, col, dir, len(args)+1)
	rows, err := tx.QueryContext(ctx, query, append(args, maxExportRows)...)
	if err != nil {
		return nil, fmt.Errorf("store: export certificates: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []domain.Certificate{}
	for rows.Next() {
		c, err := scanCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan certificate: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate certificates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("store: close rows: %w", err)
	}

	// after_value에는 조회 조건과 건수만 담는다 — 인증서 데이터 자체는 넣지 않는다.
	after := struct {
		Format    string   `json:"format"`
		RowCount  int      `json:"row_count"`
		Truncated bool     `json:"truncated"` // maxExportRows 도달 여부
		Search    string   `json:"search,omitempty"`
		Statuses  []string `json:"statuses,omitempty"`
		// ExcludeStatuses는 "무엇을 빼고 내보냈나"다. 제외 조건이 감사에 안 남으면
		// 같은 조건이라고 적힌 두 CSV의 행 수가 달라진다(설계 06 14.2 ⑥).
		ExcludeStatuses []string `json:"exclude_statuses,omitempty"`
		Types           []string `json:"types,omitempty"`
	}{Format: "csv", RowCount: len(items), Truncated: len(items) == maxExportRows,
		Search: in.List.Search}
	for _, st := range in.List.Statuses {
		after.Statuses = append(after.Statuses, string(st))
	}
	for _, st := range in.List.ExcludeStatuses {
		after.ExcludeStatuses = append(after.ExcludeStatuses, string(st))
	}
	for _, ct := range in.List.Types {
		after.Types = append(after.Types, string(ct))
	}
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventCertificateExport,
		ActorID:    in.ActorID,
		TargetType: "certificate",
		RequestID:  in.RequestID,
		After:      after,
		Result:     domain.AuditSuccess,
		SourceIP:   in.SourceIP,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return items, nil
}
