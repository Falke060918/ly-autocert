import { apiFetch, type IssuanceStatus, type DeployJobStatus, type IssuanceComparison } from "./api";
export type KeyAlgorithm =
  "rsa2048" | "rsa3072" | "rsa4096" | "ecdsa_p256" | "ecdsa_p384";
export type ValidationMethod = "txt_dns_record" | "txt_http_file";
export interface Subscriber {
  existing_subscriber_id: number;
  new_subscriber?: {
    legal_organization_name: string;
    subscriber_type: string;
    country_iso_code2: string;
    locality: string;
    province: string;
    phone_number: string;
    address: string;
  };
  contact: {
    existing_contact_id?: number;
    new_contact?: {
      first_name?: string;
      last_name: string;
      email: string;
      contact_types: string[];
    };
  };
}
export interface CAConnection {
  id: string;
  name: string;
  base_url: string;
  app_id_configured: boolean;
  subscriber: Subscriber;
  active: boolean;
  secret_configured: boolean;
}
export interface Product {
  id: number;
  name: string;
  status: string;
  max_order_validity: number | null;
  allow_wildcard: boolean;
}
export interface IssuanceSpec {
  dns_names: string[];
  key_algorithm: KeyAlgorithm;
  validity_days: number;
  product_id: number;
  signing_ca_type: "automatic" | "rsa" | "ecc";
  domain_validations: { name: string; method: ValidationMethod }[];
}
export interface Order {
  comparison?: IssuanceComparison;
  id: string;
  ca_connection_id: string;
  status: IssuanceStatus;
  spec: IssuanceSpec;
  upstream_order_code: string;
  upstream_status?: string;
  certificate_id: string | null;
  created_at: string;
  finished_at: string | null;
  last_error: string;
  domain_validation: {
    domain_name: string;
    validation_method: string;
    validation_key: string;
    dns_txt_record: string;
    http_file_url: string;
    status: string;
  }[];
  events?: { at: string; kind: string; detail: Record<string, string> }[];
}
export interface Agent {
  id: string;
  name: string;
  status: "pending" | "active" | "disabled";
  last_seen_at: string | null;
}
export interface Target {
  id?: string;
  agent_id: string;
  kind: "nginx" | "apache";
  service_unit: "nginx" | "httpd" | "apache2";
  cert_path: string;
  key_path: string;
  chain_path: string;
  verify_host: string;
  verify_port: number;
  sni_name: string;
  active: boolean;
}
export interface DeployJob {
  id: string;
  status: DeployJobStatus;
  error: string;
  observed_fingerprint: string;
  steps: { at: string; name: string; ok: boolean; message: string }[];
  created_at: string;
}
export const STATUS: Record<IssuanceStatus, string> = {
  requested: "요청됨",
  approval_pending: "승인 대기",
  issuing: "발급 중",
  awaiting_validation: "도메인 검증 필요",
  issued: "CA 발급 완료 · 결과 확인 필요",
  deploying: "배포 중",
  completed: "완료",
  cancelled: "취소됨",
  issue_failed: "발급 실패",
  deploy_failed: "배포 실패",
  rolled_back: "이전 인증서로 복원",
  manual_required: "수동 확인 필요",
};
export const JOB_STATUS: Record<DeployJobStatus, string> = {
  pending: "대기",
  leased: "작업 수령",
  running: "배포 중",
  completed: "배포 완료",
  failed: "배포 실패",
  rolled_back: "이전 인증서로 복원",
};
export const STEP: Record<string, string> = {
  precheck: "사전 검사",
  backup: "백업",
  write_temp: "임시 파일 저장",
  swap: "파일 교체",
  config_test: "설정 검사",
  reload: "서비스 reload",
  verify: "지문 검증",
  cleanup: "백업 정리",
  rollback: "이전 인증서 복원",
};
export const EVENT: Record<string, string> = {
  ca_issued: "CA 발급 완료",
  collection_warning: "발급 결과 수집 경고",
  certificate_comparison: "요청·발급 비교 결과 기록",
  validation_warning: "발급 검증 경고",
  chain_warning: "CA 체인 경고",
  created: "발급 요청",
  approved: "승인",
  order_placed: "CA 주문 접수",
  validation_pending: "도메인 검증 대기",
  validated: "도메인 검증 완료",
  issued: "인증서 발급 완료",
  inventory_registered: "인벤토리 등록",
  deploy_started: "배포 시작",
  deploy_finished: "배포 결과",
  failed: "처리 실패",
  retried: "재시도",
  retry_scheduled: "재시도 예약",
  cancelled: "취소",
  cancel_failed: "취소 실패",
};
export const orderStatus = (order: Pick<Order, "status" | "upstream_status">) =>
  order.upstream_status === "issued" && order.status === "issue_failed"
    ? "CA 발급 완료 · 결과 확인 필요"
    : STATUS[order.status];
export const send = <T>(path: string, body?: unknown, method = "POST") =>
  apiFetch<T>("/api/v1" + path, {
    method,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
