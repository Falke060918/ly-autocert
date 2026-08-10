// adminip.go — 관리 콘솔 접근 IP 제한 (마이그레이션 0014, store/adminip.go).
//
// 잠금 방지 설계 4단:
//  1. 활성 규칙 0건 = 제한 없음. 기본 설치는 절대 잠기지 않는다.
//  2. 규칙 변경으로 요청자 자신이 차단되면 409로 거부한다 (판정은 store가
//     변경과 같은 트랜잭션 안에서 한다 — 동시 요청이 서로를 잠글 수 없다).
//  3. 비상 우회 대역(ADMIN_IP_ALLOW_BOOTSTRAP)은 규칙과 무관하게 통과한다.
//     화면에서 잠갔을 때의 복구 경로이며, 비어 있으면 적용되지 않는다.
//  4. 판정은 캐시로 하고 변경 시 즉시 무효화한다 — 재기동 없이 반영되면서
//     매 요청 DB 조회는 하지 않는다. TTL은 캐시가 어긋났을 때의 안전망이다.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
)

// maxAdminIPRequestBytes는 접근 IP 규칙 API 요청 본문 상한이다.
const maxAdminIPRequestBytes = 1 << 20 // 1 MiB

// maxAdminIPDescriptionLen은 설명 메모 상한이다 (0014 CHECK와 같은 값).
const maxAdminIPDescriptionLen = 200

// adminIPCacheTTL은 캐시 스냅샷의 안전망 수명이다. 정상 경로에서는 변경 API가
// 즉시 무효화하므로 이 값이 반영 지연이 되지 않는다 — DB를 밖에서 직접 고쳤거나
// 인스턴스가 여러 개인 경우에 대비한 상한일 뿐이다.
const adminIPCacheTTL = 30 * time.Second

// adminIPReloadTimeout은 캐시 적재 쿼리 상한이다.
const adminIPReloadTimeout = 3 * time.Second

// AdminIPAccess는 접근 IP 제한의 환경변수 유래 설정이다 (cmd/server가 주입한다).
// 둘 다 비어 있어도 기능은 정상 동작한다 — 프록시를 신뢰하지 않고(직접 접속 IP만 사용),
// 비상 우회도 적용하지 않는다.
type AdminIPAccess struct {
	// TrustedProxies는 X-Forwarded-For를 신뢰할 직접 접속 대역이다
	// (ADMIN_IP_TRUSTED_PROXIES). 비면 XFF를 아예 보지 않는다.
	TrustedProxies []*net.IPNet
	// Bootstrap은 규칙과 무관하게 통과시키는 비상 우회 대역이다
	// (ADMIN_IP_ALLOW_BOOTSTRAP). 비면 적용되지 않는다.
	Bootstrap []*net.IPNet
}

// WithAdminIPAccess는 접근 IP 제한 설정을 주입한다. 주입하지 않으면 직접 접속
// IP만 보고(XFF 무시) 비상 우회 대역도 없다 — 규칙 목록만으로 동작한다.
func WithAdminIPAccess(a AdminIPAccess) Option {
	return func(s *Server) { s.adminIP.access = a }
}

// adminIPGuard는 허용 대역 스냅샷을 들고 요청을 판정한다.
type adminIPGuard struct {
	store  *store.Store
	access AdminIPAccess

	// reloadMu는 동시에 만료된 요청들이 DB 조회를 몰아치지 않게 한다.
	reloadMu sync.Mutex
	// mu는 스냅샷 읽기 전용이다 — DB 조회 중에는 잡지 않는다.
	mu       sync.RWMutex
	nets     []*net.IPNet
	loaded   bool
	loadedAt time.Time
}

func newAdminIPGuard(st *store.Store) *adminIPGuard {
	return &adminIPGuard{store: st}
}

// invalidate는 다음 요청이 DB에서 다시 읽도록 캐시를 버린다.
// 변경 API 3종이 성공한 직후에 호출한다 — 재기동 없이 즉시 반영되는 지점이다.
func (g *adminIPGuard) invalidate() {
	g.mu.Lock()
	g.loaded = false
	g.mu.Unlock()
}

// snapshot은 활성 허용 대역을 반환한다. 빈 슬라이스는 "제한 없음"이다.
func (g *adminIPGuard) snapshot(ctx context.Context) ([]*net.IPNet, error) {
	g.mu.RLock()
	if g.loaded && time.Since(g.loadedAt) < adminIPCacheTTL {
		nets := g.nets
		g.mu.RUnlock()
		return nets, nil
	}
	g.mu.RUnlock()

	g.reloadMu.Lock()
	defer g.reloadMu.Unlock()
	// 기다리는 동안 다른 요청이 이미 갱신했을 수 있다.
	g.mu.RLock()
	fresh, stale, hadStale := g.loaded && time.Since(g.loadedAt) < adminIPCacheTTL, g.nets, g.loaded
	nets := g.nets
	g.mu.RUnlock()
	if fresh {
		return nets, nil
	}

	loadCtx, cancel := context.WithTimeout(ctx, adminIPReloadTimeout)
	defer cancel()
	cidrs, err := g.store.ActiveAdminIPCIDRs(loadCtx)
	if err != nil {
		// 마지막으로 읽은 스냅샷이 있으면 그것으로 계속 판정한다 — DB 순단이
		// 곧바로 전면 차단(또는 전면 허용)으로 번지지 않게 한다.
		g.mu.Lock()
		g.loadedAt = time.Now() // 실패해도 재조회 폭주는 막는다
		g.mu.Unlock()
		if hadStale {
			slog.Warn("admin ip rules reload failed; serving cached snapshot", "error", err.Error())
			return stale, nil
		}
		return nil, err
	}

	parsed := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			// DB가 cidr 타입으로 강제하므로 정상 경로에서는 나올 수 없다.
			slog.Error("admin ip rule is not a valid cidr; skipping", "error", err.Error())
			continue
		}
		parsed = append(parsed, n)
	}
	g.mu.Lock()
	g.nets, g.loaded, g.loadedAt = parsed, true, time.Now()
	g.mu.Unlock()
	return parsed, nil
}

// containsIP는 대역 목록 중 하나라도 ip를 포함하는지 본다.
func containsIP(nets []*net.IPNet, ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// bootstrapAllows는 비상 우회 대역으로 들어온 요청인지 본다.
func (g *adminIPGuard) bootstrapAllows(ip net.IP) bool {
	return containsIP(g.access.Bootstrap, ip)
}

// allow는 이 출발지 IP가 통과해도 되는지 판정한다.
// ip가 nil(판정 불가)이면 제한이 걸린 서버에서는 통과시키지 않는다 — 확인할 수 없는
// 출발지를 허용하면 제한이 사실상 무력화된다.
func (g *adminIPGuard) allow(ctx context.Context, ip net.IP) (bool, error) {
	if g.bootstrapAllows(ip) {
		return true, nil
	}
	nets, err := g.snapshot(ctx)
	if err != nil {
		return false, err
	}
	if len(nets) == 0 {
		return true, nil
	}
	return containsIP(nets, ip), nil
}

// resolveClientIP는 요청 출발지 IP를 판정한다.
//
// 기본은 직접 접속 IP(RemoteAddr)다. X-Forwarded-For는 **직접 접속한 상대가
// 신뢰 프록시 대역일 때만** 본다 — 아무 요청이나 헤더 한 줄로 출발지를 위조할 수
// 있으면 이 기능 전체가 무의미하다. 신뢰 프록시일 때는 XFF를 오른쪽(가장 나중에
// 붙은 값)부터 훑어 신뢰 프록시가 아닌 첫 값을 쓴다.
func (g *adminIPGuard) resolveClientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || !containsIP(g.access.TrustedProxies, ip) {
		return ip
	}
	hops := forwardedForChain(r)
	for i := len(hops) - 1; i >= 0; i-- {
		cand := net.ParseIP(hops[i])
		if cand == nil {
			// 위조·손상된 값이 섞여 있으면 판정 불가로 본다(허용으로 낙하시키지 않는다).
			return nil
		}
		if !containsIP(g.access.TrustedProxies, cand) {
			return cand
		}
	}
	return ip
}

// forwardedForChain은 X-Forwarded-For 헤더 전체를 왼쪽→오른쪽 순서로 편다.
// 헤더가 여러 번 오는 경우도 하나로 잇는다 (RFC 7230 필드 병합 규약).
func forwardedForChain(r *http.Request) []string {
	var hops []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(header, ",") {
			if v := strings.TrimSpace(part); v != "" {
				hops = append(hops, v)
			}
		}
	}
	return hops
}

// withAdminIPAllow는 출발지 IP 허용 목록 미들웨어다. 인증보다 앞에 있으므로
// 차단된 IP는 로그인 화면조차 받지 못한다 — 그것이 이 기능의 목적이다.
//
// 차단은 감사 이벤트를 남기지 않는다: 감사 해시 체인은 advisory lock으로
// 직렬화되므로, 스캐너 한 대가 포트를 두드리면 로그인·업로드까지 함께 느려진다
// (알림 발송 결과를 감사에서 뺀 것과 같은 근거). 대신 경고 로그로 남긴다.
func (s *Server) withAdminIPAllow(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.adminIP.resolveClientIP(r)
		if ip != nil {
			r = r.WithContext(context.WithValue(r.Context(), ctxKeyClientIP, ip.String()))
		}
		// 헬스체크는 오케스트레이터·로드밸런서가 부른다 — 제한 대상이 아니다.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		allowed, err := s.adminIP.allow(r.Context(), ip)
		if err != nil {
			slog.Error("admin ip allow-list check failed", "error", err.Error())
			writeError(w, r, http.StatusServiceUnavailable, "unavailable", "access control is temporarily unavailable")
			return
		}
		if !allowed {
			slog.Warn("request blocked by admin ip allow-list", "source_ip", clientIPString(r), "path", r.URL.Path)
			writeError(w, r, http.StatusForbidden, "ip_not_allowed", "access from this source IP is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIPString은 로그용 출발지 표기다. 판정 불가면 "unknown"이다.
func clientIPString(r *http.Request) string {
	if ip := clientIP(r); ip != nil {
		return *ip
	}
	return "unknown"
}

// ===== API =====

type adminIPRuleListResponse struct {
	RequestID string               `json:"request_id,omitempty"`
	Items     []domain.AdminIPRule `json:"items"`
	Total     int                  `json:"total"`
	Access    adminIPAccessState   `json:"access"`
}

// adminIPAccessState는 운영자가 "지금 나를 막게 되는가"를 판단하는 데 필요한 상태다.
// 설정값(대역 문자열)은 담지 않고 설정 여부만 담는다 — 알림 채널 상태와 같은 규약이다.
type adminIPAccessState struct {
	// ClientIP는 **서버가 본** 이 요청의 출발지다. 화면이 이 값을 그대로 보여줘야
	// 운영자가 자기 IP를 착각해 스스로를 막는 일이 없다. 판정 불가면 빈 문자열.
	ClientIP string `json:"client_ip"`
	// Enforced는 활성 규칙이 1건 이상이라 제한이 실제로 걸려 있는 상태다.
	Enforced bool `json:"enforced"`
	// BootstrapConfigured는 비상 우회 대역이 설정돼 있는지다.
	BootstrapConfigured bool `json:"bootstrap_configured"`
	// ClientViaBootstrap은 이 요청이 비상 우회 대역으로 들어왔는지다
	// (= 지금은 잠금 방지 판정이 적용되지 않는다).
	ClientViaBootstrap bool `json:"client_via_bootstrap"`
	// TrustedProxyConfigured는 XFF를 신뢰할 프록시 대역이 설정돼 있는지다.
	// false인데 화면의 client_ip가 프론트엔드 컨테이너 주소로 보인다면 이 값이 원인이다.
	TrustedProxyConfigured bool `json:"trusted_proxy_configured"`
}

// handleListAdminIPRules는 GET /api/v1/admin-ip-rules다 (System Administrator 전용).
func (s *Server) handleListAdminIPRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListAdminIPRules(r.Context())
	if err != nil {
		slog.Error("list admin ip rules failed", "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	enforced := false
	for _, rule := range rules {
		if rule.IsActive {
			enforced = true
			break
		}
	}
	resp := adminIPRuleListResponse{
		Items: rules,
		Total: len(rules),
		Access: adminIPAccessState{
			Enforced:               enforced,
			BootstrapConfigured:    len(s.adminIP.access.Bootstrap) > 0,
			ClientViaBootstrap:     s.adminIP.bootstrapAllows(requestClientIP(r)),
			TrustedProxyConfigured: len(s.adminIP.access.TrustedProxies) > 0,
		},
	}
	if ip := clientIP(r); ip != nil {
		resp.Access.ClientIP = *ip
	}
	if id := requestIDFrom(r.Context()); id != nil {
		resp.RequestID = *id
	}
	writeJSON(w, http.StatusOK, resp)
}

type createAdminIPRuleRequest struct {
	CIDR        string  `json:"cidr"`
	Description *string `json:"description"`
	IsActive    *bool   `json:"is_active"`
}

type updateAdminIPRuleRequest struct {
	IsActive *bool `json:"is_active"`
}

// decodeAdminIPRequest는 접근 IP 규칙 본문을 디코드한다. 모르는 필드는 거부한다 —
// 조용히 버리면 "201인데 값이 안 들어갔다"는 오진이 난다 (users.email 전례).
func decodeAdminIPRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminIPRequestBytes))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// normalizeAdminIPCIDR은 입력 대역을 DB 저장 표기로 정규화한다.
// 단일 IP는 /32(IPv6는 /128)로 올리고, 호스트 비트가 켜진 CIDR은 **조용히 깎지 않고**
// 거부한다 — 192.168.1.5/24를 넣은 사람이 원한 것이 "그 호스트"인지 "그 대역"인지는
// 알 수 없고, 어느 쪽으로 추측해도 접근 범위가 틀린다.
func normalizeAdminIPCIDR(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errors.New("IP 또는 CIDR 대역이 필요합니다")
	}
	if !strings.Contains(v, "/") {
		ip := net.ParseIP(v)
		if ip == nil {
			return "", fmt.Errorf("%q는 IP 주소나 CIDR 대역이 아닙니다", v)
		}
		if ip.To4() != nil {
			return ip.String() + "/32", nil
		}
		return ip.String() + "/128", nil
	}
	ip, network, err := net.ParseCIDR(v)
	if err != nil {
		return "", fmt.Errorf("%q는 IP 주소나 CIDR 대역이 아닙니다", v)
	}
	if !ip.Equal(network.IP) {
		return "", fmt.Errorf("%q는 호스트 비트가 켜져 있습니다 — 대역이면 %s, 단일 호스트면 %s로 입력하십시오",
			v, network.String(), singleHostCIDR(ip))
	}
	return network.String(), nil
}

// singleHostCIDR은 단일 호스트 표기(/32·/128)를 만든다.
func singleHostCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

// handleCreateAdminIPRule은 POST /api/v1/admin-ip-rules다 (System Administrator 전용).
// admin_ip_rule.create 감사 이벤트를 store가 같은 트랜잭션으로 남긴다.
func (s *Server) handleCreateAdminIPRule(w http.ResponseWriter, r *http.Request) {
	var req createAdminIPRuleRequest
	if err := decodeAdminIPRequest(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	cidr, err := normalizeAdminIPCIDR(req.CIDR)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_cidr", err.Error())
		return
	}
	description := ""
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
	}
	if len([]rune(description)) > maxAdminIPDescriptionLen {
		writeError(w, r, http.StatusBadRequest, "invalid_description",
			fmt.Sprintf("설명은 %d자 이하여야 합니다", maxAdminIPDescriptionLen))
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}

	mutation, ok := s.adminIPMutation(w, r)
	if !ok {
		return
	}
	rule, err := s.store.CreateAdminIPRule(r.Context(), store.CreateAdminIPRuleInput{
		CIDR:                cidr,
		Description:         description,
		IsActive:            active,
		AdminIPRuleMutation: mutation,
	})
	if s.writeAdminIPMutationError(w, r, err, "create admin ip rule failed") {
		return
	}
	s.adminIP.invalidate()
	writeJSON(w, http.StatusCreated, rule)
}

// handleUpdateAdminIPRule은 PATCH /api/v1/admin-ip-rules/{id}다 (활성 토글).
func (s *Server) handleUpdateAdminIPRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "admin ip rule not found")
		return
	}
	var req updateAdminIPRuleRequest
	if err := decodeAdminIPRequest(w, r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	if req.IsActive == nil {
		writeError(w, r, http.StatusBadRequest, "no_fields", "is_active is required")
		return
	}
	mutation, ok := s.adminIPMutation(w, r)
	if !ok {
		return
	}
	rule, err := s.store.SetAdminIPRuleActive(r.Context(), id, *req.IsActive, mutation)
	if s.writeAdminIPMutationError(w, r, err, "update admin ip rule failed") {
		return
	}
	s.adminIP.invalidate()
	writeJSON(w, http.StatusOK, rule)
}

// handleDeleteAdminIPRule은 DELETE /api/v1/admin-ip-rules/{id}다.
func (s *Server) handleDeleteAdminIPRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !isUUID(id) {
		writeError(w, r, http.StatusNotFound, "not_found", "admin ip rule not found")
		return
	}
	mutation, ok := s.adminIPMutation(w, r)
	if !ok {
		return
	}
	if s.writeAdminIPMutationError(w, r,
		s.store.DeleteAdminIPRule(r.Context(), id, mutation), "delete admin ip rule failed") {
		return
	}
	s.adminIP.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// adminIPMutation은 변경 요청의 공통 메타(감사 + 잠금 방지 기준 IP)를 만든다.
//
// ActorIP는 잠금 방지 판정의 기준이다. 비상 우회 대역으로 들어온 요청만 nil이며,
// 그때는 "규칙에서 제외된 상태로도 접근할 수 있는 복구 중"이므로 판정을 건너뛴다.
// 출발지를 판정할 수 없으면 변경 자체를 막는다 — 검증할 수 없는 채로 규칙을 바꾸면
// 그 한 번으로 잠길 수 있다.
func (s *Server) adminIPMutation(w http.ResponseWriter, r *http.Request) (store.AdminIPRuleMutation, bool) {
	actor := userFrom(r.Context())
	m := store.AdminIPRuleMutation{
		RequestID: requestIDFrom(r.Context()),
		SourceIP:  clientIP(r),
	}
	if actor != nil {
		m.ActorID = &actor.ID
	}
	ip := requestClientIP(r)
	if s.adminIP.bootstrapAllows(ip) {
		return m, true
	}
	if ip == nil {
		writeError(w, r, http.StatusConflict, "client_ip_unknown",
			"cannot verify the requesting client IP; refusing to change the allow-list")
		return m, false
	}
	m.ActorIP = ptr(ip.String())
	return m, true
}

// requestClientIP는 미들웨어가 판정해 컨텍스트에 넣어둔 출발지 IP다.
func requestClientIP(r *http.Request) net.IP {
	if ip := clientIP(r); ip != nil {
		return net.ParseIP(*ip)
	}
	return nil
}

// writeAdminIPMutationError는 규칙 변경 API의 공통 오류 매핑이다.
// 처리했으면(=오류였으면) true를 반환한다.
func (s *Server) writeAdminIPMutationError(w http.ResponseWriter, r *http.Request, err error, logMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrAdminIPRuleNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "admin ip rule not found")
	case errors.Is(err, store.ErrAdminIPRuleExists):
		writeError(w, r, http.StatusConflict, "cidr_taken", "this IP or range is already registered")
	case errors.Is(err, store.ErrAdminIPSelfLockout):
		// 잠금 방지의 최종 방어선이다. 어떤 IP가 막히는지 알려줘야 고칠 수 있다.
		writeError(w, r, http.StatusConflict, "self_lockout_forbidden",
			fmt.Sprintf("this change would block your own address (%s); add a rule covering it first",
				clientIPString(r)))
	default:
		slog.Error(logMsg, "error", err.Error())
		writeError(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
	return true
}
