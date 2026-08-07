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

export type ScanJobStatus = "pending" | "running" | "completed" | "failed";

export interface ScanJob {
  id: string;
  status: ScanJobStatus;
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

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });
  if (!res.ok) {
    throw await toApiError(res);
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
  const res = await fetch(`/api/v1/certificates/export${qs ? `?${qs}` : ""}`);
  if (!res.ok) {
    throw await toApiError(res);
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
    throw await toApiError(res);
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

/** 센서 생성 — 등록 토큰 발급. 서버는 sensor.create 감사 이벤트를 남긴다. */
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
