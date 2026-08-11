package store

// servers.go — 서버(엔드포인트) 축 조회. 목록은 호스트 1행, 상세에서 포트별 확장.
//
// 왜 필요한가: 같은 인증서가 여러 서버에 배포되어 있으면 그중 곧 만료인 서버도
// 있고 아닌 서버도 있다. 인증서 1행짜리 목록은 이 관계를 뭉갠다.
//
// 새 저장 컬럼·마이그레이션이 없다. 데이터 모델이 이미 인증서 1행 : 위치 N행이고
// (certificates 지문 유니크 / certificate_locations (certificate_id, target_id, port)
// / targets (executor_scope, ip) / endpoint_monitors (target_id, port)), 조인
// certificates.fingerprint_sha256 = endpoint_monitors.current_fingerprint는 기존
// 유니크 인덱스를 탄다. 전부 파생 집계다.
//
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음;
// summary·issuers와 같은 규약). 권한 검사는 라우트 등록에서 인증서 조회와 같다.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxServerPorts는 목록 1행에 실어 보내는 포트 미리보기 개수다.
// 전 엔드포인트를 목록에 싣지 않는다 — 확장은 GET /servers/{id} 지연 로드다
// (대시보드가 page_size=500을 보내 실서버에서 400이 난 전례와 같은 함정).
const maxServerPorts = 5

// serverSortColumns는 서버 목록 정렬 허용 컬럼 화이트리스트다 (키 = API sort 값).
// 값은 srv CTE의 출력 컬럼(또는 그 파생 표현)이며 사용자 입력이 SQL 조각으로
// 들어가지 않는다 (listSortColumns와 같은 규약).
var serverSortColumns = map[string]string{
	"risk":         "earliest_not_after", // 가장 임박한 만료 = 이 서버의 위험도
	"hostname":     "sort_hostname",      // 호스트명, 없으면 IP
	"endpoints":    "endpoint_count",
	"certificates": "certificate_count",
	"last_seen":    "last_seen_at",
}

// IsServerSortField는 name이 서버 목록 정렬 허용 필드인지 반환한다.
func IsServerSortField(name string) bool {
	_, ok := serverSortColumns[name]
	return ok
}

// ListServersInput은 서버 목록 조회 조건이다.
//
// 인증서 목록(ListCertificatesInput)과 필터를 공유하지 않는다. 여기의 만료 필터는
// EXISTS 의미("이 서버에 그 구간 인증서가 하나라도 있다")라서 type·self_signed·
// issuer_org 같은 인증서 속성 필터를 그대로 옮기면 의미가 조용히 달라진다.
type ListServersInput struct {
	// Search는 호스트명·IP·현재 인증서 CN 부분 일치다 (대소문자 무시).
	Search string
	// Expired는 EXISTS 의미다: true면 이미 만료된 인증서가 하나라도 붙어 있는 서버,
	// false면 하나도 없는 서버.
	Expired *bool
	// ExpiresWithinDays도 EXISTS 의미다: 오늘부터 N일 이내 만료가 하나라도 있는 서버
	// (이미 만료된 것은 제외 — 인증서 목록 필터와 같은 규약).
	ExpiresWithinDays *int
	// Scope는 targets.executor_scope 정확 일치다 ('server' 또는 'sensor:<uuid>').
	Scope string
	// Mismatch는 구버전 잔존 필터다: true면 현재 인증서와 다른 위치 행이
	// 남아 있는 서버만(교체 후 옛 인증서가 아직 어딘가 붙어 있다).
	Mismatch *bool
	// IssuedBy는 "이 CA 계열 인증서를 쓰는 서버" 필터다 — 값은 CA 인증서 id.
	// **전이적**이다(자신 + 모든 하위 CA가 발급한 것까지) — 직계 1단계만 보는
	// 인증서 목록의 IssuedBy와 깊이가 다르다. "이 CA가 만료되면 어느 서버가
	// 죽나"가 이 필터의 용도이고, 같은 술어를 CertificateImpact가 공유하므로
	// 여기의 total이 곧 impact 응답의 server_count다 (certificates_impact.go).
	IssuedBy  string
	SortField string // serverSortColumns 키. 빈 값이면 risk
	SortDesc  bool
	Page      int // 1부터
	PageSize  int // 1..maxPageSize
}

// ServerListItem은 서버 목록 1행 = 호스트 1행이다.
type ServerListItem struct {
	ID        string  `json:"id"`
	Hostname  *string `json:"hostname"`
	IPAddress *string `json:"ip_address"`
	// Scope는 targets.executor_scope다 — 다중 세그먼트에서 사설 IP가 겹칠 수 있어
	// 호스트 동일성의 일부다 (0010).
	Scope string `json:"scope"`
	// SensorName은 Scope가 'sensor:<uuid>'일 때 그 센서 이름이다(없으면 nil).
	SensorName       *string `json:"sensor_name"`
	EndpointCount    int     `json:"endpoint_count"`
	CertificateCount int     `json:"certificate_count"`
	// EarliestNotAfter는 이 서버에서 지금 서비스 중인 인증서들의 min(not_after)다.
	// 붙어 있는 인증서를 하나도 특정하지 못하면 nil이다.
	EarliestNotAfter *time.Time `json:"earliest_not_after"`
	// LastSeenAt은 이 서버 엔드포인트들의 max(last_success_at)다.
	LastSeenAt *time.Time `json:"last_seen_at"`
	// StaleCount는 현재 인증서와 다른 위치 행 수다(구버전 잔존).
	StaleCount int `json:"stale_count"`
	// EstimatedCount는 현재 인증서를 추정으로 채운 엔드포인트 수다
	// (endpoint_monitors.current_fingerprint가 NULL — 재스캔 OFF이거나 미실행).
	// 화면은 이 값이 0보다 크면 '추정' 배지를 붙인다.
	EstimatedCount int `json:"estimated_count"`
	// Ports는 오름차순 포트 미리보기다(최대 maxServerPorts개).
	// 전체 개수는 EndpointCount이고 전체 목록은 상세 조회에 있다.
	Ports []int `json:"ports"`
}

// ServerList는 서버 목록 조회 결과다.
type ServerList struct {
	Items    []ServerListItem
	Total    int
	Page     int
	PageSize int
}

// ServerCertificateRef는 엔드포인트에 붙은 인증서 요약이다(원문·개인키 없음).
type ServerCertificateRef struct {
	ID                string    `json:"id"`
	CommonName        string    `json:"common_name"`
	NotAfter          time.Time `json:"not_after"`
	Status            string    `json:"status"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
}

// ServerStaleLocation은 현재 인증서와 다른, 아직 제거되지 않은 위치 행이다.
type ServerStaleLocation struct {
	LocationID       string               `json:"location_id"`
	Certificate      ServerCertificateRef `json:"certificate"`
	LastVerifiedAt   *time.Time           `json:"last_verified_at"`
	DeploymentStatus string               `json:"deployment_status"`
	TLSVersion       *string              `json:"tls_version"`
}

// ServerEndpoint는 서버 상세의 포트 1개다.
type ServerEndpoint struct {
	Port int `json:"port"`
	// Certificate는 지금 이 포트에 붙어 있다고 판정한 인증서다(못 정하면 nil).
	Certificate *ServerCertificateRef `json:"certificate"`
	// CertificateEstimated가 true면 Certificate는 관측 지문이 아니라
	// "가장 최근 확인된 live 위치"로 **추정**한 값이다. 화면은 반드시 구분해 표시한다.
	CertificateEstimated bool       `json:"certificate_estimated"`
	TLSVersion           *string    `json:"tls_version"`
	LastSuccessAt        *time.Time `json:"last_success_at"`
	LastProbeAt          *time.Time `json:"last_probe_at"`
	ConsecutiveMisses    int        `json:"consecutive_misses"`
	RetiredAt            *time.Time `json:"retired_at"`
	// StaleLocations는 이 포트에 남아 있는 구버전 인증서 위치다.
	StaleLocations []ServerStaleLocation `json:"stale_locations"`
}

// ServerDetail은 서버 상세 조회 결과다.
type ServerDetail struct {
	Server    ServerListItem
	Endpoints []ServerEndpoint
}

// serverEndpointCTE는 "이 서버의 엔드포인트와 지금 붙어 있는 인증서"를 만든다.
//
// 엔드포인트 집합은 두 출처의 합집합이다 — 제거되지 않은 network_endpoint 위치와
// 은퇴하지 않은 재스캔 모니터. 은퇴한 모니터도 살아 있는 위치가 남아 있으면
// 위치 쪽 가지로 들어오고 retired_at이 채워진 채 보인다(은퇴 사실을 숨기지 않되
// 흔적이 아무것도 없는 엔드포인트는 목록에서 사라진다).
//
// 현재 인증서 판정은 2단이다:
//  1. endpoint_monitors.current_fingerprint가 있으면 그 지문의 인증서 = 관측 확정.
//  2. 없으면(재스캔 OFF이거나 아직 미실행) 가장 최근 확인된 live 위치로 **추정**하고
//     estimated=true를 실어 보낸다. 프론트가 '추정' 배지를 붙이는 근거다.
//
// where는 "" 또는 " AND target_id = $1::uuid"다 — 상세 조회에서 술어를 UNION 두 가지
// 모두에 밀어 넣어 전체 스캔을 피한다.
func serverEndpointCTE(where string) string {
	return `WITH ep AS (
		SELECT target_id, port
		  FROM certificate_locations
		 WHERE location_type = 'network_endpoint' AND port IS NOT NULL
		   AND deployment_status <> 'removed'` + where + `
		UNION
		SELECT target_id, port
		  FROM endpoint_monitors
		 WHERE retired_at IS NULL` + where + `
	), cur AS (
		SELECT ep.target_id, ep.port,
		       m.last_success_at, m.last_probe_at,
		       coalesce(m.consecutive_misses, 0) AS consecutive_misses,
		       m.retired_at,
		       mc.id AS confirmed_id,
		       (SELECT l.certificate_id
		          FROM certificate_locations l
		         WHERE l.target_id = ep.target_id AND l.port = ep.port
		           AND l.location_type = 'network_endpoint'
		           AND l.deployment_status <> 'removed'
		         ORDER BY l.last_verified_at DESC NULLS LAST, l.id DESC
		         LIMIT 1) AS estimated_id
		  FROM ep
		  LEFT JOIN endpoint_monitors m
		    ON m.target_id = ep.target_id AND m.port = ep.port
		  LEFT JOIN certificates mc
		    ON m.current_fingerprint IS NOT NULL
		   AND mc.fingerprint_sha256 = m.current_fingerprint
	), endpoint AS (
		SELECT target_id, port, last_success_at, last_probe_at, consecutive_misses, retired_at,
		       coalesce(confirmed_id, estimated_id) AS certificate_id,
		       (confirmed_id IS NULL AND estimated_id IS NOT NULL) AS estimated
		  FROM cur
	)`
}

// serverEndpointCTEWithFamily는 endpoint CTE 앞에 "CA 계열" CTE를 얹는다
// (certificates_impact.go의 fam·fam_live — $1::uuid를 쓴다). family가 false면
// 기존 WITH 절 그대로다. 재귀 CTE가 하나라도 있으면 WITH RECURSIVE로 올려야
// 하므로 머리만 갈아 끼운다.
func serverEndpointCTEWithFamily(family bool, where string) string {
	if !family {
		return serverEndpointCTE(where)
	}
	return "WITH RECURSIVE " + certFamilyCTEs + ", " +
		strings.TrimPrefix(serverEndpointCTE(where), "WITH ")
}

// serverBaseCTE는 목록·상세의 서버 1행 집계다. count 질의와 페이지 질의가
// **같은 CTE 문자열을 공유**한다 — total과 페이지가 다른 그룹핑 위에서 세지 않게.
var serverBaseCTE = fmt.Sprintf(serverBaseCTEFmt, maxServerPorts)

const serverBaseCTEFmt = `, stale AS (
		SELECT e.target_id, count(*) AS stale_count
		  FROM endpoint e
		  JOIN certificate_locations l
		    ON l.target_id = e.target_id AND l.port = e.port
		   AND l.location_type = 'network_endpoint'
		   AND l.deployment_status <> 'removed'
		 WHERE e.certificate_id IS NOT NULL AND l.certificate_id <> e.certificate_id
		 GROUP BY e.target_id
	), agg AS (
		SELECT e.target_id,
		       count(*) AS endpoint_count,
		       count(DISTINCT e.certificate_id) AS certificate_count,
		       count(*) FILTER (WHERE e.estimated) AS estimated_count,
		       min(c.not_after) AS earliest_not_after,
		       min(c.not_after) FILTER (WHERE c.not_after >= now()) AS earliest_future_not_after,
		       coalesce(bool_or(c.not_after < now()), false) AS has_expired,
		       max(e.last_success_at) AS last_seen_at,
		       array_to_string((array_agg(e.port ORDER BY e.port))[1:%d], ',') AS ports,
		       coalesce(string_agg(c.common_name, chr(31)), '') AS cn_blob
		  FROM endpoint e
		  LEFT JOIN certificates c ON c.id = e.certificate_id
		 GROUP BY e.target_id
	), srv AS (
		SELECT t.id::text AS id, t.hostname, host(t.ip_address) AS ip_address,
		       t.executor_scope AS scope, sn.name AS sensor_name,
		       a.endpoint_count, a.certificate_count, a.estimated_count,
		       a.earliest_not_after, a.earliest_future_not_after, a.has_expired,
		       a.last_seen_at, coalesce(s.stale_count, 0) AS stale_count,
		       coalesce(a.ports, '') AS ports, a.cn_blob,
		       coalesce(t.hostname, host(t.ip_address)) AS sort_hostname
		  FROM agg a
		  JOIN targets t ON t.id = a.target_id
		  LEFT JOIN sensors sn ON t.executor_scope = 'sensor:' || sn.id::text
		  LEFT JOIN stale s ON s.target_id = a.target_id
	)`

// serverListColumns는 ServerListItem 스캔 순서와 1:1이다.
const serverListColumns = `id, hostname, ip_address, scope, sensor_name,
	endpoint_count, certificate_count, estimated_count,
	earliest_not_after, last_seen_at, stale_count, ports`

// buildServerWhere는 srv CTE 위의 WHERE 조건과 바인드 파라미터를 만든다.
// args는 이미 채워진 채로 들어올 수 있다 — IssuedBy를 쓰면 계열 CTE가 $1을
// 차지하므로 호출자가 그것을 먼저 넣고 여기서 $2부터 이어 붙인다.
func buildServerWhere(in ListServersInput, args []any) (conds []string, _ []any) {
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if in.Search != "" {
		ph := arg("%" + escapeLike(in.Search) + "%")
		conds = append(conds, fmt.Sprintf(
			`(coalesce(hostname, '') ILIKE %[1]s ESCAPE '\'
			  OR coalesce(ip_address, '') ILIKE %[1]s ESCAPE '\'
			  OR cn_blob ILIKE %[1]s ESCAPE '\')`, ph))
	}
	if in.Expired != nil {
		// EXISTS 의미 — 이 서버에 이미 만료된 인증서가 하나라도 있는가.
		if *in.Expired {
			conds = append(conds, "has_expired")
		} else {
			conds = append(conds, "NOT has_expired")
		}
	}
	if in.ExpiresWithinDays != nil {
		// "미만료 인증서 중 가장 임박한 것이 N일 안"이 곧 EXISTS다.
		// 파라미터는 사용 지점에서 캐스트한다(COALESCE·표현식 안의 무캐스트 $n은
		// text로 추론되어 런타임에만 42804로 터진다).
		ph := arg(*in.ExpiresWithinDays)
		conds = append(conds, fmt.Sprintf(
			"earliest_future_not_after IS NOT NULL AND earliest_future_not_after < now() + %s::int * interval '1 day'", ph))
	}
	if in.Scope != "" {
		conds = append(conds, fmt.Sprintf("scope = %s", arg(in.Scope)))
	}
	if in.Mismatch != nil {
		if *in.Mismatch {
			conds = append(conds, "stale_count > 0")
		} else {
			conds = append(conds, "stale_count = 0")
		}
	}
	if in.IssuedBy != "" {
		// 계열 판정은 SQL 술어여야 한다 — Go에서 걸러 내면 total·page가 필터를
		// 반영하지 못한다(위험 필터를 SQL로 옮긴 것과 같은 이유). fam_live는
		// 호출자가 얹은 CTE이고 바인드 파라미터는 그쪽($1)이 갖는다.
		conds = append(conds, `EXISTS (
			SELECT 1 FROM endpoint e2
			 WHERE e2.target_id = srv.id::uuid
			   AND e2.certificate_id IN (SELECT id FROM fam_live))`)
	}
	return conds, args
}

// parsePortList는 array_to_string(',')로 내려온 포트 목록을 되돌린다
// (int[]는 database/sql이 직접 스캔하지 못한다 — 패키지 규약).
func parsePortList(s string) []int {
	if s == "" {
		return []int{}
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func scanServerListItem(sc interface{ Scan(...any) error }) (ServerListItem, error) {
	var it ServerListItem
	var ports string
	if err := sc.Scan(&it.ID, &it.Hostname, &it.IPAddress, &it.Scope, &it.SensorName,
		&it.EndpointCount, &it.CertificateCount, &it.EstimatedCount,
		&it.EarliestNotAfter, &it.LastSeenAt, &it.StaleCount, &ports); err != nil {
		return it, err
	}
	it.Ports = parsePortList(ports)
	return it, nil
}

// ListServers는 서버(호스트) 축 목록을 조회한다.
// count와 페이지가 같은 CTE를 공유하므로 total은 언제나 같은 그룹핑 위의 수다.
func (s *Store) ListServers(ctx context.Context, in ListServersInput) (*ServerList, error) {
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
		sortKey = "risk"
	}
	col, ok := serverSortColumns[sortKey]
	if !ok {
		return nil, fmt.Errorf("store: sort field %q not allowed", in.SortField)
	}
	dir := "ASC"
	if in.SortDesc {
		dir = "DESC"
	}

	// 계열 필터를 쓰면 재귀 CTE가 $1을 차지하므로 대상 CA id를 먼저 넣는다.
	var args []any
	if in.IssuedBy != "" {
		if !uuidRe.MatchString(strings.ToLower(strings.TrimSpace(in.IssuedBy))) {
			return nil, fmt.Errorf("store: issued_by %q is not a certificate id", in.IssuedBy)
		}
		args = append(args, strings.ToLower(strings.TrimSpace(in.IssuedBy)))
	}
	conds, args := buildServerWhere(in, args)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	base := serverEndpointCTEWithFamily(in.IssuedBy != "", "") + serverBaseCTE

	var total int
	if err := s.db.QueryRowContext(ctx,
		base+" SELECT count(*) FROM srv"+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("store: count servers: %w", err)
	}

	// NULLS LAST는 방향과 무관하게 고정한다 — "만료 시각을 모르는 서버"는
	// 위험 순 정렬의 맨 앞에 오면 안 된다. id 보조 정렬로 페이지 경계를 안정화한다.
	query := fmt.Sprintf("%s SELECT %s FROM srv%s ORDER BY %s %s NULLS LAST, id ASC LIMIT $%d OFFSET $%d",
		base, serverListColumns, where, col, dir, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list servers: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []ServerListItem{} // 빈 결과도 JSON []로 나가도록
	for rows.Next() {
		it, err := scanServerListItem(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan server: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate servers: %w", err)
	}
	return &ServerList{Items: items, Total: total, Page: page, PageSize: size}, nil
}

// GetServerDetail은 서버 1건과 포트별 엔드포인트 목록을 조회한다.
// 잘못된 형식의 id는 존재하지 않는 리소스로 취급한다 (uuid 캐스트 오류 방지).
func (s *Store) GetServerDetail(ctx context.Context, id string) (*ServerDetail, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}

	filter := " AND target_id = $1::uuid"
	row := s.db.QueryRowContext(ctx,
		serverEndpointCTE(filter)+serverBaseCTE+" SELECT "+serverListColumns+" FROM srv", id)
	server, err := scanServerListItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		// 엔드포인트가 하나도 없는 대상은 집계에 잡히지 않는다 — 대상 자체가
		// 있으면 0건 서버로, 없으면 404로 가른다.
		server, err = s.emptyServerRow(ctx, id)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("store: get server: %w", err)
	}

	endpoints, err := s.listServerEndpoints(ctx, id, filter)
	if err != nil {
		return nil, err
	}
	return &ServerDetail{Server: server, Endpoints: endpoints}, nil
}

// emptyServerRow는 엔드포인트가 없는 대상의 서버 행을 만든다(집계는 전부 0).
func (s *Store) emptyServerRow(ctx context.Context, id string) (ServerListItem, error) {
	var it ServerListItem
	err := s.db.QueryRowContext(ctx, `
		SELECT t.id::text, t.hostname, host(t.ip_address), t.executor_scope, sn.name
		  FROM targets t
		  LEFT JOIN sensors sn ON t.executor_scope = 'sensor:' || sn.id::text
		 WHERE t.id = $1::uuid`, id,
	).Scan(&it.ID, &it.Hostname, &it.IPAddress, &it.Scope, &it.SensorName)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	if err != nil {
		return it, err
	}
	it.Ports = []int{}
	return it, nil
}

// listServerEndpoints는 서버 1건의 포트별 엔드포인트를 조회한다(구버전 잔존 포함).
func (s *Store) listServerEndpoints(ctx context.Context, id, filter string) ([]ServerEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, serverEndpointCTE(filter)+`
		SELECT e.port, e.estimated, e.last_success_at, e.last_probe_at,
		       e.consecutive_misses, e.retired_at,
		       c.id::text, c.common_name, c.not_after, c.status, c.fingerprint_sha256,
		       (SELECT l.tls_version
		          FROM certificate_locations l
		         WHERE l.target_id = e.target_id AND l.port = e.port
		           AND l.certificate_id = e.certificate_id
		         ORDER BY l.last_verified_at DESC NULLS LAST, l.id DESC
		         LIMIT 1)
		  FROM endpoint e
		  LEFT JOIN certificates c ON c.id = e.certificate_id
		 ORDER BY e.port`, id)
	if err != nil {
		return nil, fmt.Errorf("store: list server endpoints: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	endpoints := []ServerEndpoint{}
	idx := map[int]int{}
	for rows.Next() {
		var ep ServerEndpoint
		var certID, cn, status, fp *string
		var notAfter *time.Time
		if err := rows.Scan(&ep.Port, &ep.CertificateEstimated, &ep.LastSuccessAt, &ep.LastProbeAt,
			&ep.ConsecutiveMisses, &ep.RetiredAt,
			&certID, &cn, &notAfter, &status, &fp, &ep.TLSVersion); err != nil {
			return nil, fmt.Errorf("store: scan server endpoint: %w", err)
		}
		if certID != nil && notAfter != nil {
			ep.Certificate = &ServerCertificateRef{
				ID: *certID, CommonName: derefString(cn), NotAfter: *notAfter,
				Status: derefString(status), FingerprintSHA256: derefString(fp),
			}
		}
		ep.StaleLocations = []ServerStaleLocation{}
		idx[ep.Port] = len(endpoints)
		endpoints = append(endpoints, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate server endpoints: %w", err)
	}

	stale, err := s.listServerStaleLocations(ctx, id, filter)
	if err != nil {
		return nil, err
	}
	for port, locs := range stale {
		if i, ok := idx[port]; ok {
			endpoints[i].StaleLocations = locs
		}
	}
	return endpoints, nil
}

// listServerStaleLocations는 포트별 구버전 잔존 위치를 조회한다.
func (s *Store) listServerStaleLocations(ctx context.Context, id, filter string) (map[int][]ServerStaleLocation, error) {
	rows, err := s.db.QueryContext(ctx, serverEndpointCTE(filter)+`
		SELECT e.port, l.id::text, l.last_verified_at, l.deployment_status, l.tls_version,
		       c.id::text, c.common_name, c.not_after, c.status, c.fingerprint_sha256
		  FROM endpoint e
		  JOIN certificate_locations l
		    ON l.target_id = e.target_id AND l.port = e.port
		   AND l.location_type = 'network_endpoint'
		   AND l.deployment_status <> 'removed'
		  JOIN certificates c ON c.id = l.certificate_id
		 WHERE e.certificate_id IS NOT NULL AND l.certificate_id <> e.certificate_id
		 ORDER BY e.port, c.not_after ASC, l.id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("store: list server stale locations: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	out := map[int][]ServerStaleLocation{}
	for rows.Next() {
		var port int
		var sl ServerStaleLocation
		if err := rows.Scan(&port, &sl.LocationID, &sl.LastVerifiedAt, &sl.DeploymentStatus, &sl.TLSVersion,
			&sl.Certificate.ID, &sl.Certificate.CommonName, &sl.Certificate.NotAfter,
			&sl.Certificate.Status, &sl.Certificate.FingerprintSHA256); err != nil {
			return nil, fmt.Errorf("store: scan server stale location: %w", err)
		}
		out[port] = append(out[port], sl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate server stale locations: %w", err)
	}
	return out, nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
