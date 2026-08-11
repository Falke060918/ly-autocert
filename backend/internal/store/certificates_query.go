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

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/certrisk"
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
	Expired           *bool // 파생 상태: not_after 기준 만료 여부
	ExpiresWithinDays *int  // 파생 상태: 오늘부터 N일 이내 만료(이미 만료된 것 제외)
	SelfSigned        *bool // 파생 상태: subject_dn = issuer_dn (이슈 #8 대시보드 이동용)
	Unowned           *bool // 파생 상태: owner_id IS NULL
	// IssuerOrg는 발급 기관(issuer_organization, 0016) 정확 일치 필터다.
	// 분포 집계(GET /certificates/issuers)에서 클릭해 들어오는 경로라 부분 일치가 아니다.
	// 빈 문자열은 "조직 미상"(issuer_organization IS NULL), nil만 필터 없음이다.
	IssuerOrg *string
	// IssuedBy는 "이 CA 인증서가 발급한 인증서" 필터다 — 값은 발급자 인증서 id.
	// 매칭은 certificates_chain.go findIssuer의 역방향과 같다:
	// SKI=AKI 정확 일치 우선, 키 식별자가 없으면 issuer_dn = subject_dn 폴백.
	IssuedBy *string
	// RiskSeverities는 위험 판정 **최고 심각도** 필터다 (danger/warn/info/ok).
	// 최고 심각도 기준이라 값들이 인벤토리를 겹침 없이 분할한다 — 대시보드 집계
	// (CertificateRisks)에서 숫자를 클릭해 들어오는 경로와 같은 기준이다.
	RiskSeverities []certrisk.Severity
	// RiskCodes는 판정 항목 코드 필터다(해당 항목이 잡힌 인증서). 여러 개면 OR.
	RiskCodes []certrisk.Code
	SortField string // listSortColumns 키. 빈 값이면 not_after
	SortDesc  bool
	Page      int // 1부터
	PageSize  int // 1..maxPageSize
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
	// Risk는 조회 시점에 계산한 위험 판정이다 — **저장하지 않는다**(certrisk 패키지
	// 주석). 목록 경로에서는 위치 축 입력으로 "removed가 아닌 위치가 관측한 TLS 버전"만
	// 싣는다(위치 배열 전체는 상세 응답 소관).
	Risk certrisk.Assessment `json:"risk"`
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
	TLSVersion *string `json:"tls_version"`
	// CurrentFingerprint는 이 위치의 (대상, 포트) 엔드포인트에서 마지막으로 관측된
	// 인증서 지문이다(endpoint_monitors, 0015). 재스캔이 꺼져 있거나 아직 한 번도
	// 성공하지 못했으면 nil이다.
	CurrentFingerprint *string `json:"current_fingerprint"`
	// CurrentCertificateID는 CurrentFingerprint에 해당하는 인벤토리 인증서 id다.
	//
	// 화면은 이 두 필드로 위치를 3분류한다:
	//   - CurrentCertificateID == certificate_id  → 서비스 중
	//   - CurrentCertificateID != certificate_id  → 다른 인증서로 교체됨(구버전 잔존)
	//   - CurrentFingerprint == nil               → 확인 안 됨
	CurrentCertificateID *string `json:"current_certificate_id"`
	// PresentedChainLen·ChainComplete는 이 엔드포인트가 마지막 관측에서 **제시한**
	// 체인의 사실이다 (0018). 인벤토리의 다른 인증서로 보완하지 않은 값이므로
	// chain_complete=false는 곧 중간 CA 누락(풀체인 미배포)이다.
	// 0018 이전 관측·파일 업로드 위치는 둘 다 nil(미상)이다 — 재스캔으로만 채워진다.
	PresentedChainLen *int      `json:"presented_chain_len"`
	ChainComplete     *bool     `json:"chain_complete"`
	Target            TargetRef `json:"target"`
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
	// Risk는 조회 시점에 계산한 위험 판정이다(저장하지 않는다).
	// 상세는 위치 배열을 가지고 있으므로 위치 축 판정도 실제 위치로 계산한다.
	Risk certrisk.Assessment
}

// certificateColumns는 scanCertificate와 순서가 일치해야 한다.
// sans는 text[] 제약(패키지 주석 참조) 때문에 chr(31) 연결 문자열로 내린다.
const certificateColumns = `
	id, fingerprint_sha256, serial_number, subject_dn, issuer_dn, issuer_organization,
	common_name,
	array_to_string(sans, chr(31)),
	not_before, not_after, public_key_algorithm, public_key_size, signature_algorithm,
	certificate_type, status, ca_id, owner_id, department_id, environment,
	auto_renew, renewal_policy_id, subject_key_id, authority_key_id,
	replaced_by_certificate_id, replaced_at, created_at, updated_at`

// certificateScanDest는 certificateColumns 순서와 1:1인 Scan 대상 목록이다.
// 컬럼을 늘리면 여기도 같은 자리에 늘린다.
func certificateScanDest(c *domain.Certificate, sans, ctype, status *string) []any {
	return []any{
		&c.ID, &c.FingerprintSHA256, &c.SerialNumber, &c.SubjectDN, &c.IssuerDN,
		&c.IssuerOrganization, &c.CommonName,
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

// scanCertificateListRow는 certificateColumns 뒤에 목록 전용 파생 열 3개
// (last_seen_at, 위치가 관측한 TLS 버전 목록, 체인 불완전 위치 유무)가 붙은 행을 읽는다.
func scanCertificateListRow(sc interface{ Scan(...any) error }, lastSeen **time.Time, tlsVersions *sql.NullString, chainIncomplete *sql.NullBool) (domain.Certificate, error) {
	var c domain.Certificate
	var sans, ctype, status string
	dest := append(certificateScanDest(&c, &sans, &ctype, &status), lastSeen, tlsVersions, chainIncomplete)
	if err := sc.Scan(dest...); err != nil {
		return c, err
	}
	finishCertificate(&c, sans, ctype, status)
	return c, nil
}

// listRiskLocations는 목록 조회가 상관 서브쿼리로 요약해 온 위치 정보를 위치 축
// 판정 입력으로 편다. 목록은 위치 배열을 싣지 않으므로 **판정에 필요한 사실만**
// 요약해서 받는다 — 요약 조건이 SQL 술어(riskLegacyTLSSQL·riskIncompleteChainSQL)와
// 어긋나면 "배지는 danger인데 필터로는 안 잡히는" 상태가 된다.
// 두 요약 모두 removed 위치를 이미 뺀 값이라 Removed는 false다.
func listRiskLocations(tlsVersions sql.NullString, chainIncomplete sql.NullBool) []certrisk.LocationInput {
	var out []certrisk.LocationInput
	if tlsVersions.Valid && tlsVersions.String != "" {
		for _, p := range strings.Split(tlsVersions.String, sansSep) {
			if p == "" {
				continue
			}
			out = append(out, certrisk.LocationInput{TLSVersion: p})
		}
	}
	// NULL이면 판정 대상 위치가 하나도 없거나 전부 미상이다 — 어느 쪽이든 판정하지 않는다.
	if chainIncomplete.Valid && chainIncomplete.Bool {
		incomplete := false
		out = append(out, certrisk.LocationInput{ChainComplete: &incomplete})
	}
	return out
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
	if in.IssuerOrg != nil {
		if *in.IssuerOrg == "" {
			// 빈 문자열은 "조직 미상" — 분포(certificates_issuers.go)의
			// NULL 묶음 행에서 클릭해 들어오는 경로다.
			conds = append(conds, "issuer_organization IS NULL")
		} else {
			conds = append(conds, fmt.Sprintf("issuer_organization = %s", arg(*in.IssuerOrg)))
		}
	}
	if in.IssuedBy != nil {
		// certificates_chain.go findIssuer 술어의 역방향(발급자 → 하위).
		// 자기 자신 제외: 자체 서명 루트는 AKI=자기 SKI라 스스로를 발급한 것으로 잡힌다.
		ph := arg(*in.IssuedBy)
		conds = append(conds, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM certificates ic
			 WHERE ic.id = %s::uuid AND ic.id <> certificates.id
			   AND (
			        (ic.subject_key_id IS NOT NULL AND certificates.authority_key_id = ic.subject_key_id)
			     OR (certificates.issuer_dn = ic.subject_dn
			         AND (certificates.authority_key_id IS NULL OR ic.subject_key_id IS NULL))
			   ))`, ph))
	}
	// 위험 판정 필터는 바인드 파라미터를 쓰지 않는다 — 값이 아니라 코드·심각도로만
	// 받고 술어는 certificates_risk.go의 고정 문자열이다(사용자 입력이 SQL 조각으로
	// 들어가지 않는다. 정렬 화이트리스트와 같은 규약).
	conds = append(conds, buildRiskConds(in)...)
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
	// last_seen_at·관측 TLS 버전·체인 불완전 여부는 상관 서브쿼리로 붙인다
	// (정렬·필터에는 쓰지 않는다). 뒤 둘은 위험 판정의 위치 축 입력이며 removed
	// 위치의 옛 관측은 제외한다 — Go 규칙(certrisk.legacyTLSRule·incompleteChainRule)과
	// SQL 술어(riskLegacyTLSSQL·riskIncompleteChainSQL)가 같은 기준이어야 한다.
	// chain_complete는 IS NOT NULL로 좁혀 "미상"이 false로 뭉개지지 않게 한다.
	query := fmt.Sprintf(`SELECT %s,
			(SELECT max(l.last_verified_at) FROM certificate_locations l
			  WHERE l.certificate_id = certificates.id),
			(SELECT array_to_string(array_agg(DISTINCT l.tls_version), chr(31))
			   FROM certificate_locations l
			  WHERE l.certificate_id = certificates.id
			    AND l.deployment_status <> 'removed'
			    AND l.tls_version IS NOT NULL),
			(SELECT bool_or(NOT l.chain_complete)
			   FROM certificate_locations l
			  WHERE l.certificate_id = certificates.id
			    AND l.deployment_status <> 'removed'
			    AND l.chain_complete IS NOT NULL)
		 FROM certificates%s ORDER BY %s %s, id ASC LIMIT $%d OFFSET $%d`,
		certificateColumns, where, col, dir, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list certificates: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	// 판정 기준 시각은 페이지 전체에 하나만 쓴다 — 같은 응답 안에서 행마다
	// 만료 판정 기준이 달라지지 않게 한다.
	now := time.Now()
	items := []CertificateListItem{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		var item CertificateListItem
		var tlsVersions sql.NullString
		var chainIncomplete sql.NullBool
		c, err := scanCertificateListRow(rows, &item.LastSeenAt, &tlsVersions, &chainIncomplete)
		if err != nil {
			return nil, fmt.Errorf("store: scan certificate: %w", err)
		}
		item.Certificate = c
		item.Risk = certrisk.Evaluate(certrisk.Input{
			Certificate: c,
			Locations:   listRiskLocations(tlsVersions, chainIncomplete),
			Now:         now,
		})
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

	// endpoint_monitors LEFT JOIN은 "이 위치의 엔드포인트에 지금 무엇이 붙어 있나"를
	// 붙인다 — 인증서 1행 : 위치 N행 구조에서 위치별 서비스 여부를 가르는 근거다.
	// 지문 유니크 인덱스를 타는 등가 조인이라 행이 늘지 않는다.
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.id, l.certificate_id, l.target_id, l.location_type, l.file_path,
			l.store_name, l.service_name, l.port, l.last_verified_at, l.deployment_status,
			l.tls_version, m.current_fingerprint, mc.id,
			l.presented_chain_len, l.chain_complete,
			t.id, t.name, t.target_type, t.hostname, host(t.ip_address)
		FROM certificate_locations l
		JOIN targets t ON t.id = l.target_id
		LEFT JOIN endpoint_monitors m ON m.target_id = l.target_id AND m.port = l.port
		LEFT JOIN certificates mc ON m.current_fingerprint IS NOT NULL
			AND mc.fingerprint_sha256 = m.current_fingerprint
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
			&l.TLSVersion, &l.CurrentFingerprint, &l.CurrentCertificateID,
			&l.PresentedChainLen, &l.ChainComplete,
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
	risk := certrisk.Evaluate(certrisk.Input{
		Certificate: cert,
		Locations:   locationRiskInputs(locations),
		Now:         time.Now(),
	})
	return &CertificateDetail{
		Certificate: cert, Owner: owner, Locations: locations, Chain: chain, Risk: risk,
	}, nil
}

// locationRiskInputs는 상세 조회의 설치 위치를 위치 축 판정 입력으로 옮긴다.
// deployment_status='removed'는 더 이상 인증서를 제공하지 않는 위치이므로
// 옛 관측으로 판정하지 않는다(목록 경로의 서브쿼리 조건과 같은 기준).
// ChainComplete는 nil을 그대로 넘긴다 — 미상은 판정 대상이 아니다 (0018).
func locationRiskInputs(locs []LocationWithTarget) []certrisk.LocationInput {
	out := make([]certrisk.LocationInput, 0, len(locs))
	for _, l := range locs {
		in := certrisk.LocationInput{
			Removed:       l.DeploymentStatus == domain.DeployRemoved,
			ChainComplete: l.ChainComplete,
		}
		if l.TLSVersion != nil {
			in.TLSVersion = *l.TLSVersion
		}
		out = append(out, in)
	}
	return out
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
