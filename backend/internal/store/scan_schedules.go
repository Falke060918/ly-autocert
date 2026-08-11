// scan_schedules.go — 정기 스캔 3종(0019 scan_schedules)의 영속화.
//
// 정기 스캔은 **새 실행 엔진이 아니라 발주 트리거**다:
//   - full·preset  → 주기가 오면 실행 1건(run)을 열고 호출자가 기존 스윕을 1건 발주한다.
//   - registered   → 기존 재스캔 엔진(endpoint_monitors·ClaimDueEndpointMonitors)이
//     그대로 돈다. 이 테이블의 registered 행은 그 엔진의 **설정 행**이며
//     클레임 대상이 아니다(만료 임박 가속 티어는 재스캔 쪽 그대로 유지된다).
//
// 겹침은 구조적으로 불가능해야 한다: 실행 1건은 scan_schedule_runs 1행이고
// 부분 유니크 인덱스 uq_scan_schedule_runs_active가 스케줄당 running을 1건으로
// 묶는다. 클레임의 NOT EXISTS는 그 앞단 판정일 뿐 최종 방어는 인덱스다.
//
// 이 파일은 discovery 패키지를 import하지 않는다(의존 방향 discovery → store).
// 그래서 port_spec 검증은 호출자가 discovery.ParsePortSpec을 감싸 주입한다
// (ScanScheduleInput.ValidatePortSpec — sweeps.go의 SweepChunkFunc 주입과 같은 규약).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// EventDiscoverySchedule은 정기 스캔 설정 변경·실행 감사 이벤트 타입이다.
// 발주된 스윕·Job의 감사는 기존 discovery.sweep / discovery.scan을 그대로 쓴다.
const EventDiscoverySchedule = "discovery.schedule"

// 정기 스캔 종류(0019 kind CHECK와 같은 값).
const (
	// ScheduleKindFull은 지정 대역의 전 포트를 도는 전체 정기 스캔이다(1행 유일).
	ScheduleKindFull = "full"
	// ScheduleKindPreset은 사전 등록한 대역·포트의 정기 스캔이다(N행).
	ScheduleKindPreset = "preset"
	// ScheduleKindRegistered는 이미 등록된 엔드포인트만 다시 보는 스캔이다(1행 유일).
	ScheduleKindRegistered = "registered"
)

// 실행 상태·발동 주체(0019 CHECK와 같은 값).
const (
	ScheduleRunRunning   = "running"
	ScheduleRunCompleted = "completed"
	ScheduleRunFailed    = "failed"
	ScheduleRunCanceled  = "canceled"
)

const (
	// ScheduleTriggerSchedule은 주기 도래로 열린 실행이다.
	ScheduleTriggerSchedule = "schedule"
	// ScheduleTriggerManual은 사용자의 '지금 실행'이다. next_run_at을 건드리지 않는다.
	ScheduleTriggerManual = "manual"
)

// MinScheduleIntervalSeconds는 full·preset 주기의 하한(1시간)이다.
// 더 짧은 주기는 스윕이 앞 실행을 계속 따라잡지 못해 매 주기를 건너뛰기만 한다.
// registered는 재스캔 설정(RESCAN_INTERVAL)의 지배를 받으므로 이 하한을 적용하지 않는다.
const MinScheduleIntervalSeconds = 3600

// ErrScanScheduleNotFound는 존재하지 않는 스케줄 조회·변경 시 반환한다.
var ErrScanScheduleNotFound = errors.New("store: scan schedule not found")

// ErrScanScheduleKind는 preset 전용 연산(생성·삭제)을 full·registered에 요청했을 때다.
// full·registered는 시드 1행이 정본이라 만들거나 지울 수 없고 설정만 바꾼다.
var ErrScanScheduleKind = errors.New("store: scan schedule kind does not allow this operation")

// ErrScanScheduleInput은 입력 검증 실패의 센티널이다(핸들러가 400 매핑).
var ErrScanScheduleInput = errors.New("store: invalid scan schedule input")

// ErrScanScheduleRunActive는 이미 실행 중인데 새 실행을 요청했을 때다(핸들러 409).
var ErrScanScheduleRunActive = errors.New("store: scan schedule run already active")

// ScanScheduleRunActiveError는 ErrScanScheduleRunActive에 기존 실행의 문맥을 실은
// 형태다 — 화면이 "주기 실행이 도는 중"과 "내가 방금 누른 수동 실행"을 가른다.
// errors.Is(err, ErrScanScheduleRunActive)는 계속 참이다(ScanJobStateError 규약).
type ScanScheduleRunActiveError struct {
	RunID   string
	Trigger string
}

func (e *ScanScheduleRunActiveError) Error() string {
	return "store: scan schedule run already active: " + e.RunID + " (" + e.Trigger + ")"
}

func (e *ScanScheduleRunActiveError) Is(target error) bool { return target == ErrScanScheduleRunActive }

// ErrScanScheduleRunNotFound는 존재하지 않는 실행 종료 요청에 반환한다.
var ErrScanScheduleRunNotFound = errors.New("store: scan schedule run not found")

// ScanSchedule은 정기 스캔 설정 1건이다.
type ScanSchedule struct {
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	Targets          []string   `json:"targets"`
	PortSpec         *string    `json:"port_spec"`
	IntervalSeconds  *int       `json:"interval_seconds"`
	RatePPS          *int       `json:"rate_pps"`
	ExecutorScope    string     `json:"executor_scope"`
	NextRunAt        *time.Time `json:"next_run_at"`
	LastRunStartedAt *time.Time `json:"last_run_started_at"`
	LastRunID        *string    `json:"last_run_id"`
	UpdatedBy        *string    `json:"updated_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ScanScheduleRun은 정기 스캔 실행 1건이다.
type ScanScheduleRun struct {
	ID                  string     `json:"id"`
	ScheduleID          string     `json:"schedule_id"`
	Trigger             string     `json:"trigger"`
	Status              string     `json:"status"`
	SweepID             *string    `json:"sweep_id"`
	DispatchedJobs      int        `json:"dispatched_jobs"`
	DispatchedEndpoints int64      `json:"dispatched_endpoints"`
	StartedAt           time.Time  `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at"`
	Error               *string    `json:"error"`
}

// ScanScheduleActor는 변경 요청의 감사 주체다(SweepActor와 같은 규약).
type ScanScheduleActor struct {
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// PortSpecValidator는 port_spec 원문의 유효성 판정자다.
// store는 discovery를 import할 수 없어(순환) 호출자가 discovery.ParsePortSpec을
// 감싸 주입한다. nil이면 "비어 있지 않은지"까지만 본다.
type PortSpecValidator func(spec string) error

// ScanScheduleInput은 preset 스케줄 생성 요청이다.
// full·registered는 0019가 시드한 1행이 정본이라 생성 경로가 없다.
type ScanScheduleInput struct {
	Name             string
	Enabled          bool
	Targets          []string // IP/CIDR 입력 원문
	PortSpec         string   // 포트 사양 원문('common', '443,8443' 등)
	IntervalSeconds  *int     // nil이면 환경변수 기본 주기
	RatePPS          *int     // nil이면 스윕 기본값
	ExecutorScope    string   // 'server' 또는 'sensor:<uuid>' (빈 값이면 'server')
	ValidatePortSpec PortSpecValidator
	Actor            ScanScheduleActor
}

// ScanScheduleUpdate는 부분 수정 요청이다. nil 필드는 "변경 없음"이고,
// Clear* 플래그가 켜지면 그 컬럼을 NULL로 되돌린다(값 지정과 동시에 켜면 입력 오류).
type ScanScheduleUpdate struct {
	Name                 *string
	Enabled              *bool
	Targets              *[]string
	PortSpec             *string
	IntervalSeconds      *int
	ClearIntervalSeconds bool
	RatePPS              *int
	ClearRatePPS         bool
	ExecutorScope        *string
	ValidatePortSpec     PortSpecValidator
	Actor                ScanScheduleActor
}

// ClaimedScanSchedule은 방금 클레임된 스케줄 1건과 그 실행 행이다.
// 호출자는 이 값으로 스윕을 발주하고 RunID로 결과를 되돌려 준다.
type ClaimedScanSchedule struct {
	ScheduleID      string
	RunID           string
	Kind            string
	Name            string
	Targets         []string
	PortSpec        string
	ExecutorScope   string
	RatePPS         int // 0이면 호출자 기본값(스윕 기본 rate)
	IntervalSeconds int // 적용된 유효 주기(초)
	StartedAt       time.Time
}

// ScanScheduleRunResult는 실행 종료 보고다.
type ScanScheduleRunResult struct {
	Status              string // completed | failed | canceled
	SweepID             *string
	DispatchedJobs      int
	DispatchedEndpoints int64
	Error               *string
}

const scanScheduleColumns = `s.id::text, s.kind, s.name, s.enabled,
	array_to_string(s.targets, chr(31)), s.port_spec, s.interval_seconds, s.rate_pps,
	s.executor_scope, s.next_run_at, s.last_run_started_at, s.last_run_id::text,
	s.updated_by::text, s.created_at, s.updated_at`

// scanScheduleRow는 scanScheduleColumns 순서 그대로 1행을 읽는다.
func scanScheduleRow(row interface{ Scan(...any) error }) (ScanSchedule, error) {
	var (
		sc         ScanSchedule
		targetsStr string
	)
	err := row.Scan(&sc.ID, &sc.Kind, &sc.Name, &sc.Enabled, &targetsStr, &sc.PortSpec,
		&sc.IntervalSeconds, &sc.RatePPS, &sc.ExecutorScope, &sc.NextRunAt,
		&sc.LastRunStartedAt, &sc.LastRunID, &sc.UpdatedBy, &sc.CreatedAt, &sc.UpdatedAt)
	if err != nil {
		return ScanSchedule{}, err
	}
	sc.Targets = splitNonEmpty(targetsStr, sansSep)
	if sc.Targets == nil {
		sc.Targets = []string{}
	}
	return sc, nil
}

// ---------------------------------------------------------------------------
// 조회
// ---------------------------------------------------------------------------

// ListScanSchedules는 전체 스케줄을 kind(full → preset → registered), 이름 순으로
// 반환한다. 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12).
func (s *Store) ListScanSchedules(ctx context.Context) ([]ScanSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+scanScheduleColumns+`
		  FROM scan_schedules s
		 ORDER BY CASE s.kind WHEN 'full' THEN 0 WHEN 'preset' THEN 1 ELSE 2 END,
		          s.name ASC, s.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: list scan schedules: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []ScanSchedule{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		sc, err := scanScheduleRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan scan schedule: %w", err)
		}
		items = append(items, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate scan schedules: %w", err)
	}
	return items, nil
}

// GetScanSchedule은 스케줄 1건을 조회한다. 없으면 ErrScanScheduleNotFound.
func (s *Store) GetScanSchedule(ctx context.Context, id string) (*ScanSchedule, error) {
	sc, err := scanScheduleRow(s.db.QueryRowContext(ctx,
		`SELECT `+scanScheduleColumns+` FROM scan_schedules s WHERE s.id = $1::uuid`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get scan schedule: %w", err)
	}
	return &sc, nil
}

// GetScanScheduleByKind는 단일 행 종류(full·registered)를 조회한다.
func (s *Store) GetScanScheduleByKind(ctx context.Context, kind string) (*ScanSchedule, error) {
	if kind != ScheduleKindFull && kind != ScheduleKindRegistered {
		return nil, fmt.Errorf("%w: kind %q는 단일 행이 아닙니다", ErrScanScheduleInput, kind)
	}
	sc, err := scanScheduleRow(s.db.QueryRowContext(ctx,
		`SELECT `+scanScheduleColumns+` FROM scan_schedules s WHERE s.kind = $1`, kind))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get scan schedule by kind: %w", err)
	}
	return &sc, nil
}

// ---------------------------------------------------------------------------
// 검증
// ---------------------------------------------------------------------------

// normalizeTargets는 공백을 털고 빈 항목을 버린다(입력 원문은 그대로 보존).
func normalizeTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if v := strings.TrimSpace(t); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// validateScanSchedule은 kind별 불변식을 검사한다. DB CHECK와 같은 규칙을 앞단에서
// 한 번 더 보는 이유는 오류 문구를 한국어로 돌려주기 위해서다(23514는 문구가 없다).
func validateScanSchedule(kind, name string, enabled bool, targets []string, portSpec string,
	intervalSeconds *int, ratePPS *int, executorScope string, validate PortSpecValidator) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: 이름이 비었습니다", ErrScanScheduleInput)
	}
	if strings.TrimSpace(executorScope) == "" {
		return fmt.Errorf("%w: 실행 주체(executor_scope)가 비었습니다", ErrScanScheduleInput)
	}
	if executorScope != domain.ExecutorScopeServer && !strings.HasPrefix(executorScope, "sensor:") {
		return fmt.Errorf("%w: 실행 주체 %q의 형식이 잘못되었습니다 ('server' 또는 'sensor:<id>')",
			ErrScanScheduleInput, executorScope)
	}
	if ratePPS != nil && *ratePPS < 1 {
		return fmt.Errorf("%w: 초당 프로브 수는 1 이상이어야 합니다", ErrScanScheduleInput)
	}

	if kind == ScheduleKindRegistered {
		// 등록된 인증서 스캔은 대상·포트를 쓰지 않는다 — 대상은 이미 등록된
		// 엔드포인트이고, "새 인증서를 찾는 일"은 full·preset의 몫이다.
		if len(targets) > 0 {
			return fmt.Errorf("%w: 등록된 인증서 정기 스캔은 대상을 지정하지 않습니다", ErrScanScheduleInput)
		}
		if strings.TrimSpace(portSpec) != "" {
			return fmt.Errorf("%w: 등록된 인증서 정기 스캔은 포트를 지정하지 않습니다", ErrScanScheduleInput)
		}
		if intervalSeconds != nil && *intervalSeconds < 1 {
			return fmt.Errorf("%w: 주기는 1초 이상이어야 합니다", ErrScanScheduleInput)
		}
		return nil
	}

	if enabled && len(targets) == 0 {
		return fmt.Errorf("%w: 대상이 없으면 정기 스캔을 켤 수 없습니다", ErrScanScheduleInput)
	}
	if spec := strings.TrimSpace(portSpec); spec == "" {
		if enabled {
			return fmt.Errorf("%w: 포트 사양이 비었습니다", ErrScanScheduleInput)
		}
	} else if validate != nil {
		if err := validate(spec); err != nil {
			return fmt.Errorf("%w: %s", ErrScanScheduleInput, err.Error())
		}
	}
	if intervalSeconds != nil && *intervalSeconds < MinScheduleIntervalSeconds {
		return fmt.Errorf("%w: 정기 스캔 주기는 %d초(1시간) 이상이어야 합니다",
			ErrScanScheduleInput, MinScheduleIntervalSeconds)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 생성 · 수정 · 삭제
// ---------------------------------------------------------------------------

// CreateScanSchedule은 preset 스케줄 1건을 만들고 감사 이벤트를 남긴다.
// full·registered는 시드 1행이 정본이라 이 경로로 만들 수 없다(ErrScanScheduleKind).
//
// enabled로 만들면 next_run_at은 **now() + 주기**다. 생성 즉시 도는 설계가 아닌
// 이유는 대역이 넓을 때 등록 버튼 하나가 전 대역 스윕이 되기 때문이다 —
// 즉시 실행이 필요하면 '지금 실행'(StartManualScanScheduleRun)이 그 자리다.
func (s *Store) CreateScanSchedule(ctx context.Context, in ScanScheduleInput) (*ScanSchedule, error) {
	scope := strings.TrimSpace(in.ExecutorScope)
	if scope == "" {
		scope = domain.ExecutorScopeServer
	}
	targets := normalizeTargets(in.Targets)
	portSpec := strings.TrimSpace(in.PortSpec)
	if err := validateScanSchedule(ScheduleKindPreset, in.Name, in.Enabled, targets, portSpec,
		in.IntervalSeconds, in.RatePPS, scope, in.ValidatePortSpec); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_schedules (
			kind, name, enabled, targets, port_spec, interval_seconds, rate_pps,
			executor_scope, next_run_at, updated_by)
		VALUES (
			'preset', $1, $2,
			coalesce(string_to_array(nullif($3, ''), chr(31)), '{}'),
			nullif($4, ''), $5::int, $6::int, $7,
			CASE WHEN $2 THEN now() + make_interval(secs => coalesce($5::int, $8::int)) END,
			$9::uuid)
		RETURNING id::text`,
		strings.TrimSpace(in.Name), in.Enabled, strings.Join(targets, sansSep), portSpec,
		in.IntervalSeconds, in.RatePPS, scope, defaultScheduleIntervalSeconds(in.IntervalSeconds),
		in.Actor.ActorID,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("store: insert scan schedule: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySchedule,
		ActorID:    in.Actor.ActorID,
		TargetType: "scan_schedule",
		TargetID:   &id,
		RequestID:  in.Actor.RequestID,
		After: map[string]any{
			"phase":          "created",
			"kind":           ScheduleKindPreset,
			"name":           strings.TrimSpace(in.Name),
			"enabled":        in.Enabled,
			"targets":        targets,
			"port_spec":      portSpec,
			"executor_scope": scope,
		},
		Result:   domain.AuditSuccess,
		SourceIP: in.Actor.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return s.GetScanSchedule(ctx, id)
}

// defaultScheduleIntervalSeconds는 next_run_at 계산의 SQL 폴백 값이다.
// 주기가 지정되지 않은 스케줄(NULL = 환경변수 기본)에도 예정 시각은 있어야 하므로
// 하한값을 쓴다 — 스케줄러가 실제로 돌 때는 자신의 기본 주기로 다시 계산한다.
func defaultScheduleIntervalSeconds(iv *int) int {
	if iv != nil && *iv > 0 {
		return *iv
	}
	return MinScheduleIntervalSeconds
}

// UpdateScanSchedule은 스케줄을 부분 수정하고 감사 이벤트를 남긴다.
// kind는 바꿀 수 없다(생성 시 고정). registered 행은 enabled·interval만 의미가 있다.
//
// 꺼져 있던 스케줄을 켜면 next_run_at을 now() + 주기로 새로 잡고, 끄면 NULL로
// 비운다 — 예정 시각이 남아 있으면 "꺼졌는데 다음 실행이 보이는" 화면이 된다.
func (s *Store) UpdateScanSchedule(ctx context.Context, id string, in ScanScheduleUpdate) (*ScanSchedule, error) {
	if in.IntervalSeconds != nil && in.ClearIntervalSeconds {
		return nil, fmt.Errorf("%w: 주기를 지정하면서 동시에 비울 수 없습니다", ErrScanScheduleInput)
	}
	if in.RatePPS != nil && in.ClearRatePPS {
		return nil, fmt.Errorf("%w: 속도를 지정하면서 동시에 비울 수 없습니다", ErrScanScheduleInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	cur, err := scanScheduleRow(tx.QueryRowContext(ctx,
		`SELECT `+scanScheduleColumns+` FROM scan_schedules s WHERE s.id = $1::uuid FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: lock scan schedule: %w", err)
	}

	next := cur
	if in.Name != nil {
		next.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.Targets != nil {
		next.Targets = normalizeTargets(*in.Targets)
	}
	if in.PortSpec != nil {
		if spec := strings.TrimSpace(*in.PortSpec); spec == "" {
			next.PortSpec = nil
		} else {
			next.PortSpec = &spec
		}
	}
	switch {
	case in.ClearIntervalSeconds:
		next.IntervalSeconds = nil
	case in.IntervalSeconds != nil:
		v := *in.IntervalSeconds
		next.IntervalSeconds = &v
	}
	switch {
	case in.ClearRatePPS:
		next.RatePPS = nil
	case in.RatePPS != nil:
		v := *in.RatePPS
		next.RatePPS = &v
	}
	if in.ExecutorScope != nil {
		next.ExecutorScope = strings.TrimSpace(*in.ExecutorScope)
	}

	portSpec := ""
	if next.PortSpec != nil {
		portSpec = *next.PortSpec
	}
	if err := validateScanSchedule(next.Kind, next.Name, next.Enabled, next.Targets, portSpec,
		next.IntervalSeconds, next.RatePPS, next.ExecutorScope, in.ValidatePortSpec); err != nil {
		return nil, err
	}

	// next_run_at 규약:
	//   꺼짐            → NULL
	//   꺼짐 → 켜짐     → now() + 주기 (새로 예약)
	//   켜짐 유지       → 기존 예정 유지. 단 비어 있으면(과거 비활성 잔재) 새로 잡는다.
	// 수정이 주기를 리셋하지 않는 이유는 '지금 실행'과 같다 — 설정을 만질 때마다
	// 예정이 밀리면 자주 손대는 스케줄은 영영 돌지 않는다.
	reschedule := next.Enabled && (!cur.Enabled || cur.NextRunAt == nil)

	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_schedules
		   SET name = $2, enabled = $3,
		       targets = coalesce(string_to_array(nullif($4, ''), chr(31)), '{}'),
		       port_spec = nullif($5, ''),
		       interval_seconds = $6::int,
		       rate_pps = $7::int,
		       executor_scope = $8,
		       next_run_at = CASE
		           WHEN NOT $3 THEN NULL
		           WHEN $9 THEN now() + make_interval(secs => coalesce($6::int, $10::int))
		           ELSE next_run_at END,
		       updated_by = $11::uuid,
		       updated_at = now()
		 WHERE id = $1::uuid`,
		id, next.Name, next.Enabled, strings.Join(next.Targets, sansSep), portSpec,
		next.IntervalSeconds, next.RatePPS, next.ExecutorScope, reschedule,
		defaultScheduleIntervalSeconds(next.IntervalSeconds), in.Actor.ActorID); err != nil {
		return nil, fmt.Errorf("store: update scan schedule: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySchedule,
		ActorID:    in.Actor.ActorID,
		TargetType: "scan_schedule",
		TargetID:   &id,
		RequestID:  in.Actor.RequestID,
		Before:     scanScheduleAuditValue(cur),
		After:      scanScheduleAuditValue(next),
		Result:     domain.AuditSuccess,
		SourceIP:   in.Actor.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return s.GetScanSchedule(ctx, id)
}

// scanScheduleAuditValue는 감사 before/after에 실을 설정 스냅샷이다.
// 인증서·개인키가 지나가지 않는 축이지만 스냅샷 필드는 명시 나열로 고정한다.
func scanScheduleAuditValue(sc ScanSchedule) map[string]any {
	v := map[string]any{
		"kind":           sc.Kind,
		"name":           sc.Name,
		"enabled":        sc.Enabled,
		"targets":        sc.Targets,
		"executor_scope": sc.ExecutorScope,
	}
	if sc.PortSpec != nil {
		v["port_spec"] = *sc.PortSpec
	}
	if sc.IntervalSeconds != nil {
		v["interval_seconds"] = *sc.IntervalSeconds
	}
	if sc.RatePPS != nil {
		v["rate_pps"] = *sc.RatePPS
	}
	return v
}

// DeleteScanSchedule은 preset 스케줄을 지우고 감사 이벤트를 남긴다.
// full·registered는 지울 수 없다(ErrScanScheduleKind) — 시드 1행이 정본이고
// 비활성화가 곧 "쓰지 않음"이다. 실행 이력은 FK CASCADE로 함께 사라지고,
// 그 실행이 발주했던 스윕은 schedule_id만 NULL이 되어 그대로 남는다.
func (s *Store) DeleteScanSchedule(ctx context.Context, id string, act ScanScheduleActor) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	cur, err := scanScheduleRow(tx.QueryRowContext(ctx,
		`SELECT `+scanScheduleColumns+` FROM scan_schedules s WHERE s.id = $1::uuid FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrScanScheduleNotFound
	}
	if err != nil {
		return fmt.Errorf("store: lock scan schedule: %w", err)
	}
	if cur.Kind != ScheduleKindPreset {
		return ErrScanScheduleKind
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_schedules WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("store: delete scan schedule: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySchedule,
		ActorID:    act.ActorID,
		TargetType: "scan_schedule",
		TargetID:   &id,
		RequestID:  act.RequestID,
		Before:     scanScheduleAuditValue(cur),
		After:      map[string]any{"phase": "deleted"},
		Result:     domain.AuditSuccess,
		SourceIP:   act.SourceIP,
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 클레임 · 실행
// ---------------------------------------------------------------------------

// scheduleTickLockKey는 정기 스캔 tick의 advisory lock 키('sched'의 ASCII 바이트)다.
// 재스캔 tick(x'72657363616e')·알림 tick(x'6e6f74696679')과 **다른 키**여야 한다 —
// 같은 키를 쓰면 두 스케줄러가 서로를 조용히 건너뛴다.
const scheduleTickLockKey = `x'7363686564'::bigint`

// ClaimDueScanSchedules는 주기가 도래한 full·preset 스케줄을 클레임하고
// 각각에 running 실행 행을 연다.
//
// 규약 3개가 이 함수의 전부다.
//   - 다음 실행 시각 = **실행이 실제로 시작한 시각 + 주기**. 예정 시각 기준이 아니라
//     시작 시각 기준이라 실행이 밀려도 주기가 촘촘해지지 않는다.
//   - 활성 실행이 있으면 그 주기는 **건너뛴다**(next_run_at을 밀지 않는다) —
//     끝나는 즉시 다음 tick이 집어간다. 겹침은 절대 없다.
//   - registered는 대상이 아니다. 그 종류는 기존 재스캔 엔진이 돌린다.
//
// locked=false는 다른 인스턴스가 이미 tick을 도는 중이라는 뜻이며 오류가 아니다
// (ClaimDueEndpointMonitors·RunNotificationTick과 같은 규약, 키만 다르다).
func (s *Store) ClaimDueScanSchedules(ctx context.Context, defaultInterval time.Duration, limit int) ([]ClaimedScanSchedule, bool, error) {
	if limit <= 0 {
		return nil, true, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var locked bool
	if err := tx.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(`+scheduleTickLockKey+`)`).Scan(&locked); err != nil {
		return nil, false, fmt.Errorf("store: scan schedule tick lock: %w", err)
	}
	if !locked {
		return nil, false, nil
	}

	// 실행 행 INSERT와 스케줄 UPDATE를 한 문장에 담는다 — 두 문장으로 나누면
	// 그 사이에 수동 실행이 끼어들 수 있다. 같은 행을 두 번 UPDATE하지 않도록
	// (Postgres는 한 문장 안 이중 UPDATE를 보장하지 않는다) 실행 행을 먼저 만들고
	// 그 id·started_at으로 스케줄을 한 번만 갱신한다.
	rows, err := tx.QueryContext(ctx, `
		WITH due AS (
			SELECT s.id
			  FROM scan_schedules s
			 WHERE s.enabled
			   AND s.kind IN ('full', 'preset')
			   AND s.next_run_at IS NOT NULL AND s.next_run_at <= now()
			   AND cardinality(s.targets) > 0
			   AND btrim(coalesce(s.port_spec, '')) <> ''
			   AND NOT EXISTS (
			       SELECT 1 FROM scan_schedule_runs r
			        WHERE r.schedule_id = s.id AND r.status = 'running')
			 ORDER BY s.next_run_at ASC
			 LIMIT $2
			 FOR UPDATE OF s SKIP LOCKED),
		started AS (
			INSERT INTO scan_schedule_runs (schedule_id, trigger)
			SELECT due.id, 'schedule' FROM due
			RETURNING id, schedule_id, started_at)
		UPDATE scan_schedules s
		   SET next_run_at = started.started_at
		         + make_interval(secs => coalesce(s.interval_seconds, $1::int)),
		       last_run_started_at = started.started_at,
		       last_run_id = started.id
		  FROM started
		 WHERE s.id = started.schedule_id
		RETURNING s.id::text, started.id::text, s.kind, s.name,
		          array_to_string(s.targets, chr(31)), coalesce(s.port_spec, ''),
		          s.executor_scope, coalesce(s.rate_pps, 0),
		          coalesce(s.interval_seconds, $1::int), started.started_at`,
		positiveSeconds(defaultInterval), limit)
	if err != nil {
		return nil, true, fmt.Errorf("store: claim due scan schedules: %w", err)
	}

	var out []ClaimedScanSchedule
	for rows.Next() {
		var (
			c          ClaimedScanSchedule
			targetsStr string
		)
		if err := rows.Scan(&c.ScheduleID, &c.RunID, &c.Kind, &c.Name, &targetsStr,
			&c.PortSpec, &c.ExecutorScope, &c.RatePPS, &c.IntervalSeconds, &c.StartedAt); err != nil {
			rows.Close() //nolint:errcheck
			return nil, true, fmt.Errorf("store: scan claimed scan schedule: %w", err)
		}
		c.Targets = splitNonEmpty(targetsStr, sansSep)
		out = append(out, c)
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return nil, true, fmt.Errorf("store: claim due scan schedules: %w", err)
	}

	// UPDATE ... RETURNING의 행 순서는 보장되지 않는다 — 발주 순서를 재현 가능하게
	// 고정한다(ClaimDueEndpointMonitors와 같은 이유).
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ScheduleID < out[j].ScheduleID
	})

	if err := tx.Commit(); err != nil {
		return nil, true, fmt.Errorf("store: commit: %w", err)
	}
	return out, true, nil
}

// StartManualScanScheduleRun은 '지금 실행' 1건을 연다.
//
// **next_run_at을 건드리지 않는다** — 예정된 다음 실행은 그대로 두고 1회 추가
// 실행만 한다(사용자 확정 규약). 활성 실행이 있으면 *ScanScheduleRunActiveError를
// 반환해 핸들러가 409로 옮긴다.
//
// tick과 같은 advisory lock을 **블로킹으로** 잡는다: tick은 try(못 잡으면 skip)라
// 사람이 누른 실행이 조용히 사라지지 않고, 부분 유니크 인덱스와의 경합(23505)도
// 여기서 사라진다.
func (s *Store) StartManualScanScheduleRun(ctx context.Context, id string, act ScanScheduleActor) (*ScanScheduleRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(`+scheduleTickLockKey+`)`); err != nil {
		return nil, fmt.Errorf("store: scan schedule tick lock: %w", err)
	}

	cur, err := scanScheduleRow(tx.QueryRowContext(ctx,
		`SELECT `+scanScheduleColumns+` FROM scan_schedules s WHERE s.id = $1::uuid FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: lock scan schedule: %w", err)
	}
	if cur.Kind != ScheduleKindRegistered {
		if len(cur.Targets) == 0 {
			return nil, fmt.Errorf("%w: 대상이 없어 실행할 수 없습니다", ErrScanScheduleInput)
		}
		if cur.PortSpec == nil || strings.TrimSpace(*cur.PortSpec) == "" {
			return nil, fmt.Errorf("%w: 포트 사양이 없어 실행할 수 없습니다", ErrScanScheduleInput)
		}
	}

	var activeID, activeTrigger string
	err = tx.QueryRowContext(ctx, `
		SELECT id::text, trigger FROM scan_schedule_runs
		 WHERE schedule_id = $1::uuid AND status = 'running'
		 ORDER BY started_at DESC LIMIT 1`, id).Scan(&activeID, &activeTrigger)
	if err == nil {
		return nil, &ScanScheduleRunActiveError{RunID: activeID, Trigger: activeTrigger}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: find active scan schedule run: %w", err)
	}

	run, err := scanScheduleRunRow(tx.QueryRowContext(ctx, `
		INSERT INTO scan_schedule_runs (schedule_id, trigger)
		VALUES ($1::uuid, 'manual')
		RETURNING `+scanScheduleRunColumns, id))
	if err != nil {
		return nil, fmt.Errorf("store: insert manual scan schedule run: %w", err)
	}

	// next_run_at은 그대로 둔다. 마지막 실행 표시만 움직인다.
	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_schedules
		   SET last_run_started_at = $2, last_run_id = $3::uuid
		 WHERE id = $1::uuid`, id, run.StartedAt, run.ID); err != nil {
		return nil, fmt.Errorf("store: mark scan schedule run started: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySchedule,
		ActorID:    act.ActorID,
		TargetType: "scan_schedule",
		TargetID:   &id,
		RequestID:  act.RequestID,
		After: map[string]any{
			"phase":   "run_started",
			"kind":    cur.Kind,
			"trigger": ScheduleTriggerManual,
			"run_id":  run.ID,
		},
		Result:   domain.AuditSuccess,
		SourceIP: act.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return &run, nil
}

// FinishScanScheduleRun은 실행 1건을 종료 상태로 전이한다.
// running이 아니면 ErrScanScheduleRunNotFound다 — 이미 종료된 실행을 다시
// 종료해도 카운터가 덧씌워지지 않게 한다(MarkScanFinished와 같은 규약).
func (s *Store) FinishScanScheduleRun(ctx context.Context, runID string, res ScanScheduleRunResult) (*ScanScheduleRun, error) {
	switch res.Status {
	case ScheduleRunCompleted, ScheduleRunFailed, ScheduleRunCanceled:
	default:
		return nil, fmt.Errorf("%w: 종료 상태 %q는 허용되지 않습니다", ErrScanScheduleInput, res.Status)
	}

	run, err := scanScheduleRunRow(s.db.QueryRowContext(ctx, `
		UPDATE scan_schedule_runs
		   SET status = $2, finished_at = now(),
		       sweep_id = coalesce($3::uuid, sweep_id),
		       dispatched_jobs = $4, dispatched_endpoints = $5,
		       error = $6
		 WHERE id = $1::uuid AND status = 'running'
		RETURNING `+scanScheduleRunColumns,
		runID, res.Status, res.SweepID, res.DispatchedJobs, res.DispatchedEndpoints, res.Error))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScanScheduleRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: finish scan schedule run: %w", err)
	}
	return &run, nil
}

// AttachScanScheduleRunSweep은 실행 행에 발주된 스윕을 연결한다.
// 발주와 종료 사이에도 화면이 "어떤 스윕이 도는지"를 볼 수 있어야 한다.
func (s *Store) AttachScanScheduleRunSweep(ctx context.Context, runID, sweepID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE scan_schedule_runs SET sweep_id = $2::uuid
		 WHERE id = $1::uuid AND status = 'running'`, runID, sweepID)
	if err != nil {
		return fmt.Errorf("store: attach scan schedule run sweep: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: attach scan schedule run sweep: %w", err)
	}
	if n == 0 {
		return ErrScanScheduleRunNotFound
	}
	return nil
}

const scanScheduleRunColumns = `id::text, schedule_id::text, trigger, status, sweep_id::text,
	dispatched_jobs, dispatched_endpoints, started_at, finished_at, error`

// scanScheduleRunRow는 scanScheduleRunColumns 순서 그대로 1행을 읽는다.
func scanScheduleRunRow(row interface{ Scan(...any) error }) (ScanScheduleRun, error) {
	var r ScanScheduleRun
	if err := row.Scan(&r.ID, &r.ScheduleID, &r.Trigger, &r.Status, &r.SweepID,
		&r.DispatchedJobs, &r.DispatchedEndpoints, &r.StartedAt, &r.FinishedAt, &r.Error); err != nil {
		return ScanScheduleRun{}, err
	}
	return r, nil
}

// ListScanScheduleRunsInput은 실행 이력 조회 조건이다.
type ListScanScheduleRunsInput struct {
	ScheduleID string   // 비면 전체 스케줄
	Statuses   []string // 비면 전체
	Page       int      // 1부터
	PageSize   int      // 1..maxPageSize
}

// ScanScheduleRunList는 실행 이력 조회 결과다.
type ScanScheduleRunList struct {
	Items    []ScanScheduleRun
	Total    int
	Page     int
	PageSize int
}

// ListScanScheduleRuns는 실행 이력을 최신순 페이지네이션으로 조회한다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12).
func (s *Store) ListScanScheduleRuns(ctx context.Context, in ListScanScheduleRunsInput) (*ScanScheduleRunList, error) {
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
	if strings.TrimSpace(in.ScheduleID) != "" {
		args = append(args, in.ScheduleID)
		conds = append(conds, fmt.Sprintf("schedule_id = $%d::uuid", len(args)))
	}
	if len(in.Statuses) > 0 {
		args = append(args, strings.Join(in.Statuses, sansSep))
		conds = append(conds, fmt.Sprintf("status = ANY(string_to_array($%d, chr(31)))", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM scan_schedule_runs"+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("store: count scan schedule runs: %w", err)
	}

	// id 보조 정렬로 페이지 경계를 안정화한다.
	query := fmt.Sprintf("SELECT %s FROM scan_schedule_runs%s ORDER BY started_at DESC, id ASC LIMIT $%d OFFSET $%d",
		scanScheduleRunColumns, where, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list scan schedule runs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []ScanScheduleRun{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		r, err := scanScheduleRunRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan scan schedule run: %w", err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate scan schedule runs: %w", err)
	}
	return &ScanScheduleRunList{Items: items, Total: total, Page: page, PageSize: size}, nil
}
