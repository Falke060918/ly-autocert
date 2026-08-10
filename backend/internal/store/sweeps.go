// sweeps.go — 전 포트 백그라운드 스윕(docs/design/04-port-autoscan.md)의 영속화.
// 부모 scan_sweeps 1행 + 자식 청크는 기존 scan_jobs 행을 그대로 쓴다 —
// scan_jobs 상태 enum(pending/leased/running/completed/failed)과 기존 ON CONFLICT
// 아비터는 한 글자도 건드리지 않는다 (0010 사고 / GOTCHAS "Contract drift").
//
// 진행률은 부모에 카운터를 두지 않고 자식 Job 집계로 매번 파생한다 —
// 카운터를 하나 더 만들면 하트비트·관측 배치에 이은 3번째 작성자가 생겨
// 이중 계상된다 (0011 사고 / GOTCHAS "Sensor job progress counters overshoot").
//
// 이 파일은 discovery 패키지를 import하지 않는다: 의존 방향이 discovery → store
// 단방향이라 역방향은 순환이다. 그래서 포트 목록·청크 계산 입력은 전부 인자로 받는다.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/audit"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
)

// EventDiscoverySweep은 스윕 생성·상태 전이 감사 이벤트 타입이다.
// 자식 청크 Job의 발주·종료 감사는 기존 EventDiscoveryScan을 그대로 쓴다.
const EventDiscoverySweep = "discovery.sweep"

// 스윕 상태(자체 상태 머신 — scan_jobs enum과 별개다, 설계 4.2).
const (
	SweepStatusQueued    = "queued"
	SweepStatusRunning   = "running"
	SweepStatusPaused    = "paused"
	SweepStatusCompleted = "completed"
	SweepStatusFailed    = "failed"
	SweepStatusCanceled  = "canceled"
)

// 스윕 기본값. 환경변수(SCAN_SWEEP_*) 해석은 config 계층 소관이고,
// store는 0/미지정을 받으면 같은 기본값으로 폴백한다.
const (
	DefaultSweepChunkEndpoints = 4096             // SCAN_SWEEP_CHUNK_ENDPOINTS
	DefaultSweepRatePPS        = 10               // SCAN_SWEEP_RATE_PPS
	DefaultSweepMaxDays        = 30               // SCAN_SWEEP_MAX_DAYS
	DefaultSweepStallAfter     = 10 * time.Minute // SCAN_SWEEP_STALL_AFTER
)

// sweepChunkMaxAttempts는 seq 1개당 허용하는 총 실행 횟수다(최초 1 + 재시도 1).
// 부분 유니크 인덱스 uq_scan_jobs_sweep_seq_active가 failed 행을 비켜가므로
// 재발주 자체는 가능하고, 이 상수가 무한 재시도를 막는다 (설계 5.3).
const sweepChunkMaxAttempts = 2

// 웨이브 경계(설계 4.3). 프리셋 포트는 호출자가 priority로 넘긴다 —
// common 프리셋 정의는 discovery 패키지에 있고 store는 그것을 import할 수 없다.
const (
	waveWellKnownMaxPort  = 1023
	waveRegisteredMaxPort = 49151
)

// ErrSweepNotFound는 존재하지 않는 스윕 조회·전이 시 반환한다.
var ErrSweepNotFound = errors.New("store: scan sweep not found")

// ErrSweepState는 상태 머신이 허용하지 않는 전이 요청에 반환한다.
var ErrSweepState = errors.New("store: scan sweep state conflict")

// ErrSweepTooLong은 ETA 상한 초과 거부의 센티넬이다(핸들러가 422 매핑).
var ErrSweepTooLong = errors.New("store: scan sweep exceeds the duration limit")

// SweepTooLongError는 거부와 함께 **실제 소요 일수**를 담는다.
// 숫자 없이 거부하면 사용자는 대역을 얼마나 줄여야 할지 알 수 없다 (설계 5.4).
type SweepTooLongError struct {
	EstimatedDays float64
	MaxDays       int
}

func (e *SweepTooLongError) Error() string {
	return fmt.Sprintf("이 스윕은 약 %s일이 걸립니다. 대상 범위를 줄이거나 포트 스펙을 좁혀 주세요. (제한: %d일)",
		formatSweepDays(e.EstimatedDays), e.MaxDays)
}

// Unwrap으로 errors.Is(err, ErrSweepTooLong) 판정을 열어둔다.
func (e *SweepTooLongError) Unwrap() error { return ErrSweepTooLong }

// formatSweepDays는 소요 일수를 사람이 읽는 형태로 만든다(10일 미만은 소수 1자리).
func formatSweepDays(d float64) string {
	if d < 10 {
		return strconv.FormatFloat(math.Round(d*10)/10, 'f', -1, 64)
	}
	return strconv.FormatFloat(math.Round(d), 'f', 0, 64)
}

// SweepInput은 스윕 생성 요청이다.
// Targets·PortSpec은 재현·감사용 **입력 원문**이고(0006 scan_jobs.targets 규약),
// IPCount·PortCount는 호출자가 전개한 결과 개수다 — store는 discovery를
// import할 수 없어 CIDR·포트 스펙을 스스로 전개하지 않는다.
type SweepInput struct {
	Name           *string
	Targets        []string // IP/CIDR 입력 원문
	IPCount        int      // Targets를 전개한 개별 IP 수
	PortSpec       string   // 포트 사양 원문('1-65535', 'common' 등)
	PortCount      int      // PortSpec을 전개한 포트 수
	ExecutorScope  string   // 'server' 또는 'sensor:<uuid>' (0010 규약)
	RatePPS        int      // 0이면 DefaultSweepRatePPS
	ChunkEndpoints int      // 0이면 DefaultSweepChunkEndpoints
	MaxDays        int      // 0이면 DefaultSweepMaxDays
	ActorID        *string
	RequestID      *string
	SourceIP       *string
}

// SweepProgress는 자식 scan_jobs 집계에서 파생한 진행률이다(저장하지 않는다).
// 엔드포인트 카운터는 failed 행을 제외하고 합산한다 — 재시도된 청크의 부분
// 진행이 두 번 세어져 total_endpoints를 넘는 것을 막는다.
type SweepProgress struct {
	ChunksTotal        int   `json:"chunks_total"`
	ChunksCompleted    int   `json:"chunks_completed"`
	ChunksActive       int   `json:"chunks_active"`
	FailedAttempts     int   `json:"failed_attempts"`
	ScannedEndpoints   int64 `json:"scanned_endpoints"`
	ReachableEndpoints int64 `json:"reachable_endpoints"`
	CertificatesFound  int64 `json:"certificates_found"`
	CertificatesNew    int64 `json:"certificates_new"`
}

// Sweep은 스윕 1건이다. Progress는 조회 시점 집계다.
type Sweep struct {
	ID                string        `json:"id"`
	Name              *string       `json:"name,omitempty"`
	Status            string        `json:"status"`
	Targets           []string      `json:"targets"`
	PortSpec          string        `json:"port_spec"`
	ExecutorScope     string        `json:"executor_scope"`
	TotalEndpoints    int64         `json:"total_endpoints"`
	TotalChunks       int           `json:"total_chunks"`
	NextSeq           int           `json:"next_seq"`
	RatePPS           int           `json:"rate_pps"`
	ChunkEndpoints    int           `json:"chunk_endpoints"`
	EstimatedFinishAt *time.Time    `json:"estimated_finish_at"`
	StalledSince      *time.Time    `json:"stalled_since"`
	LastError         *string       `json:"last_error"`
	CreatedBy         *string       `json:"created_by,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	StartedAt         *time.Time    `json:"started_at"`
	FinishedAt        *time.Time    `json:"finished_at"`
	Progress          SweepProgress `json:"progress"`
}

// SweepActor는 상태 전이 요청의 감사 주체다.
type SweepActor struct {
	ActorID   *string
	RequestID *string
	SourceIP  *string
}

// ---------------------------------------------------------------------------
// 청크 계산 (순수 함수 — DB 비의존, 결정론적)
// ---------------------------------------------------------------------------

// WaveOrderPorts는 포트 목록을 웨이브 우선순위로 재정렬한다 (설계 4.3).
//
//	① priority(= common 프리셋) → ② 1-1023 → ③ 1024-49151 → ④ 49152-65535
//
// 같은 웨이브 안에서는 포트 번호 오름차순이다. 중복은 제거한다.
// priority는 호출자가 넘긴다 — 프리셋 정의는 discovery 패키지에 있고
// store가 그것을 import하면 순환이 된다.
//
// 이 순서가 곧 seq 순서이므로, 사용자가 며칠 뒤 스윕을 취소해도
// "얻을 것은 이미 얻은" 상태가 된다.
func WaveOrderPorts(ports, priority []int) []int {
	prio := make(map[int]bool, len(priority))
	for _, p := range priority {
		prio[p] = true
	}
	seen := make(map[int]bool, len(ports))
	out := make([]int, 0, len(ports))
	for _, p := range ports {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := portWave(out[i], prio), portWave(out[j], prio)
		if wi != wj {
			return wi < wj
		}
		return out[i] < out[j]
	})
	return out
}

// portWave는 포트 1개의 웨이브 번호(0이 가장 먼저)를 돌려준다.
func portWave(p int, prio map[int]bool) int {
	switch {
	case prio[p]:
		return 0
	case p <= waveWellKnownMaxPort:
		return 1
	case p <= waveRegisteredMaxPort:
		return 2
	default:
		return 3
	}
}

// sweepChunkGrid는 (IP 수, 포트 수, 청크 상한)에서 파생되는 청크 격자다.
// 포트-메이저 순서다: 포트 그룹 하나를 전 IP에 대해 소진한 뒤 다음 포트 그룹으로
// 넘어간다. 포트가 웨이브 순서로 정렬돼 있으므로 이 순서가 곧 우선순위다.
type sweepChunkGrid struct {
	ipsPerChunk   int
	portsPerChunk int
	ipGroups      int
	portGroups    int
}

func newSweepChunkGrid(ipCount, portCount, chunkSize int) sweepChunkGrid {
	if chunkSize < 1 {
		chunkSize = DefaultSweepChunkEndpoints
	}
	if ipCount < 1 || portCount < 1 {
		return sweepChunkGrid{}
	}
	ipsPerChunk := min(ipCount, chunkSize)
	portsPerChunk := min(max(chunkSize/ipsPerChunk, 1), portCount)
	return sweepChunkGrid{
		ipsPerChunk:   ipsPerChunk,
		portsPerChunk: portsPerChunk,
		ipGroups:      ceilDiv(ipCount, ipsPerChunk),
		portGroups:    ceilDiv(portCount, portsPerChunk),
	}
}

// count는 격자가 만드는 총 청크 수다.
func (g sweepChunkGrid) count() int { return g.ipGroups * g.portGroups }

// SweepChunkCount는 스윕 하나가 만드는 총 청크 수다(scan_sweeps.total_chunks).
func SweepChunkCount(ipCount, portCount, chunkSize int) int {
	return newSweepChunkGrid(ipCount, portCount, chunkSize).count()
}

// ChunkForSeq는 seq 하나로 그 청크의 엔드포인트 집합을 **결정론적으로 재생**한다
// (설계 5.2). 같은 (ips, ports, chunkSize, seq)는 언제나 같은 결과다 —
// 이 성질 덕분에 재시도가 멱등이고 청크 목록을 DB에 물리 저장할 필요가 없다.
//
// ports는 WaveOrderPorts를 거친 웨이브 순서여야 한다. 반환 슬라이스는 복사본이라
// 호출자가 수정해도 입력이 오염되지 않는다. 청크의 IP 수 × 포트 수는 항상
// chunkSize 이하다. 범위를 벗어난 seq는 ok=false다.
func ChunkForSeq(ips []string, ports []int, chunkSize, seq int) ([]string, []int, bool) {
	g := newSweepChunkGrid(len(ips), len(ports), chunkSize)
	if seq < 0 || seq >= g.count() {
		return nil, nil, false
	}
	portGroup, ipGroup := seq/g.ipGroups, seq%g.ipGroups
	ipStart := ipGroup * g.ipsPerChunk
	portStart := portGroup * g.portsPerChunk
	return append([]string(nil), ips[ipStart:min(ipStart+g.ipsPerChunk, len(ips))]...),
		append([]int(nil), ports[portStart:min(portStart+g.portsPerChunk, len(ports))]...),
		true
}

// ceilDiv는 올림 나눗셈이다(b > 0 전제).
func ceilDiv(a, b int) int { return (a + b - 1) / b }

// ---------------------------------------------------------------------------
// 생성
// ---------------------------------------------------------------------------

// CreateSweep은 queued 상태의 스윕을 만들고 생성 감사 이벤트를 남긴다.
// total_endpoints·total_chunks·estimated_finish_at은 여기서 파생한다.
// ETA가 MaxDays를 넘으면 행을 만들지 않고 *SweepTooLongError로 거부한다.
func (s *Store) CreateSweep(ctx context.Context, in SweepInput) (*Sweep, error) {
	ratePPS := in.RatePPS
	if ratePPS < 1 {
		ratePPS = DefaultSweepRatePPS
	}
	chunkEndpoints := in.ChunkEndpoints
	if chunkEndpoints < 1 {
		chunkEndpoints = DefaultSweepChunkEndpoints
	}
	maxDays := in.MaxDays
	if maxDays < 1 {
		maxDays = DefaultSweepMaxDays
	}
	if strings.TrimSpace(in.ExecutorScope) == "" {
		return nil, fmt.Errorf("store: sweep executor_scope is required")
	}

	// 전수 스캔은 대상×포트가 수천만이라 int로는 부족하다 — bigint로 센다.
	totalEndpoints := int64(in.IPCount) * int64(in.PortCount)
	totalChunks := SweepChunkCount(in.IPCount, in.PortCount, chunkEndpoints)
	etaSeconds := float64(totalEndpoints) / float64(ratePPS)
	if days := etaSeconds / 86400; days > float64(maxDays) {
		return nil, &SweepTooLongError{EstimatedDays: days, MaxDays: maxDays}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_sweeps (
			name, targets, port_spec, executor_scope, status,
			total_endpoints, total_chunks, rate_pps, chunk_endpoints,
			estimated_finish_at, created_by)
		VALUES (
			$1,
			coalesce(string_to_array(nullif($2, ''), chr(31)), '{}'),
			$3, $4, 'queued',
			$5, $6, $7, $8,
			now() + make_interval(secs => $9), $10::uuid)
		RETURNING id`,
		in.Name, strings.Join(in.Targets, sansSep), in.PortSpec, in.ExecutorScope,
		totalEndpoints, totalChunks, ratePPS, chunkEndpoints, etaSeconds, in.ActorID,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("store: insert scan sweep: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySweep,
		ActorID:    in.ActorID,
		TargetType: "scan_sweep",
		TargetID:   &id,
		RequestID:  in.RequestID,
		After: map[string]any{
			"phase":           "created",
			"targets":         in.Targets,
			"port_spec":       in.PortSpec,
			"executor_scope":  in.ExecutorScope,
			"total_endpoints": totalEndpoints,
			"total_chunks":    totalChunks,
			"rate_pps":        ratePPS,
			"chunk_endpoints": chunkEndpoints,
		},
		Result:   domain.AuditSuccess,
		SourceIP: in.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return s.GetSweep(ctx, id)
}

// ---------------------------------------------------------------------------
// 청크 발주
// ---------------------------------------------------------------------------

// SweepChunkFunc는 seq → (IP 목록, 포트 목록)의 순수 함수다.
// 같은 seq는 언제나 같은 집합을 돌려줘야 한다(재시도 멱등, 설계 5.2).
// store가 CIDR·포트 스펙을 전개할 수 없어(discovery 의존 불가) 호출자가 주입한다.
type SweepChunkFunc func(seq int) (ips []string, ports []int, ok bool)

// SweepDispatchInput은 청크 1건 발주에 필요한 실행 파라미터다.
type SweepDispatchInput struct {
	TimeoutMs   int
	Concurrency int
	Chunk       SweepChunkFunc
	RequestID   *string
}

// SweepChunk는 방금 발주된 청크 1건이다.
type SweepChunk struct {
	JobID     string
	Seq       int
	Attempt   int // 1이면 최초 발주, 2면 재시도
	Endpoints int
}

// DispatchNextChunk는 스윕의 다음 청크 1건을 자식 scan_jobs 행으로 발주한다.
// 발주할 것이 없으면 (nil, nil)이다 — 아래 경우가 전부 여기 해당한다.
//
//   - 스윕이 queued/running이 아니다 (paused·canceled·종료 상태)
//   - 진행 중(pending/leased/running) 자식 청크가 이미 있다 — **동시 청크는 최대 1개**
//   - 남은 청크가 없다 → 스윕을 completed로 전이한다
//   - 어떤 seq가 재시도 상한을 소진했다 → 스윕을 failed로 전이한다
//
// 전 과정이 단일 트랜잭션이며 스윕 행을 FOR UPDATE로 잠근다. 부분 유니크
// uq_scan_jobs_sweep_seq_active 충돌은 "이미 발주됨"이라는 정상 상황으로 흡수한다.
func (s *Store) DispatchNextChunk(ctx context.Context, sweepID string, in SweepDispatchInput) (*SweepChunk, error) {
	if in.Chunk == nil {
		return nil, fmt.Errorf("store: sweep dispatch requires a chunk function")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var (
		status, portSpec, executorScope   string
		nextSeq, totalChunks              int
		ratePPS, chunkEndpoints           int
		retrySeq, exhaustedSeq, retryFail sql.NullInt64
	)
	err = tx.QueryRowContext(ctx, `
		SELECT status, next_seq, total_chunks, rate_pps, chunk_endpoints, port_spec, executor_scope
		  FROM scan_sweeps WHERE id = $1::uuid FOR UPDATE`,
		sweepID,
	).Scan(&status, &nextSeq, &totalChunks, &ratePPS, &chunkEndpoints, &portSpec, &executorScope)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSweepNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: lock scan sweep: %w", err)
	}
	if status != SweepStatusQueued && status != SweepStatusRunning {
		return nil, nil // pause/cancel/종료 — 신규 발주만 멈춘다(진행 중 청크는 완주).
	}

	// 동시 청크는 최대 1개다.
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM scan_jobs
		 WHERE sweep_id = $1::uuid AND status IN ('pending', 'leased', 'running')`,
		sweepID).Scan(&active); err != nil {
		return nil, fmt.Errorf("store: count active sweep chunks: %w", err)
	}
	if active > 0 {
		return nil, nil
	}

	// seq별 시도 이력: live=0(살아있는 행 없음)인 seq만 재발주 후보다.
	// failed 행은 부분 유니크 인덱스 대상이 아니라 같은 seq로 다시 INSERT할 수 있다.
	if err := tx.QueryRowContext(ctx, `
		WITH g AS (
			SELECT sweep_seq AS seq,
			       count(*) FILTER (WHERE status =  'failed') AS failed,
			       count(*) FILTER (WHERE status <> 'failed') AS live
			  FROM scan_jobs
			 WHERE sweep_id = $1::uuid AND sweep_seq IS NOT NULL
			 GROUP BY sweep_seq)
		SELECT (SELECT min(seq) FROM g WHERE live = 0 AND failed >= $2),
		       (SELECT seq    FROM g WHERE live = 0 AND failed > 0 AND failed < $2 ORDER BY seq LIMIT 1),
		       (SELECT failed FROM g WHERE live = 0 AND failed > 0 AND failed < $2 ORDER BY seq LIMIT 1)`,
		sweepID, sweepChunkMaxAttempts,
	).Scan(&exhaustedSeq, &retrySeq, &retryFail); err != nil {
		return nil, fmt.Errorf("store: inspect sweep chunk attempts: %w", err)
	}

	switch {
	case exhaustedSeq.Valid:
		if err := failSweep(ctx, tx, sweepID, int(exhaustedSeq.Int64)); err != nil {
			return nil, err
		}
		return nil, commitSweepTx(tx)
	case !retrySeq.Valid && nextSeq >= totalChunks:
		if err := completeSweep(ctx, tx, sweepID); err != nil {
			return nil, err
		}
		return nil, commitSweepTx(tx)
	}

	seq, attempt := nextSeq, 1
	if retrySeq.Valid {
		seq, attempt = int(retrySeq.Int64), int(retryFail.Int64)+1
	}
	ips, ports, ok := in.Chunk(seq)
	if !ok || len(ips) == 0 || len(ports) == 0 {
		// total_chunks와 청크 함수가 어긋난 경우 — 발주할 것이 없으므로 종결한다.
		if err := completeSweep(ctx, tx, sweepID); err != nil {
			return nil, err
		}
		return nil, commitSweepTx(tx)
	}
	endpoints := len(ips) * len(ports)

	executorType := domain.ExecutorServer
	var sensorID *string
	if scoped, found := strings.CutPrefix(executorScope, sensorScopePrefix); found {
		executorType = domain.ExecutorSensor
		sensorID = &scoped
	}

	// 아비터는 0013이 만든 부분 유니크 인덱스와 정확히 같은 술어여야 추론된다.
	// 기존 아비터는 건드리지 않는다 (0010 사고 / GOTCHAS).
	//
	// origin은 반드시 명시한다 — 0015의 DEFAULT는 'manual'이고 백필도 기존 행만
	// 했으므로, 빼먹으면 앞으로 생기는 자식 청크가 전부 수동 스캔으로 기록된다.
	var jobID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO scan_jobs (
			targets, ports, timeout_ms, concurrency, status, executor_type, sensor_id,
			total_endpoints, sweep_id, sweep_seq, rate_pps, port_spec, origin)
		VALUES (
			coalesce(string_to_array(nullif($1, ''), chr(31)), '{}'),
			coalesce(string_to_array(nullif($2, ''), ','), '{}')::int[],
			$3, $4, 'pending', $5, $6::uuid,
			$7, $8::uuid, $9, $10, $11, 'sweep')
		ON CONFLICT (sweep_id, sweep_seq) WHERE sweep_id IS NOT NULL AND status <> 'failed'
		DO NOTHING
		RETURNING id`,
		strings.Join(ips, sansSep), joinInts(ports), in.TimeoutMs, in.Concurrency,
		string(executorType), sensorID, endpoints, sweepID, seq, ratePPS, portSpec,
	).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // 이미 발주됨 — 정상 상황으로 흡수한다.
	}
	if err != nil {
		return nil, fmt.Errorf("store: insert sweep chunk job: %w", err)
	}

	// next_seq는 발주 시점에만 전진한다. 재시도는 seq < next_seq라 GREATEST가 지킨다.
	if _, err := tx.ExecContext(ctx, `
		UPDATE scan_sweeps
		   SET status = 'running',
		       next_seq = GREATEST(next_seq, $2),
		       started_at = coalesce(started_at, now()),
		       updated_at = now()
		 WHERE id = $1::uuid`,
		sweepID, seq+1); err != nil {
		return nil, fmt.Errorf("store: advance sweep: %w", err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoveryScan,
		TargetType: "scan_job",
		TargetID:   &jobID,
		RequestID:  in.RequestID,
		After: map[string]any{
			"phase":           "created",
			"sweep_id":        sweepID,
			"sweep_seq":       seq,
			"attempt":         attempt,
			"targets":         ips,
			"port_spec":       portSpec,
			"rate_pps":        ratePPS,
			"executor_type":   string(executorType),
			"total_endpoints": endpoints,
		},
		Result: domain.AuditSuccess,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return &SweepChunk{JobID: jobID, Seq: seq, Attempt: attempt, Endpoints: endpoints}, nil
}

// completeSweep은 모든 청크가 소진된 스윕을 completed로 종결한다.
func completeSweep(ctx context.Context, tx *sql.Tx, sweepID string) error {
	return finishSweep(ctx, tx, sweepID, SweepStatusCompleted, nil, domain.AuditSuccess)
}

// failSweep은 재시도 상한을 소진한 청크 때문에 진행 불가가 된 스윕을 failed로 종결한다.
// 마지막 청크 오류를 스윕에 남긴다 — 인증서·대상 본문은 담지 않는다 (보안 하드 규칙 3).
func failSweep(ctx context.Context, tx *sql.Tx, sweepID string, seq int) error {
	var jobErr sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT error FROM scan_jobs
		 WHERE sweep_id = $1::uuid AND sweep_seq = $2 AND status = 'failed'
		 ORDER BY finished_at DESC NULLS LAST
		 LIMIT 1`,
		sweepID, seq).Scan(&jobErr); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: read sweep chunk error: %w", err)
	}
	reason := fmt.Sprintf("chunk %d failed %d times", seq, sweepChunkMaxAttempts)
	if jobErr.Valid && jobErr.String != "" {
		reason += ": " + jobErr.String
	}
	return finishSweep(ctx, tx, sweepID, SweepStatusFailed, &reason, domain.AuditFailure)
}

// finishSweep은 스윕을 종료 상태로 전이하고 종료 감사 이벤트를 남긴다.
func finishSweep(ctx context.Context, tx *sql.Tx, sweepID, status string, reason *string, result domain.AuditResult) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE scan_sweeps
		   SET status = $2, finished_at = now(), stalled_since = NULL,
		       last_error = coalesce($3, last_error), updated_at = now()
		 WHERE id = $1::uuid AND status IN ('queued', 'running')`,
		sweepID, status, reason)
	if err != nil {
		return fmt.Errorf("store: finish scan sweep: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: rows affected: %w", err)
	} else if n == 0 {
		return nil // 이미 다른 경로가 종결했다.
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	after := map[string]any{"phase": "finished", "status": status}
	if reason != nil {
		after["reason"] = *reason
	}
	return audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySweep,
		TargetType: "scan_sweep",
		TargetID:   &sweepID,
		After:      after,
		Result:     result,
	})
}

// commitSweepTx는 "발주 없이 상태만 바꾸고 끝"인 경로의 커밋이다.
func commitSweepTx(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 상태 전이 (pause / resume / cancel)
// ---------------------------------------------------------------------------

// sweepTransition은 전이 1개의 허용 출발 상태와 SET 절이다.
// SET 절은 전부 코드 상수다 — 사용자 입력이 SQL로 들어가지 않는다.
type sweepTransition struct {
	from []string
	set  string
}

// 세 전이는 모두 **발주자에 대한 제어**다. 진행 중인 청크 Job에는 관여하지 않는다
// (취소를 센서까지 전파하지 않는다 — 설계 6.2, 센서 프로토콜 변경 불필요).
var sweepTransitions = map[string]sweepTransition{
	"pause": {
		from: []string{SweepStatusQueued, SweepStatusRunning},
		set:  "status = 'paused'",
	},
	"resume": {
		from: []string{SweepStatusPaused},
		set:  "status = CASE WHEN started_at IS NULL THEN 'queued' ELSE 'running' END",
	},
	"cancel": {
		from: []string{SweepStatusQueued, SweepStatusRunning, SweepStatusPaused},
		set:  "status = 'canceled', finished_at = now(), stalled_since = NULL",
	},
}

// PauseSweep은 신규 청크 발주만 멈춘다. 이미 나간 청크는 완주한다.
func (s *Store) PauseSweep(ctx context.Context, sweepID string, act SweepActor) (*Sweep, error) {
	return s.transitionSweep(ctx, sweepID, "pause", act)
}

// ResumeSweep은 next_seq부터 발주를 재개한다.
func (s *Store) ResumeSweep(ctx context.Context, sweepID string, act SweepActor) (*Sweep, error) {
	return s.transitionSweep(ctx, sweepID, "resume", act)
}

// CancelSweep은 발주를 영구 중단한다. 진행 중 청크의 결과는 정상 반영된다.
func (s *Store) CancelSweep(ctx context.Context, sweepID string, act SweepActor) (*Sweep, error) {
	return s.transitionSweep(ctx, sweepID, "cancel", act)
}

// transitionSweep은 상태 전이 + 감사를 한 트랜잭션으로 수행한다.
func (s *Store) transitionSweep(ctx context.Context, sweepID, action string, act SweepActor) (*Sweep, error) {
	t, ok := sweepTransitions[action]
	if !ok {
		return nil, fmt.Errorf("%w: unknown sweep action %q", ErrSweepState, action)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var before string
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM scan_sweeps WHERE id = $1::uuid FOR UPDATE`, sweepID).Scan(&before)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSweepNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: lock scan sweep: %w", err)
	}
	if !containsString(t.from, before) {
		return nil, fmt.Errorf("%w: sweep is %s, cannot %s", ErrSweepState, before, action)
	}

	var after string
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(
		`UPDATE scan_sweeps SET %s, updated_at = now() WHERE id = $1::uuid RETURNING status`, t.set),
		sweepID).Scan(&after); err != nil {
		return nil, fmt.Errorf("store: %s scan sweep: %w", action, err)
	}

	// 감사 삽입은 트랜잭션의 마지막 문장이어야 한다 (docs/GOTCHAS.md).
	if err := audit.Insert(ctx, tx, audit.Event{
		Type:       EventDiscoverySweep,
		ActorID:    act.ActorID,
		TargetType: "scan_sweep",
		TargetID:   &sweepID,
		RequestID:  act.RequestID,
		Before:     map[string]any{"status": before},
		After:      map[string]any{"phase": action, "status": after},
		Result:     domain.AuditSuccess,
		SourceIP:   act.SourceIP,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return s.GetSweep(ctx, sweepID)
}

// ---------------------------------------------------------------------------
// 센서 대기(stalled) 감지
// ---------------------------------------------------------------------------

// RefreshSweepStalls는 센서 스코프 스윕의 stalled_since를 갱신한다.
// 자식 청크가 stallAfter를 넘겨 pending에 머물면 세팅하고, 집히면 해제한다.
// 갱신한 스윕 수를 반환한다.
//
// **신규 종료 상태를 만들지 않는다** — 스윕은 계속 running이고 UI가 이 값을
// 근거로 "센서 대기"를 표시할 뿐이다 (설계 6.4, /discovery 대기 배지와 같은 표현).
func (s *Store) RefreshSweepStalls(ctx context.Context, stallAfter time.Duration) (int, error) {
	if stallAfter <= 0 {
		stallAfter = DefaultSweepStallAfter
	}
	secs := stallAfter.Seconds()

	set, err := s.db.ExecContext(ctx, `
		UPDATE scan_sweeps sw
		   SET stalled_since = p.oldest, updated_at = now()
		  FROM (SELECT sweep_id, min(created_at) AS oldest
		          FROM scan_jobs
		         WHERE sweep_id IS NOT NULL AND status = 'pending'
		           AND created_at < now() - make_interval(secs => $1)
		         GROUP BY sweep_id) p
		 WHERE sw.id = p.sweep_id
		   AND sw.stalled_since IS NULL
		   AND sw.status IN ('queued', 'running')
		   AND starts_with(sw.executor_scope, $2)`,
		secs, sensorScopePrefix)
	if err != nil {
		return 0, fmt.Errorf("store: mark stalled sweeps: %w", err)
	}

	cleared, err := s.db.ExecContext(ctx, `
		UPDATE scan_sweeps sw
		   SET stalled_since = NULL, updated_at = now()
		 WHERE sw.stalled_since IS NOT NULL
		   AND NOT EXISTS (
		       SELECT 1 FROM scan_jobs j
		        WHERE j.sweep_id = sw.id AND j.status = 'pending'
		          AND j.created_at < now() - make_interval(secs => $1))`,
		secs)
	if err != nil {
		return 0, fmt.Errorf("store: clear stalled sweeps: %w", err)
	}

	n1, err := set.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: rows affected: %w", err)
	}
	n2, err := cleared.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: rows affected: %w", err)
	}
	return int(n1 + n2), nil
}

// ---------------------------------------------------------------------------
// 조회
// ---------------------------------------------------------------------------

// sweepColumns는 Sweep 스캔 순서와 1:1로 맞춘 컬럼 목록이다.
const sweepColumns = `sw.id, sw.name, sw.status,
	array_to_string(sw.targets, chr(31)), sw.port_spec, sw.executor_scope,
	sw.total_endpoints, sw.total_chunks, sw.next_seq, sw.rate_pps, sw.chunk_endpoints,
	sw.estimated_finish_at, sw.stalled_since, sw.last_error, sw.created_by::text,
	sw.created_at, sw.updated_at, sw.started_at, sw.finished_at`

// sweepProgressColumns/sweepProgressJoin은 진행률을 자식 scan_jobs 집계로
// **매번 파생**한다 (설계 6.1). 부모에 카운터를 두지 않는 이유는 0011 이중 계상 사고다.
// 엔드포인트 합산에서 failed 행을 빼는 이유: 재시도된 청크의 부분 진행이 두 번
// 세어지면 total_endpoints를 넘어선다.
const sweepProgressColumns = `coalesce(p.chunks_completed, 0), coalesce(p.chunks_active, 0),
	coalesce(p.failed_attempts, 0), coalesce(p.scanned, 0), coalesce(p.reachable, 0),
	coalesce(p.found, 0), coalesce(p.new_certs, 0)`

const sweepProgressJoin = `
	LEFT JOIN LATERAL (
		SELECT count(DISTINCT j.sweep_seq) FILTER (WHERE j.status = 'completed')                     AS chunks_completed,
		       count(DISTINCT j.sweep_seq) FILTER (WHERE j.status IN ('pending', 'leased', 'running')) AS chunks_active,
		       count(*)                    FILTER (WHERE j.status = 'failed')                        AS failed_attempts,
		       coalesce(sum(j.scanned_endpoints)   FILTER (WHERE j.status <> 'failed'), 0) AS scanned,
		       coalesce(sum(j.reachable_endpoints) FILTER (WHERE j.status <> 'failed'), 0) AS reachable,
		       coalesce(sum(j.certificates_found)  FILTER (WHERE j.status <> 'failed'), 0) AS found,
		       coalesce(sum(j.certificates_new)    FILTER (WHERE j.status <> 'failed'), 0) AS new_certs
		  FROM scan_jobs j WHERE j.sweep_id = sw.id) p ON true`

// scanSweep은 sweepColumns + sweepProgressColumns 순서의 1행을 Sweep으로 읽는다.
func scanSweep(sc rowScanner) (Sweep, error) {
	var (
		sw         Sweep
		targetsStr string
	)
	err := sc.Scan(&sw.ID, &sw.Name, &sw.Status, &targetsStr, &sw.PortSpec, &sw.ExecutorScope,
		&sw.TotalEndpoints, &sw.TotalChunks, &sw.NextSeq, &sw.RatePPS, &sw.ChunkEndpoints,
		&sw.EstimatedFinishAt, &sw.StalledSince, &sw.LastError, &sw.CreatedBy,
		&sw.CreatedAt, &sw.UpdatedAt, &sw.StartedAt, &sw.FinishedAt,
		&sw.Progress.ChunksCompleted, &sw.Progress.ChunksActive, &sw.Progress.FailedAttempts,
		&sw.Progress.ScannedEndpoints, &sw.Progress.ReachableEndpoints,
		&sw.Progress.CertificatesFound, &sw.Progress.CertificatesNew)
	if err != nil {
		return Sweep{}, err
	}
	sw.Targets = splitNonEmpty(targetsStr, sansSep)
	sw.Progress.ChunksTotal = sw.TotalChunks
	return sw, nil
}

// GetSweep은 스윕 1건을 진행률 파생값과 함께 조회한다. 없으면 ErrSweepNotFound.
// 잘못된 형식의 id는 존재하지 않는 리소스로 취급한다 (uuid 캐스트 오류 방지).
func (s *Store) GetSweep(ctx context.Context, sweepID string) (*Sweep, error) {
	id := strings.ToLower(strings.TrimSpace(sweepID))
	if !uuidRe.MatchString(id) {
		return nil, ErrSweepNotFound
	}
	row := s.db.QueryRowContext(ctx,
		"SELECT "+sweepColumns+", "+sweepProgressColumns+
			" FROM scan_sweeps sw"+sweepProgressJoin+" WHERE sw.id = $1::uuid", id)
	sw, err := scanSweep(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSweepNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get scan sweep: %w", err)
	}
	return &sw, nil
}

// ListSweepsInput은 스윕 목록 조회 조건이다.
type ListSweepsInput struct {
	Statuses []string // 비면 전체
	Page     int      // 1부터
	PageSize int      // 1..maxPageSize
}

// SweepList는 목록 조회 결과다.
type SweepList struct {
	Items    []Sweep
	Total    int
	Page     int
	PageSize int
}

// ListSweeps는 상태 필터 + 최신순 페이지네이션으로 스윕 목록을 조회한다.
// 조회 전용이라 감사 이벤트를 남기지 않는다 (00-spec 7.12 기록 대상에 조회 없음).
func (s *Store) ListSweeps(ctx context.Context, in ListSweepsInput) (*SweepList, error) {
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

	where := ""
	var args []any
	if len(in.Statuses) > 0 {
		where = " WHERE sw.status = ANY(string_to_array($1, chr(31)))"
		args = append(args, strings.Join(in.Statuses, sansSep))
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM scan_sweeps sw"+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("store: count scan sweeps: %w", err)
	}

	// id 보조 정렬로 페이지 경계를 안정화한다.
	query := fmt.Sprintf("SELECT %s, %s FROM scan_sweeps sw%s%s ORDER BY sw.created_at DESC, sw.id ASC LIMIT $%d OFFSET $%d",
		sweepColumns, sweepProgressColumns, sweepProgressJoin, where, len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, fmt.Errorf("store: list scan sweeps: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	items := []Sweep{} // 빈 결과도 JSON []로 나가도록 nil이 아닌 슬라이스
	for rows.Next() {
		sw, err := scanSweep(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan scan sweep: %w", err)
		}
		items = append(items, sw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate scan sweeps: %w", err)
	}
	return &SweepList{Items: items, Total: total, Page: page, PageSize: size}, nil
}

// containsString은 문자열 슬라이스 포함 여부다(전이 허용 상태 판정용).
func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
