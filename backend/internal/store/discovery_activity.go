package store

// 통합 작업 목록 — 스캔 Job·스윕·정기 스캔 실행을 한 줄기로 합쳐 조회한다.
//
// 화면이 세 목록을 따로 폴링하면 "지금 무엇이 돌고 있나"를 사람이 머릿속에서
// 합쳐야 한다. 여기서 UNION으로 합치되, **자동 발주의 개별 Job은 싣지 않는다**:
//   - origin='rescan'  : 주기 재스캔이 발주한 Job (수십 건이 목록을 도배한다)
//   - origin='sweep'   : 스윕 자식 청크 (부모 스윕 1줄이 대표한다)
//   - schedule_id 있는 스윕 : 정기 스캔 실행 1줄이 대표한다
//
// 취소 가능 여부는 **서버가 판정해 cancel로 내려준다**. 클라이언트가 상태
// 문자열로 추론하면 상태 머신이 바뀔 때마다 조용히 어긋난다 — 판정은
// CancelScanJob·sweepTransitions·실행 행 상태와 같은 규칙을 SQL로 옮긴 것이다.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 작업 축 3종. 취소 API 경로가 축마다 다르므로 화면은 이 값으로 분기한다.
const (
	// ActivityKindScan은 사람이 발주한 단발 스캔 Job이다 (origin='manual').
	ActivityKindScan = "scan"
	// ActivityKindSweep은 스케줄이 발주하지 않은 전 포트 스윕이다.
	ActivityKindSweep = "sweep"
	// ActivityKindScheduleRun은 정기 스캔 실행 1건이다(발주한 스윕을 품는다).
	ActivityKindScheduleRun = "schedule_run"
)

// ActivityKinds는 허용되는 kind 값 전체다(API 계층의 입력 검증용).
func ActivityKinds() []string {
	return []string{ActivityKindScan, ActivityKindSweep, ActivityKindScheduleRun}
}

// ActivityItem은 통합 목록의 1행이다. 축마다 다른 필드는 nil로 비운다.
type ActivityItem struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Status string `json:"status"`
	// Active는 "아직 끝나지 않았다"다(축별 종료 상태의 여집합).
	Active bool `json:"active"`
	// Cancel은 지금 이 항목을 취소할 수 있는지의 **서버 판정**이다.
	Cancel bool `json:"cancel"`
	// Name은 스윕 이름 또는 정기 스캔 이름이다. 단발 스캔은 null.
	Name *string `json:"name"`
	// Origin은 스캔의 발주 출처(manual) 또는 실행의 발동 주체(schedule|manual)다.
	Origin *string `json:"origin"`
	// ExecutorScope는 'server' 또는 'sensor:<uuid>'다 (0010 규약).
	ExecutorScope string   `json:"executor_scope"`
	Targets       []string `json:"targets"`
	// PortSpec은 입력 원문이다. 단발 스캔만 전개된 포트 목록("443,8443")이다.
	PortSpec           string     `json:"port_spec"`
	TotalEndpoints     int64      `json:"total_endpoints"`
	ScannedEndpoints   int64      `json:"scanned_endpoints"`
	ReachableEndpoints int64      `json:"reachable_endpoints"`
	CertificatesFound  int64      `json:"certificates_found"`
	CertificatesNew    int64      `json:"certificates_new"`
	Error              *string    `json:"error"`
	ScheduleID         *string    `json:"schedule_id"`
	SweepID            *string    `json:"sweep_id"`
	StartedAt          time.Time  `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}

// ListActivityInput은 통합 목록 조회 조건이다.
type ListActivityInput struct {
	Kinds    []string // 비면 전체 (값 검증은 API 계층)
	Active   *bool    // nil이면 전체
	Page     int      // 1부터
	PageSize int      // 1..maxPageSize
}

// ActivityList는 통합 목록 조회 결과다.
type ActivityList struct {
	Items    []ActivityItem
	Total    int
	Page     int
	PageSize int
}

// activityUnionSQL은 세 축을 같은 열 모양으로 맞춘 UNION이다.
// 열 이름은 첫 분기가 정하며, 나머지 분기는 순서를 반드시 지켜야 한다.
// 정수 폭이 축마다 다르므로(scan_jobs는 int, scan_sweeps는 bigint) 전부
// ::bigint로 못박는다 — UNION의 암묵 승격에 기대지 않는다.
//
// 진행률은 스윕 상세와 **같은 파생 집계**(sweepProgressJoin)를 쓴다. 두 화면이
// 다른 수치를 보이면 어느 쪽이 맞는지 알 수 없다.
const activityUnionSQL = `
	SELECT 'scan' AS kind, j.id::text AS id, j.status AS status,
	       (j.status IN ('pending', 'leased', 'running')) AS active,
	       -- CancelScanJob과 같은 판정: pending은 실행기 무관, running은 서버 실행만.
	       (j.status = 'pending'
	        OR (j.status = 'running' AND j.executor_type = 'server')) AS cancel,
	       NULL::text AS name, j.origin AS origin,
	       CASE WHEN j.sensor_id IS NOT NULL THEN 'sensor:' || j.sensor_id::text
	            ELSE j.executor_type END AS executor_scope,
	       array_to_string(j.targets, chr(31)) AS targets,
	       array_to_string(j.ports, ',') AS port_spec,
	       j.total_endpoints::bigint AS total_endpoints,
	       j.scanned_endpoints::bigint AS scanned_endpoints,
	       j.reachable_endpoints::bigint AS reachable_endpoints,
	       j.certificates_found::bigint AS certificates_found,
	       j.certificates_new::bigint AS certificates_new,
	       j.error AS error, NULL::text AS schedule_id, NULL::text AS sweep_id,
	       j.created_at AS started_at, j.finished_at AS finished_at
	  FROM scan_jobs j
	 WHERE j.origin = 'manual'
	UNION ALL
	SELECT 'sweep', sw.id::text, sw.status,
	       (sw.status IN ('queued', 'running', 'paused')),
	       -- sweepTransitions['cancel']의 허용 출발 상태와 같다.
	       (sw.status IN ('queued', 'running', 'paused')),
	       sw.name, NULL::text,
	       sw.executor_scope,
	       array_to_string(sw.targets, chr(31)),
	       coalesce(sw.port_spec, ''),
	       sw.total_endpoints::bigint,
	       coalesce(p.scanned, 0)::bigint, coalesce(p.reachable, 0)::bigint,
	       coalesce(p.found, 0)::bigint, coalesce(p.new_certs, 0)::bigint,
	       sw.last_error, NULL::text, NULL::text,
	       sw.created_at, sw.finished_at
	  FROM scan_sweeps sw` + sweepProgressJoin + `
	 WHERE sw.schedule_id IS NULL
	UNION ALL
	SELECT 'schedule_run', r.id::text, r.status,
	       (r.status = 'running'),
	       (r.status = 'running'),
	       sc.name, r.trigger,
	       sc.executor_scope,
	       array_to_string(sc.targets, chr(31)),
	       coalesce(sw.port_spec, sc.port_spec, ''),
	       -- 스윕 발주 전에는 실행 행의 누적치가 유일하게 아는 규모다.
	       coalesce(sw.total_endpoints, r.dispatched_endpoints)::bigint,
	       coalesce(p.scanned, 0)::bigint, coalesce(p.reachable, 0)::bigint,
	       coalesce(p.found, 0)::bigint, coalesce(p.new_certs, 0)::bigint,
	       r.error, r.schedule_id::text, r.sweep_id::text,
	       r.started_at, r.finished_at
	  FROM scan_schedule_runs r
	  JOIN scan_schedules sc ON sc.id = r.schedule_id
	  LEFT JOIN scan_sweeps sw ON sw.id = r.sweep_id` + sweepProgressJoin

// ListDiscoveryActivity는 스캔·스윕·정기 스캔 실행을 최신순으로 합쳐 조회한다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) ListDiscoveryActivity(ctx context.Context, in ListActivityInput) (*ActivityList, error) {
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

	var (
		conds []string
		args  []any
	)
	if len(in.Kinds) > 0 {
		args = append(args, strings.Join(in.Kinds, sansSep))
		conds = append(conds, fmt.Sprintf("a.kind = ANY(string_to_array($%d::text, chr(31)))", len(args)))
	}
	if in.Active != nil {
		args = append(args, *in.Active)
		conds = append(conds, fmt.Sprintf("a.active = $%d::boolean", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// count와 페이지가 **같은 UNION**을 공유해야 total과 목록이 어긋나지 않는다.
	base := "WITH activity AS (" + activityUnionSQL + ") "

	var total int
	if err := s.db.QueryRowContext(ctx,
		base+"SELECT count(*) FROM activity a"+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("store: count discovery activity: %w", err)
	}

	// id 보조 정렬로 페이지 경계를 안정화한다 (다른 목록과 같은 규약).
	query := fmt.Sprintf(
		base+"SELECT a.kind, a.id, a.status, a.active, a.cancel, a.name, a.origin,"+
			" a.executor_scope, a.targets, a.port_spec, a.total_endpoints,"+
			" a.scanned_endpoints, a.reachable_endpoints, a.certificates_found,"+
			" a.certificates_new, a.error, a.schedule_id, a.sweep_id,"+
			" a.started_at, a.finished_at"+
			" FROM activity a%s ORDER BY a.started_at DESC, a.id ASC LIMIT $%d OFFSET $%d",
		where, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list discovery activity: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []ActivityItem{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		var (
			it         ActivityItem
			targetsStr string
		)
		if err := rows.Scan(&it.Kind, &it.ID, &it.Status, &it.Active, &it.Cancel,
			&it.Name, &it.Origin, &it.ExecutorScope, &targetsStr, &it.PortSpec,
			&it.TotalEndpoints, &it.ScannedEndpoints, &it.ReachableEndpoints,
			&it.CertificatesFound, &it.CertificatesNew, &it.Error,
			&it.ScheduleID, &it.SweepID, &it.StartedAt, &it.FinishedAt); err != nil {
			return nil, fmt.Errorf("store: scan discovery activity: %w", err)
		}
		it.Targets = splitNonEmpty(targetsStr, sansSep)
		if it.Targets == nil {
			it.Targets = []string{}
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate discovery activity: %w", err)
	}
	return &ActivityList{Items: items, Total: total, Page: page, PageSize: size}, nil
}
