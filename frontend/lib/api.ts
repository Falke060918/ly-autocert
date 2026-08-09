/**
 * 백엔드 API 클라이언트.
 * 브라우저는 같은 출처(/api/*)로만 호출하고 Next 서버가 백엔드로 프록시한다
 * (next.config.ts rewrites) — HttpOnly 세션 쿠키가 CORS 설정 없이 동작한다.
 * 응답 타입은 backend/api/openapi.yaml 스키마를 따른다.
 */

// ── 타입 (openapi.yaml components/schemas) ────────────────────────────────

export type Role =
  | "system_administrator"
  | "pki_administrator"
  | "security_administrator"
  | "operator"
  | "auditor"
  | "viewer";

export interface User {
  id: string;
  username: string;
  display_name: string | null;
  role: Role;
  auth_provider: string;
  is_active: boolean;
  department_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface UserListResponse {
  request_id?: string;
  items: User[];
  total: number;
}

/** 부분 수정 — 제공한 필드만 바뀐다 (PATCH /users/{id}). */
export interface UserUpdateRequest {
  role?: Role;
  display_name?: string | null;
}

export type CertificateStatus =
  | "active"
  | "expired"
  | "revoked"
  | "replaced"
  | "unknown";

export type CertificateType =
  | "server"
  | "client"
  | "ca"
  | "code_signing"
  | "email"
  | "other";

export interface Certificate {
  id: string;
  fingerprint_sha256: string;
  serial_number: string;
  subject_dn: string;
  issuer_dn: string;
  common_name: string;
  sans: string[];
  not_before: string;
  not_after: string;
  public_key_algorithm: string;
  public_key_size: number;
  signature_algorithm: string;
  certificate_type: CertificateType;
  status: CertificateStatus;
  ca_id: string | null;
  owner_id: string | null;
  department_id: string | null;
  environment: string | null;
  auto_renew: boolean;
  renewal_policy_id: string | null;
  subject_key_id: string | null;
  authority_key_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface CertificateListResponse {
  request_id: string;
  items: Certificate[];
  total: number;
  page: number;
  page_size: number;
}

export interface TargetRef {
  id: string;
  name: string;
  target_type: string;
  hostname: string | null;
  ip_address: string | null;
}

export interface CertificateLocation {
  id: string;
  certificate_id: string;
  target_id: string;
  location_type:
    | "network_endpoint"
    | "file"
    | "windows_store"
    | "keystore"
    | "other";
  file_path: string | null;
  store_name: string | null;
  service_name: string | null;
  port: number | null;
  last_verified_at: string | null;
  deployment_status: "unknown" | "deployed" | "pending" | "failed" | "removed";
  tls_version: string | null;
  target: TargetRef;
}

/**
 * 체인 1단계. 배열은 자신→상위→루트 순이며, 인벤토리에 없는 상위는
 * stored=false(id=null, subject_dn은 하위의 issuer_dn)로 표시된다.
 */
export interface CertificateChainItem {
  id: string | null;
  stored: boolean;
  subject_dn: string;
  common_name: string | null;
  not_after: string | null;
  certificate_type: CertificateType | null;
  status: CertificateStatus | null;
  self_signed: boolean;
}

export interface CertificateDetailResponse {
  request_id: string;
  certificate: Certificate;
  locations: CertificateLocation[];
  chain: CertificateChainItem[];
}

export interface CertificateSummary {
  request_id: string;
  total: number;
  expiring_7d: number;
  expiring_30d: number;
  expiring_60d: number;
  expiring_90d: number;
  expired: number;
  self_signed: number;
  unowned: number;
}

export interface ImportedCertificate {
  id: string;
  fingerprint_sha256: string;
  common_name: string;
  not_before: string;
  not_after: string;
  certificate_type: CertificateType;
  status: CertificateStatus;
  duplicate: boolean;
}

export interface ImportResult {
  request_id: string;
  imported: ImportedCertificate[];
  location_id: string;
}

export interface ScanRequest {
  targets: string[];
  ports?: number[];
  timeout_ms?: number;
  concurrency?: number;
  /** 지정하면 서버 직접 실행 대신 해당 센서가 스캔을 수행한다 (Phase 3a). */
  sensor_id?: string;
}

export interface ScanAccepted {
  job_id: string;
  status: "pending";
  total_endpoints: number;
  request_id?: string;
}

/** leased = 센서가 Job을 수령했고 아직 실행 보고 전인 상태 (00-spec 7.8). */
export type ScanJobStatus =
  | "pending"
  | "leased"
  | "running"
  | "completed"
  | "failed";

/** Job 실행 주체 — server = 서버 직접 실행, sensor = 센서 위임(pull 리스). */
export type ScanExecutorType = "server" | "sensor";

export interface ScanJob {
  id: string;
  status: ScanJobStatus;
  executor_type: ScanExecutorType;
  /** executor_type이 sensor일 때 실행을 맡은 센서 id. 서버 실행이면 null. */
  sensor_id: string | null;
  targets: string[];
  ports: number[];
  timeout_ms: number;
  concurrency: number;
  total_endpoints: number;
  scanned_endpoints: number;
  reachable_endpoints: number;
  certificates_found: number;
  certificates_new: number;
  error: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

// ── 센서 (00-spec 7.8 — 서버 API는 U5에서 구현, 아래는 계약 기준 타입) ────

export type SensorStatus = "pending" | "active" | "disabled";

export interface Sensor {
  id: string;
  name: string;
  status: SensorStatus;
  client_cert_fingerprint_sha256: string | null;
  registered_at: string | null;
  last_seen_at: string | null;
  disabled_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface SensorListResponse {
  request_id: string;
  items: Sensor[];
}

export interface SensorDetailResponse {
  request_id: string;
  sensor: Sensor;
}

/**
 * 센서 생성 응답. enrollment_token은 이 응답에서 **1회만** 내려온다 —
 * 서버는 SHA-256 해시만 저장하므로 다시 조회할 수 없다 (00-spec 7.8 등록 흐름).
 */
export interface SensorCreateResponse {
  request_id: string;
  sensor: Sensor;
  enrollment_token: string;
  ca_fingerprint: string;
  enroll_command_example: string;
}

// ── 알림 (docs/design/05-notifications.md · openapi notifications 태그) ────

/** 서버가 만료 잔여일로 계산한 심각도 (0일 이하 critical → 7일 error → 30일 warning). */
export type NotificationSeverity = "info" | "warning" | "error" | "critical";

/** 알림함 목록 필터 — archived는 보관함, all은 "보관하지 않은 전체"다. */
export type NotificationStatusFilter = "unread" | "all" | "archived";

/**
 * 알림함 행 1건. 표시 내용(title/body)은 사건 시점에 굳어 있으므로
 * 인증서가 지워져도 그대로 읽을 수 있다 (certificate_id만 null이 될 수 있다).
 */
export interface NotificationItem {
  id: string;
  event_id: string;
  title: string;
  body: string;
  severity: NotificationSeverity;
  certificate_id: string | null;
  read_at: string | null;
  archived_at: string | null;
  created_at: string;
}

export interface NotificationListResponse {
  request_id?: string;
  items: NotificationItem[];
  total: number;
  /** 필터와 무관한 전체 안 읽은 건수. */
  unread: number;
  page: number;
  page_size: number;
}

export type NotificationChannel = "email" | "webhook" | "inbox" | "syslog";

export type NotificationEventType =
  | "certificate.expiring"
  | "certificate.expired";

/** 수신자 종류 정본 (설계 14.2) — 서버 검증과 같은 4종이다. */
export type NotificationRecipientKind = "owner" | "role" | "user" | "static";

export interface NotificationRecipient {
  kind: NotificationRecipientKind;
  /** kind=role */
  role?: Role;
  /** kind=user */
  user_id?: string;
  /** kind=static */
  email?: string;
}

/** 필터가 null이면 전체 인증서에 매칭된다. */
export interface NotificationPolicy {
  id: string;
  name: string;
  enabled: boolean;
  event_types: NotificationEventType[];
  thresholds: number[];
  channels: NotificationChannel[];
  recipients: NotificationRecipient[];
  filter_environments: string[] | null;
  filter_department_ids: string[] | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface NotificationPolicyListResponse {
  request_id?: string;
  items: NotificationPolicy[];
  total: number;
}

export interface NotificationPolicyRequest {
  name: string;
  enabled?: boolean;
  event_types: NotificationEventType[];
  thresholds: number[];
  channels: NotificationChannel[];
  recipients: NotificationRecipient[];
  filter_environments?: string[];
  filter_department_ids?: string[];
}

/** 부분 수정 — 보낸 필드만 바뀐다 (PATCH /notification-policies/{id}). */
export type NotificationPolicyPatch = Partial<NotificationPolicyRequest>;

/**
 * 채널 상태. **필드는 이 셋뿐이다** — 호스트·계정·URL 같은 설정값은 응답에
 * 아예 실리지 않는다 (설계 9.3). 비활성 사유는 reason 문자열로만 확인한다.
 */
export interface NotificationChannelStatus {
  channel: NotificationChannel;
  enabled: boolean;
  reason: string;
}

export interface NotificationChannelListResponse {
  request_id?: string;
  items: NotificationChannelStatus[];
}

export interface NotificationTestChannelResult {
  channel: NotificationChannel;
  status: "sent" | "skipped" | "failed";
  reason?: string;
}

export interface NotificationTestResponse {
  request_id?: string;
  policy_id: string;
  results: NotificationTestChannelResult[];
}

/** skipped = 실패가 아니라 "보낼 수 없어 건너뜀"(채널 비활성·수신자 없음). */
export type NotificationDeliveryStatus =
  | "pending"
  | "sending"
  | "sent"
  | "failed"
  | "skipped";

export interface NotificationDelivery {
  id: string;
  event_id: string;
  channel: NotificationChannel;
  recipient: string;
  status: NotificationDeliveryStatus;
  attempt_count: number;
  next_attempt_at: string;
  last_error: string | null;
  sent_at: string | null;
  created_at: string;
  event_type: string;
  severity: NotificationSeverity;
  certificate_id: string | null;
  threshold_days: number | null;
}

export interface NotificationDeliveryListResponse {
  request_id?: string;
  items: NotificationDelivery[];
  total: number;
  page: number;
  page_size: number;
}

// ── 오류·요청 헬퍼 ────────────────────────────────────────────────────────

/** 통일 오류 응답(00-spec 13장)을 담는 예외. status로 401/403 분기한다. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId?: string;

  constructor(status: number, code: string, message: string, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

async function toApiError(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as {
      error?: { code?: string; message?: string };
      request_id?: string;
    };
    return new ApiError(
      res.status,
      body.error?.code ?? "unknown",
      body.error?.message ?? `요청이 실패했습니다 (HTTP ${res.status}).`,
      body.request_id,
    );
  } catch {
    return new ApiError(res.status, "unknown", `요청이 실패했습니다 (HTTP ${res.status}).`);
  }
}

/**
 * 401을 자체 UI로 처리하는 엔드포인트 — 전역 탈출 경로를 태우지 않는다.
 * login은 자격 증명 오류가 401이고, logout은 auth.tsx가 직접 뒷정리한다.
 */
const BOUNCE_EXEMPT = ["/api/v1/auth/login", "/api/v1/auth/logout"];

/** 401 응답 하나마다 탈출을 시작하지 않도록 하는 1회 빗장. */
let bouncing = false;

/**
 * 세션이 만료·폐기됐을 때의 탈출 경로.
 * 남은 HttpOnly 쿠키를 먼저 만료시켜야 proxy.ts가 /login을 다시 /로 되돌리지 않는다.
 * reason 파라미터는 로그인 화면의 안내 문구와 proxy.ts의 2차 방어에 함께 쓰인다.
 */
export function bounceToLogin(): void {
  if (typeof window === "undefined" || bouncing) return;
  bouncing = true;
  const here = window.location.pathname + window.location.search;
  const params = new URLSearchParams({ reason: "session_expired" });
  if (here !== "/") params.set("next", here);
  void clearSessionCookie().then(() => {
    window.location.replace(`/login?${params.toString()}`);
  });
}

async function failed(path: string, res: Response): Promise<ApiError> {
  // 소프트 내비게이션에서는 /auth/me를 다시 부르지 않으므로, 폐기된 세션은 각 화면의
  // API 401로만 드러난다 — 여기서 잡지 않으면 앱 안에 갇힌다.
  if (res.status === 401 && !BOUNCE_EXEMPT.includes(path.split("?")[0])) {
    bounceToLogin();
  }
  return toApiError(res);
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });
  if (!res.ok) {
    throw await failed(path, res);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

// ── 엔드포인트 ────────────────────────────────────────────────────────────

export function login(username: string, password: string) {
  return apiFetch<{ token: string; expires_at: string; user: User }>(
    "/api/v1/auth/login",
    { method: "POST", body: JSON.stringify({ username, password }) },
  );
}

export function logout() {
  return apiFetch<void>("/api/v1/auth/logout", { method: "POST" });
}

export function fetchMe() {
  return apiFetch<User>("/api/v1/auth/me");
}

/**
 * 남은 세션 쿠키를 만료시킨다 (Next 로컬 라우트 — app/session/clear).
 * 세션이 서버에서 이미 폐기된 경우 /auth/logout이 401이라 HttpOnly 쿠키가 남는다.
 * 실패해도 로그인 이동은 계속한다 — proxy.ts가 reason 파라미터를 통과시킨다.
 */
export async function clearSessionCookie(): Promise<void> {
  try {
    await fetch("/session/clear", { method: "POST" });
  } catch {
    // 네트워크 실패는 무시한다.
  }
}

// ── 사용자 관리 (System Administrator 전용 — 00-spec 7.11) ────────────────

export function listUsers() {
  return apiFetch<UserListResponse>("/api/v1/users");
}

export function createUser(req: {
  username: string;
  password: string;
  display_name?: string | null;
  role: Role;
}) {
  return apiFetch<User>("/api/v1/users", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** 역할·표시이름 부분 수정. 서버는 user.update 감사 이벤트를 남긴다. */
export function updateUser(id: string, patch: UserUpdateRequest) {
  return apiFetch<User>(`/api/v1/users/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify(patch),
  });
}

/** 비활성화 — 이후 로그인이 거부된다. 마지막 관리자·자기 자신은 서버가 거부한다. */
export function disableUser(id: string) {
  return apiFetch<User>(`/api/v1/users/${encodeURIComponent(id)}/disable`, {
    method: "POST",
  });
}

export function enableUser(id: string) {
  return apiFetch<User>(`/api/v1/users/${encodeURIComponent(id)}/enable`, {
    method: "POST",
  });
}

/**
 * 관리자 비밀번호 재설정 — 관리자가 새 비밀번호를 직접 정한다.
 * 서버 응답은 204(본문 없음)이고, 해당 사용자의 기존 세션은 서버에서 즉시 폐기된다.
 */
export function resetUserPassword(id: string, password: string) {
  return apiFetch<void>(`/api/v1/users/${encodeURIComponent(id)}/password`, {
    method: "POST",
    body: JSON.stringify({ password }),
  });
}

export function listCertificates(params: URLSearchParams) {
  const qs = params.toString();
  return apiFetch<CertificateListResponse>(
    `/api/v1/certificates${qs ? `?${qs}` : ""}`,
  );
}

export function getCertificate(id: string) {
  return apiFetch<CertificateDetailResponse>(
    `/api/v1/certificates/${encodeURIComponent(id)}`,
  );
}

export function getCertificateSummary() {
  return apiFetch<CertificateSummary>("/api/v1/certificates/summary");
}

/**
 * 인증서 인벤토리 CSV 내보내기 — 목록과 같은 조회 조건, 페이지네이션 없음.
 * 모든 호출이 서버에서 certificate.export 감사 이벤트를 남긴다.
 */
export async function exportCertificatesCsv(
  params: URLSearchParams,
): Promise<Blob> {
  const qs = params.toString();
  const path = `/api/v1/certificates/export${qs ? `?${qs}` : ""}`;
  const res = await fetch(path);
  if (!res.ok) {
    throw await failed(path, res);
  }
  return res.blob();
}

/** 인증서 파일 업로드 — 원문 바이트를 그대로 보낸다 (PEM·DER). */
export async function importCertificateFile(file: File): Promise<ImportResult> {
  const res = await fetch("/api/v1/certificates/import", {
    method: "POST",
    headers: { "Content-Type": "application/octet-stream" },
    body: await file.arrayBuffer(),
  });
  if (!res.ok) {
    throw await failed("/api/v1/certificates/import", res);
  }
  return res.json() as Promise<ImportResult>;
}

export function createScan(req: ScanRequest) {
  return apiFetch<ScanAccepted>("/api/v1/discovery/scans", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function getScan(id: string) {
  return apiFetch<ScanJob>(`/api/v1/discovery/scans/${encodeURIComponent(id)}`);
}

/** 센서 생성 — 등록 토큰 발급. 서버는 sensor.token.issue 감사 이벤트를 남긴다. */
export function createSensor(name: string) {
  return apiFetch<SensorCreateResponse>("/api/v1/sensors", {
    method: "POST",
    body: JSON.stringify({ name }),
  });
}

export function listSensors() {
  return apiFetch<SensorListResponse>("/api/v1/sensors");
}

export function getSensor(id: string) {
  return apiFetch<SensorDetailResponse>(
    `/api/v1/sensors/${encodeURIComponent(id)}`,
  );
}

/** 센서 비활성화 — 이후 mTLS 접속·작업 리스가 거부된다. 서버는 sensor.disable 감사 이벤트를 남긴다. */
export function disableSensor(id: string) {
  return apiFetch<SensorDetailResponse>(
    `/api/v1/sensors/${encodeURIComponent(id)}/disable`,
    { method: "POST" },
  );
}

// ── 알림함 (전 역할 — 언제나 본인 소유 행만 다룬다) ───────────────────────

/** 남의 알림을 지정하면 404다 — 존재 여부를 흘리지 않으려고 403을 쓰지 않는다. */
export function listNotifications(
  status: NotificationStatusFilter = "unread",
  opts?: { page?: number; pageSize?: number },
) {
  const params = new URLSearchParams({ status });
  if (opts?.page) params.set("page", String(opts.page));
  if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
  return apiFetch<NotificationListResponse>(
    `/api/v1/notifications?${params.toString()}`,
  );
}

/** 벨 배지용 — 목록보다 훨씬 가벼우므로 주기 폴링은 이쪽만 한다. */
export function getUnreadNotificationCount() {
  return apiFetch<{ request_id?: string; unread: number }>(
    "/api/v1/notifications/unread-count",
  );
}

/** 읽음 처리 (204, 본문 없음). */
export function markNotificationRead(id: string) {
  return apiFetch<void>(
    `/api/v1/notifications/${encodeURIComponent(id)}/read`,
    { method: "POST" },
  );
}

/** 보관 — 읽음도 함께 처리된다 (204, 본문 없음). */
export function archiveNotification(id: string) {
  return apiFetch<void>(
    `/api/v1/notifications/${encodeURIComponent(id)}/archive`,
    { method: "POST" },
  );
}

export function markAllNotificationsRead() {
  return apiFetch<{ request_id?: string; updated: number }>(
    "/api/v1/notifications/read-all",
    { method: "POST" },
  );
}

// ── 알림 정책·채널·발송 이력 (System/Security Administrator — 00-spec 7.11) ─

export function listNotificationPolicies() {
  return apiFetch<NotificationPolicyListResponse>("/api/v1/notification-policies");
}

/** 서버는 생성과 notification.policy.create 감사를 단일 트랜잭션으로 남긴다. */
export function createNotificationPolicy(req: NotificationPolicyRequest) {
  return apiFetch<NotificationPolicy>("/api/v1/notification-policies", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export function updateNotificationPolicy(
  id: string,
  patch: NotificationPolicyPatch,
) {
  return apiFetch<NotificationPolicy>(
    `/api/v1/notification-policies/${encodeURIComponent(id)}`,
    { method: "PATCH", body: JSON.stringify(patch) },
  );
}

/** 삭제해도 이미 만들어진 이벤트·발송 이력은 남는다 (policy_id만 NULL이 된다). */
export function deleteNotificationPolicy(id: string) {
  return apiFetch<void>(
    `/api/v1/notification-policies/${encodeURIComponent(id)}`,
    { method: "DELETE" },
  );
}

/**
 * 테스트 발송. channel을 비우면 정책의 전 채널로 보낸다.
 * 서버에 알림 발송이 배선되어 있지 않으면 503(channels_unavailable)이다.
 */
export function testNotificationPolicy(
  id: string,
  body?: { channel?: NotificationChannel; recipient?: string },
) {
  return apiFetch<NotificationTestResponse>(
    `/api/v1/notification-policies/${encodeURIComponent(id)}/test`,
    { method: "POST", body: JSON.stringify(body ?? {}) },
  );
}

/** 채널 상태 — 설정값은 응답에 없다. 비활성 사유(reason)만 그대로 보여준다. */
export function listNotificationChannels() {
  return apiFetch<NotificationChannelListResponse>("/api/v1/notification-channels");
}

/** 발송 이력 — "왜 안 왔는지"의 답. 빈 필터 값은 붙이지 않는다. */
export function listNotificationDeliveries(opts?: {
  status?: NotificationDeliveryStatus;
  channel?: NotificationChannel;
  eventId?: string;
  page?: number;
  pageSize?: number;
}) {
  const params = new URLSearchParams();
  if (opts?.status) params.set("status", opts.status);
  if (opts?.channel) params.set("channel", opts.channel);
  if (opts?.eventId) params.set("event_id", opts.eventId);
  if (opts?.page) params.set("page", String(opts.page));
  if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
  const qs = params.toString();
  return apiFetch<NotificationDeliveryListResponse>(
    `/api/v1/notification-deliveries${qs ? `?${qs}` : ""}`,
  );
}
