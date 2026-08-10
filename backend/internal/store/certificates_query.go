package store

// 인증서 목록·상세 조회 (이슈 #4).
// 정렬 컬럼은 화이트리스트로만 받는다 — 사용자 입력이 SQL 조각으로 들어가지 않는다.
// 검색·필터 값은 전부 바인드 파라미터다.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// ErrNotFound는 조회 대상 리소스가 없을 때 반환한다.
var ErrNotFound = errors.New("store: not found")

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// listSortColumns는 목록 정렬 허용 컬럼 화이트리스트다 (키 = API sort 값).
var listSortColumns = map[string]string{
	"not_after":        "not_after",
	"not_before":       "not_before",
	"common_name":      "common_name",
	"created_at":       "created_at",
	"updated_at":       "updated_at",
	"status":           "status",
	"certificate_type": "certificate_type",
	"public_key_size":  "public_key_size",
}

// IsListSortField는 name이 정렬 허용 필드인지 반환한다 (API 파라미터 검증용).
func IsListSortField(name string) bool {
	_, ok := listSortColumns[name]
	return ok
}

// ListCertificatesInput은 목록 조회 조건이다. 제로값이면 전체를 기본 정렬로 반환한다.
type ListCertificatesInput struct {
	Search   string                     // CN·Subject·Issuer·Serial·Fingerprint·SAN 부분 일치 (대소문자 무시)
	Statuses []domain.CertificateStatus // 저장된 라이프사이클 상태 필터
	// ExcludeStatuses는 결과에서 뺄 상태다(교체된 인증서 숨기기 등).
	// **비어 있으면 아무것도 제외하지 않는다** — 서버 기본 동작을 바꾸지 않는 것이
	// 이 필드의 전제다. 같은 입력을 CSV 내보내기가 공유하므로(certificates_export.go)
	// 여기에 기본 제외를 넣으면 아무도 요청하지 않은 내보내기 내용 변경이 따라온다
	// (설계 06 14.2 ⑥). 제외는 호출자가 명시적으로 요청한다.
	ExcludeStatuses   []domain.CertificateStatus
	Types             []domain.CertificateType
	Expired           *bool  // 파생 상태: not_after 기준 만료 여부
	ExpiresWithinDays *int   // 파생 상태: 오늘부터 N일 이내 만료(이미 만료된 것 제외)
	SelfSigned        *bool  // 파생 상태: subject_dn = issuer_dn (이슈 #8 대시보드 이동용)
	Unowned           *bool  // 파생 상태: owner_id IS NULL
	SortField         string // listSortColumns 키. 빈 값이면 not_after
	SortDesc          bool
	Page              int // 1부터
	PageSize          int // 1..maxPageSize
}

// CertificateListItem은 목록 1행이다 — 인증서 + 목록 전용 파생 열.
//
// LastSeenAt을 domain.Certificate에 넣지 않는 이유는 그것이 인증서의 속성이
// 아니라 설치 위치 집계이기 때문이다. 상세 응답은 위치 배열을 통째로 내려주므로
// 이 파생 열이 필요 없고, CSV 내보내기도 인증서 열만 담는다.
type CertificateListItem struct {
	domain.Certificate
	// LastSeenAt은 이 인증서의 설치 위치 중 가장 최근 확인 시각이다
	// (max(certificate_locations.last_verified_at)). 위치가 없거나 한 번도
	// 확인되지 않았으면 nil이다. **정렬 화이트리스트에는 넣지 않는다** —
	// 집계 파생값이라 ORDER BY 컬럼으로 안전하게 끼워 넣을 수 없다(설계 06 11.3).
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// CertificateList는 목록 조회 결과다.
type CertificateList struct {
	Items    []CertificateListItem
	Total    int
	Page     int
	PageSize int
}

// TargetRef는 설치 위치에 붙는 대상 시스템 요약이다.
type TargetRef struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	TargetType string  `json:"target_type"`
	Hostname   *string `json:"hostname"`
	IPAddress  *string `json:"ip_address"`
}

// LocationWithTarget은 설치 위치 + 대상 요약이다.
// TLSVersion은 네트워크 스캔(이슈 #5)이 관측한 협상 TLS 버전이다 (0006 마이그레이션).
type LocationWithTarget struct {
	domain.CertificateLocation
	TLSVersion *string   `json:"tls_version"`
	Target     TargetRef `json:"target"`
}

// OwnerRef는 인증서 소유자 표시용 요약이다. 화면이 owner_id(UUID)를 그대로
// 노출하지 않도록 상세 응답에 실어 보낸다 — 목록 API가 System Administrator
// 전용이라 프론트가 스스로 UUID를 이름으로 바꿀 방법이 없기 때문이다.
// 비밀번호·메일 주소 등 계정 정보는 담지 않는다(표시에 필요한 최소 필드만).
type OwnerRef struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
	IsActive    bool    `json:"is_active"`
}

// CertificateDetail은 상세 조회 결과다 (설치 위치 목록 + 체인 포함).
type CertificateDetail struct {
	Certificate domain.Certificate
	// Owner는 owner_id가 가리키는 계정이다. 소유자 미지정이면 nil이고,
	// owner_id가 있는데 계정 행이 없으면(정상 경로에선 FK ON DELETE SET NULL이
	// 막지만) 역시 nil이다 — 호출자는 두 경우를 owner_id 유무로 구분한다.
	Owner     *OwnerRef
	Locations []LocationWithTarget
	Chain     []ChainItem // 자신→상위→루트 순 (certificates_chain.go)
}

// certificateColumns는 scanCertificate와 순서가 일치해야 한다.
// sans는 text[] 제약(패키지 주석 참조) 때문에 chr(31) 연결 문자열로 내린다.
const certificateColumns = `
	id, fingerprint_sha256, serial_number, subject_dn, issuer_dn, common_name,
	array_to_string(sans, chr(31)),
	not_before, not_after, public_key_algorithm, public_key_size, signature_algorithm,
	certificate_type, status, ca_id, owner_id, department_id, environment,
	auto_renew, renewal_policy_id, subject_key_id, authority_key_id,
	replaced_by_certificate_id, replaced_at, created_at, updated_at`

// certificateScanDest는 certificateColumns 순서와 1:1인 Scan 대상 목록이다.
// 컬럼을 늘리면 여기도 같은 자리에 늘린다.
func certificateScanDest(c *domain.Certificate, sans, ctype, status *string) []any {
	return []any{
		&c.ID, &c.FingerprintSHA256, &c.SerialNumber, &c.SubjectDN, &c.IssuerDN, &c.CommonName,
		sans,
		&c.NotBefore, &c.NotAfter, &c.PublicKeyAlgorithm, &c.PublicKeySize, &c.SignatureAlgorithm,
		ctype, status, &c.CAID, &c.OwnerID, &c.DepartmentID, &c.Environment,
		&c.AutoRenew, &c.RenewalPolicyID, &c.SubjectKeyID, &c.AuthorityKeyID,
		&c.ReplacedByCertificateID, &c.ReplacedAt,
		&c.CreatedAt, &c.UpdatedAt,
	}
}

// finishCertificate는 SQL 표현으로 받은 필드를 도메인 타입으로 되돌린다.
func finishCertificate(c *domain.Certificate, sans, ctype, status string) {
	c.CertificateType = domain.CertificateType(ctype)
	c.Status = domain.CertificateStatus(status)
	c.SANs = []string{}
	if sans != "" {
		c.SANs = strings.Split(sans, sansSep)
	}
}

func scanCertificate(sc interface{ Scan(...any) error }) (domain.Certificate, error) {
	var c domain.Certificate
	var sans, ctype, status string
	if err := sc.Scan(certificateScanDest(&c, &sans, &ctype, &status)...); err != nil {
		return c, err
	}
	finishCertificate(&c, sans, ctype, status)
	return c, nil
}

// scanCertificateWithLastSeen은 certificateColumns 뒤에 last_seen_at 파생 열이
// 하나 더 붙은 행을 읽는다(목록 전용).
func scanCertificateWithLastSeen(sc interface{ Scan(...any) error }, lastSeen **time.Time) (domain.Certificate, error) {
	var c domain.Certificate
	var sans, ctype, status string
	dest := append(certificateScanDest(&c, &sans, &ctype, &status), lastSeen)
	if err := sc.Scan(dest...); err != nil {
		return c, err
	}
	finishCertificate(&c, sans, ctype, status)
	return c, nil
}

// escapeLike는 LIKE 패턴 메타문자를 이스케이프한다 (ESCAPE '\'와 짝).
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// buildListWhere는 WHERE 조건과 바인드 파라미터를 만든다.
func buildListWhere(in ListCertificatesInput) (conds []string, args []any) {
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if in.Search != "" {
		ph := arg("%" + escapeLike(in.Search) + "%")
		fields := []string{
			"common_name", "subject_dn", "issuer_dn", "serial_number",
			"fingerprint_sha256", "array_to_string(sans, chr(31))",
		}
		ors := make([]string, len(fields))
		for i, f := range fields {
			ors[i] = fmt.Sprintf(`%s ILIKE %s ESCAPE '\'`, f, ph)
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	if len(in.Statuses) > 0 {
		vals := make([]string, len(in.Statuses))
		for i, s := range in.Statuses {
			vals[i] = string(s)
		}
		conds = append(conds, fmt.Sprintf("status = ANY(string_to_array(%s, chr(31)))",
			arg(strings.Join(vals, sansSep))))
	}
	// 제외 필터는 포함 필터와 독립이다(둘 다 지정하면 AND로 겹친다).
	// 빈 슬라이스면 조건 자체를 만들지 않는다 — 기본 동작 불변이 이 필드의 계약이다.
	if len(in.ExcludeStatuses) > 0 {
		vals := make([]string, len(in.ExcludeStatuses))
		for i, s := range in.ExcludeStatuses {
			vals[i] = string(s)
		}
		conds = append(conds, fmt.Sprintf("status <> ALL(string_to_array(%s, chr(31)))",
			arg(strings.Join(vals, sansSep))))
	}
	if len(in.Types) > 0 {
		vals := make([]string, len(in.Types))
		for i, ct := range in.Types {
			vals[i] = string(ct)
		}
		conds = append(conds, fmt.Sprintf("certificate_type = ANY(string_to_array(%s, chr(31)))",
			arg(strings.Join(vals, sansSep))))
	}
	if in.Expired != nil {
		if *in.Expired {
			conds = append(conds, "not_after < now()")
		} else {
			conds = append(conds, "not_after >= now()")
		}
	}
	if in.ExpiresWithinDays != nil {
		ph := arg(*in.ExpiresWithinDays)
		conds = append(conds, fmt.Sprintf(
			"not_after >= now() AND not_after < now() + %s * interval '1 day'", ph))
	}
	if in.SelfSigned != nil {
		if *in.SelfSigned {
			conds = append(conds, "subject_dn = issuer_dn")
		} else {
			conds = append(conds, "subject_dn <> issuer_dn")
		}
	}
	if in.Unowned != nil {
		if *in.Unowned {
			conds = append(conds, "owner_id IS NULL")
		} else {
			conds = append(conds, "owner_id IS NOT NULL")
		}
	}
	return conds, args
}

// ListCertificates는 검색·필터·정렬·페이지네이션 조건으로 인증서 목록을 조회한다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) ListCertificates(ctx context.Context, in ListCertificatesInput) (*CertificateList, error) {
	page := in.Page
	if page < 1 {
		page = 1
	}
	size := in.PageSize
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	sortKey := in.SortField
	if sortKey == "" {
		sortKey = "not_after"
	}
	col, ok := listSortColumns[sortKey]
	if !ok {
		return nil, fmt.Errorf("store: sort field %q not allowed", in.SortField)
	}
	dir := "ASC"
	if in.SortDesc {
		dir = "DESC"
	}

	conds, args := buildListWhere(in)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM certificates"+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("store: count certificates: %w", err)
	}

	// id 보조 정렬로 페이지 경계를 안정화한다.
	// last_seen_at은 상관 서브쿼리 1개로 붙인다(정렬·필터에는 쓰지 않는다).
	query := fmt.Sprintf(`SELECT %s,
			(SELECT max(l.last_verified_at) FROM certificate_locations l
			  WHERE l.certificate_id = certificates.id)
		 FROM certificates%s ORDER BY %s %s, id ASC LIMIT $%d OFFSET $%d`,
		certificateColumns, where, col, dir, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list certificates: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []CertificateListItem{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		var item CertificateListItem
		c, err := scanCertificateWithLastSeen(rows, &item.LastSeenAt)
		if err != nil {
			return nil, fmt.Errorf("store: scan certificate: %w", err)
		}
		item.Certificate = c
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate certificates: %w", err)
	}
	return &CertificateList{Items: items, Total: total, Page: page, PageSize: size}, nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// GetCertificateDetail은 인증서 1건과 설치 위치 목록(대상 요약 포함)을 조회한다.
// 잘못된 형식의 id는 존재하지 않는 리소스로 취급한다 (uuid 캐스트 오류 방지).
func (s *Store) GetCertificateDetail(ctx context.Context, id string) (*CertificateDetail, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}

	row := s.db.QueryRowContext(ctx,
		"SELECT "+certificateColumns+" FROM certificates WHERE id = $1", id)
	cert, err := scanCertificate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get certificate: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.certificate_id, l.target_id, l.location_type, l.file_path,
			l.store_name, l.service_name, l.port, l.last_verified_at, l.deployment_status,
			l.tls_version,
			t.id, t.name, t.target_type, t.hostname, host(t.ip_address)
		FROM certificate_locations l
		JOIN targets t ON t.id = l.target_id
		WHERE l.certificate_id = $1
		ORDER BY t.name, l.id`, cert.ID)
	if err != nil {
		return nil, fmt.Errorf("store: list locations: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	locations := []LocationWithTarget{}
	for rows.Next() {
		var l LocationWithTarget
		var locType, depStatus string
		if err := rows.Scan(
			&l.ID, &l.CertificateID, &l.TargetID, &locType, &l.FilePath,
			&l.StoreName, &l.ServiceName, &l.Port, &l.LastVerifiedAt, &depStatus,
			&l.TLSVersion,
			&l.Target.ID, &l.Target.Name, &l.Target.TargetType, &l.Target.Hostname, &l.Target.IPAddress,
		); err != nil {
			return nil, fmt.Errorf("store: scan location: %w", err)
		}
		l.LocationType = domain.LocationType(locType)
		l.DeploymentStatus = domain.DeploymentStatus(depStatus)
		locations = append(locations, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate locations: %w", err)
	}

	owner, err := s.getCertificateOwner(ctx, cert.OwnerID)
	if err != nil {
		return nil, err
	}

	chain, err := s.getCertificateChain(ctx, cert)
	if err != nil {
		return nil, err
	}
	return &CertificateDetail{
		Certificate: cert, Owner: owner, Locations: locations, Chain: chain,
	}, nil
}

// getCertificateOwner는 owner_id에 해당하는 계정 요약을 조회한다.
// ownerID가 nil이면 (nil, nil)이고, 계정 행이 사라진 경우에도 오류가 아니라
// (nil, nil)이다 — 소유자 표시 하나 때문에 상세 조회 전체를 실패시키지 않는다.
func (s *Store) getCertificateOwner(ctx context.Context, ownerID *string) (*OwnerRef, error) {
	if ownerID == nil {
		return nil, nil
	}
	var o OwnerRef
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, is_active FROM users WHERE id = $1`, *ownerID,
	).Scan(&o.ID, &o.Username, &o.DisplayName, &o.IsActive)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get certificate owner: %w", err)
	}
	return &o, nil
}
