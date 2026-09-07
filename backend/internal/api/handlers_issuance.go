package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/agentwire"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/domain"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/issuance/upstream"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/sensorca"
	"github.com/Falke060918/INHERIT_AutoCert/backend/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
)

func WithIssuance(v *issuance.Service) Option { return func(s *Server) { s.issuance = v } }
func (s *Server) registerIssuanceRoutes(caKey []byte) {
	if s.issuance == nil {
		s.issuance = issuance.New(s.store, nil)
	}
	managers := []domain.Role{domain.RoleSystemAdministrator, domain.RolePKIAdministrator}
	for _, route := range []struct {
		p string
		h http.HandlerFunc
	}{
		{"GET /api/v1/ca-connections", s.listCA}, {"POST /api/v1/ca-connections", s.saveCA}, {"PATCH /api/v1/ca-connections/{id}", s.saveCA}, {"DELETE /api/v1/ca-connections/{id}", s.deleteCA}, {"POST /api/v1/ca-connections/{id}/test", s.testCA}, {"GET /api/v1/ca-connections/{id}/products", s.productsCA},
		{"GET /api/v1/issuance/settings", s.issuanceSettings}, {"PATCH /api/v1/issuance/settings", s.issuanceSettings}, {"POST /api/v1/issuance/orders", s.createOrder}, {"POST /api/v1/issuance/orders/{id}/cancel", s.cancelOrder}, {"POST /api/v1/issuance/orders/{id}/retry", s.retryOrder},
	} {
		s.protected(route.p, route.h, managers...)
	}
	s.protected("GET /api/v1/issuance/orders", s.listOrders, domain.AllRoles...)
	s.protected("GET /api/v1/issuance/orders/{id}", s.getOrder, domain.AllRoles...)
	s.protected("POST /api/v1/issuance/orders/{id}/approve", s.approveOrder, domain.RoleSystemAdministrator, domain.RolePKIAdministrator, domain.RoleSecurityAdministrator)
	for _, route := range []struct {
		p string
		h http.HandlerFunc
	}{
		{"GET /api/v1/agents", s.listAgents}, {"POST /api/v1/agents/tokens", s.createAgent(caKey)}, {"PATCH /api/v1/agents/{id}", s.disableAgent},
		{"GET /api/v1/deployment/targets", s.listTargets}, {"POST /api/v1/deployment/targets", s.saveTarget}, {"PATCH /api/v1/deployment/targets/{id}", s.saveTarget}, {"DELETE /api/v1/deployment/targets/{id}", s.deleteTarget},
		{"POST /api/v1/issuance/orders/{id}/deploy", s.deployOrder}, {"GET /api/v1/deployment/jobs", s.listDeploymentJobs},
	} {
		s.protected(route.p, route.h, domain.RoleSystemAdministrator)
	}
}
func issuanceActor(r *http.Request) store.IssuanceActor {
	a := store.IssuanceActor{RequestID: requestIDFrom(r.Context()), SourceIP: clientIP(r)}
	if u := userFrom(r.Context()); u != nil {
		a.ID = &u.ID
	}
	return a
}
func issuanceJSON(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
	data["request_id"] = requestIDFrom(r.Context())
	writeJSON(w, status, data)
}
func issuanceError(w http.ResponseWriter, r *http.Request, e error) {
	status := 500
	msg := "요청 처리 중 오류가 발생했습니다"
	var pg *pgconn.PgError
	if errors.Is(e, sql.ErrNoRows) || errors.Is(e, store.ErrAgentNotFound) {
		status = 404
		msg = "대상을 찾을 수 없거나 작업 가능한 상태가 아닙니다"
	} else if errors.Is(e, store.ErrIssuanceConflict) {
		status = 409
		msg = e.Error()
	} else if errors.As(e, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
		status = 409
		msg = "진행 중인 작업 또는 참조 관계를 확인하세요"
	}
	writeError(w, r, status, "issuance_error", msg)
}

var issuanceUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validID(w http.ResponseWriter, r *http.Request, id string) bool {
	if !issuanceUUID.MatchString(id) {
		writeError(w, r, 400, "invalid_request", "ID 형식이 올바르지 않습니다")
		return false
	}
	return true
}
func issuanceDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		writeError(w, r, 400, "invalid_request", "요청 형식이 올바르지 않습니다")
		return false
	}
	return true
}
func (s *Server) requireIssuance(w http.ResponseWriter, r *http.Request) bool {
	if !s.issuance.Enabled() {
		writeError(w, r, 503, "issuance_disabled", "ISSUANCE_ENC_KEY 설정이 필요합니다")
		return false
	}
	return true
}
func (s *Server) listCA(w http.ResponseWriter, r *http.Request) {
	items, e := s.store.ListCAConnections(r.Context())
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"items": items, "enabled": s.issuance.Enabled()})
}

type caRequest struct {
	Name       string              `json:"name"`
	BaseURL    string              `json:"base_url"`
	AppID      string              `json:"app_id"`
	Secret     string              `json:"secret"`
	Subscriber upstream.Subscriber `json:"subscriber"`
	Active     *bool               `json:"active"`
}

func (s *Server) saveCA(w http.ResponseWriter, r *http.Request) {
	if !s.requireIssuance(w, r) {
		return
	}
	var in caRequest
	if !issuanceDecode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if id != "" && !validID(w, r, id) {
		return
	}
	if e := upstream.ValidateURL(in.BaseURL); e != nil {
		writeError(w, r, 400, "invalid_request", e.Error())
		return
	}
	if e := in.Subscriber.Validate(); e != nil {
		writeError(w, r, 400, "invalid_request", e.Error())
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || (in.AppID != "" && len(in.AppID) != 16) || (id == "" && in.AppID == "") || (in.Secret != "" && len(in.Secret) != 32) || (id == "" && in.Secret == "") {
		writeError(w, r, 400, "invalid_request", "연결 이름·App ID(16자)·Secret(32자)을 확인하세요")
		return
	}
	c := store.CAConnection{ID: id, Name: strings.TrimSpace(in.Name), BaseURL: strings.TrimRight(in.BaseURL, "/"), Subscriber: in.Subscriber, Active: true}
	if in.Active != nil {
		c.Active = *in.Active
	}
	var e error
	if in.AppID != "" {
		c.AppID, c.AppIDNonce, e = sensorca.Seal(s.issuance.Key, []byte(in.AppID))
		if e != nil {
			issuanceError(w, r, e)
			return
		}
	}
	if in.Secret != "" {
		c.Secret, c.Nonce, e = sensorca.Seal(s.issuance.Key, []byte(in.Secret))
		if e != nil {
			issuanceError(w, r, e)
			return
		}
	}
	out, e := s.store.SaveCAConnection(r.Context(), c, issuanceActor(r))
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	issuanceJSON(w, r, status, map[string]any{"connection": out})
}
func (s *Server) deleteCA(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	if e := s.store.DeleteCAConnection(r.Context(), id, issuanceActor(r)); e != nil {
		issuanceError(w, r, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) testCA(w http.ResponseWriter, r *http.Request) {
	if !s.requireIssuance(w, r) {
		return
	}
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	c, _, e := s.issuance.Connector(r.Context(), id)
	if e == nil {
		e = c.TestConnection(r.Context())
	}
	if ae := s.store.RecordCATest(r.Context(), id, issuanceActor(r), e == nil); ae != nil {
		issuanceError(w, r, ae)
		return
	}
	msg := "연결 성공"
	if e != nil {
		msg = "CA 연결 실패. 주소·계정·네트워크를 확인하세요"
	}
	issuanceJSON(w, r, 200, map[string]any{"success": e == nil, "message": msg})
}
func (s *Server) productsCA(w http.ResponseWriter, r *http.Request) {
	if !s.requireIssuance(w, r) {
		return
	}
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	c, _, e := s.issuance.Connector(r.Context(), id)
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	items, e := c.ListProducts(r.Context())
	if e != nil {
		writeError(w, r, 502, "ca_error", "CA 상품 목록을 가져올 수 없습니다")
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"items": items})
}
func (s *Server) issuanceSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PATCH" {
		var in store.IssuanceSettings
		if !issuanceDecode(w, r, &in) {
			return
		}
		if e := s.store.SaveIssuanceSettings(r.Context(), in, issuanceActor(r)); e != nil {
			issuanceError(w, r, e)
			return
		}
	}
	v, e := s.store.GetIssuanceSettings(r.Context())
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"settings": v, "enabled": s.issuance.Enabled(), "renewal_worker_enabled": false})
}
func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	if !s.requireIssuance(w, r) {
		return
	}
	var in struct {
		ConnectionID string        `json:"ca_connection_id"`
		Spec         upstream.Spec `json:"spec"`
	}
	if !issuanceDecode(w, r, &in) || !validID(w, r, in.ConnectionID) {
		return
	}
	if e := in.Spec.Validate(); e != nil {
		writeError(w, r, 400, "invalid_request", e.Error())
		return
	}
	o, e := s.issuance.Create(r.Context(), in.ConnectionID, in.Spec, issuanceActor(r))
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 201, map[string]any{"order": o})
}
func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	limit, offset := 50, 0
	var e error
	if v := r.URL.Query().Get("page_size"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > 200 {
			writeError(w, r, 400, "invalid_request", "page_size는 1~200이어야 합니다")
			return
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, e = strconv.Atoi(v)
		if e != nil || offset < 0 || offset > 1000000 {
			writeError(w, r, 400, "invalid_request", "offset 범위 오류")
			return
		}
	}
	items, e := s.store.ListIssuanceOrders(r.Context(), r.URL.Query().Get("status"), limit, offset)
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"items": items})
}
func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	o, e := s.store.GetIssuanceOrder(r.Context(), id)
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"order": o})
}
func (s *Server) orderAction(w http.ResponseWriter, r *http.Request, action string) {
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	var e error
	if action == "cancel" {
		e = s.issuance.Cancel(r.Context(), id, issuanceActor(r))
	} else {
		e = s.store.ChangeIssuanceOrder(r.Context(), id, action, issuanceActor(r))
	}
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	s.getOrder(w, r)
}
func (s *Server) approveOrder(w http.ResponseWriter, r *http.Request) { s.orderAction(w, r, "approve") }
func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request)  { s.orderAction(w, r, "cancel") }
func (s *Server) retryOrder(w http.ResponseWriter, r *http.Request)   { s.orderAction(w, r, "retry") }
func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	items, e := s.store.ListAgents(r.Context())
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"items": items})
}
func (s *Server) createAgent(key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if !issuanceDecode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
			writeError(w, r, 400, "invalid_request", "에이전트 이름을 입력하세요")
			return
		}
		fp, e := ensureSensorCAFingerprint(r, s.store, key)
		if e != nil {
			writeError(w, r, 503, "agent_disabled", "SENSOR_CA_ENC_KEY 설정이 필요합니다")
			return
		}
		a := issuanceActor(r)
		v, token, e := s.store.CreateAgent(r.Context(), store.CreateAgentInput{Name: in.Name, ActorID: a.ID, RequestID: a.RequestID, SourceIP: a.SourceIP})
		if e != nil {
			issuanceError(w, r, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		issuanceJSON(w, r, 201, map[string]any{"agent": v, "enrollment_token": token, "ca_fingerprint": fp, "gateway_port": s.sensorGatewayPort})
	}
}
func (s *Server) disableAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !issuanceDecode(w, r, &in) {
		return
	}
	if in.Status != "disabled" {
		writeError(w, r, 400, "invalid_request", "disabled 전이만 지원합니다")
		return
	}
	a := issuanceActor(r)
	v, e := s.store.DisableAgent(r.Context(), store.DisableAgentInput{AgentID: id, ActorID: a.ID, RequestID: a.RequestID, SourceIP: a.SourceIP})
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"agent": v})
}
func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	items, e := s.store.ListDeploymentTargets(r.Context())
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"items": items})
}
func (s *Server) saveTarget(w http.ResponseWriter, r *http.Request) {
	var t agentwire.Target
	if !issuanceDecode(w, r, &t) || !validID(w, r, t.AgentID) {
		return
	}
	t.ID = r.PathValue("id")
	if t.ID != "" && !validID(w, r, t.ID) {
		return
	}
	t.CurrentCertificateID = nil
	t.CurrentFingerprint = ""
	if e := t.Validate(); e != nil {
		writeError(w, r, 400, "invalid_request", e.Error())
		return
	}
	out, e := s.store.SaveDeploymentTarget(r.Context(), t, issuanceActor(r))
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	status := 200
	if t.ID == "" {
		status = 201
	}
	issuanceJSON(w, r, status, map[string]any{"target": out})
}
func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	if e := s.store.DeleteDeploymentTarget(r.Context(), id, issuanceActor(r)); e != nil {
		issuanceError(w, r, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) deployOrder(w http.ResponseWriter, r *http.Request) {
	if !s.requireIssuance(w, r) {
		return
	}
	id := r.PathValue("id")
	if !validID(w, r, id) {
		return
	}
	var in struct {
		TargetID string `json:"deployment_target_id"`
	}
	if !issuanceDecode(w, r, &in) || !validID(w, r, in.TargetID) {
		return
	}
	j, e := s.store.CreateDeployJob(r.Context(), id, in.TargetID, issuanceActor(r))
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 201, map[string]any{"job": j})
}
func (s *Server) listDeploymentJobs(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("order_id")
	if !validID(w, r, id) {
		return
	}
	items, e := s.store.ListDeployJobs(r.Context(), id)
	if e != nil {
		issuanceError(w, r, e)
		return
	}
	issuanceJSON(w, r, 200, map[string]any{"items": items})
}
