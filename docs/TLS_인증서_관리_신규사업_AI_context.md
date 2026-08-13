---
document_type: new_business_proposal
project_id: tls-certificate-management
project_name: TLS 인증서 관리 신규사업
version: 2.1-ai-context
language: ko-KR
as_of: 2026-08-12
source_artifact: TLS_인증서_관리_신규사업_기획안_v2.pptx
primary_purpose: AI가 사업기획, 제품설계, 개발명세, 리서치, PPT 재작성에 재사용할 수 있는 구조화 컨텍스트
status: working_draft
---

# 0. 문서 사용 지침

이 문서는 TLS/SSL 인증서 관리 신규사업 아이디어를 AI가 재사용하기 쉬운 형태로 정리한 구조화 컨텍스트다.

AI가 후속 작업을 수행할 때 다음 구분을 유지한다.

- `FACT`: 공개 표준, 벤더 공식 문서, 사건 등 외부 근거가 있는 사실.
- `ASSUMPTION`: 현재 사업 가설 또는 계산을 위한 전제.
- `HYPOTHESIS`: 고객 인터뷰/PoC로 검증해야 할 사업 가설.
- `DECISION`: 현재 제품 설계상 채택한 방향.
- `RISK`: 기술적/사업적 불확실성 또는 과장해서는 안 되는 부분.
- `NON_GOAL`: 해당 단계에서 의도적으로 하지 않는 범위.

# 1. 사업 한 줄 정의

`DECISION`

짧아지는 TLS 인증서 수명에 대응하여, 고객 내부망에 경량 센서를 설치하고 네트워크 스캔·DNS·SNI 정보를 교차 분석함으로써 인증서, 도메인, 엔드포인트, 서비스, 소유자의 관계를 중앙에서 가시화하고 만료 리스크를 관리하는 온프레미스 우선 Certificate Lifecycle Management 진입 제품을 만든다.

장기적으로는 이 인벤토리를 기반으로 ACME/CA API 기반 발급·갱신 오케스트레이션과 환경별 인증서 배포 자동화로 확장한다.

# 2. Why Now

## 2.1 공개 TLS 인증서 수명 단축

`FACT`

CA/Browser Forum Ballot SC081v3 기준 공개적으로 신뢰되는 TLS 서버 인증서의 최대 유효기간은 단계적으로 축소된다.

| 구간 | 최대 유효기간 |
|---|---:|
| ~ 2026-03-14 | 398일 |
| 2026-03-15 ~ | 200일 |
| 2027-03-15 ~ | 100일 |
| 2029-03-15 ~ | 47일 |

현재 기준일 `2026-08-12`에는 200일 구간이다.

추가로 DCV/SAN 검증 정보의 재사용 기간도 단축되며, 2029년에는 10일 수준까지 축소되는 로드맵이 포함되어 있다.

`IMPLICATION`

인증서 수명 단축은 단순 정책 변경이 아니라 인증서 갱신·검증·배포 이벤트 수를 구조적으로 증가시키는 운영 변화다.

## 2.2 운영량 증가 정량 예시

`ASSUMPTION`

- 인증서는 최대 유효기간을 기준으로 지속 갱신
- 연간 예상 갱신 이벤트 = `인증서 수 × 365 ÷ 최대 유효기간`
- 연간 예상 서버 교체 작업 = `인증서가 설치된 서버 수 × 365 ÷ 최대 유효기간`

### 인증서 10개 기준

| 최대 유효기간 | 연간 갱신 이벤트(약) |
|---:|---:|
| 398일 | 9 |
| 200일 | 18 |
| 100일 | 37 |
| 47일 | 78 |

### 인증서 100개 기준

| 최대 유효기간 | 연간 갱신 이벤트(약) |
|---:|---:|
| 398일 | 92 |
| 200일 | 183 |
| 100일 | 365 |
| 47일 | 777 |

### 인증서 설치 서버 10대 기준

| 최대 유효기간 | 연간 서버 교체 작업(약) |
|---:|---:|
| 398일 | 9 |
| 200일 | 18 |
| 100일 | 37 |
| 47일 | 78 |

### 인증서 설치 서버 100대 기준

| 최대 유효기간 | 연간 서버 교체 작업(약) |
|---:|---:|
| 398일 | 92 |
| 200일 | 183 |
| 100일 | 365 |
| 47일 | 777 |

`KEY_MESSAGE`

- 인증서 최대 유효기간이 398일에서 47일로 단축되면 갱신 및 교체 작업량은 약 8.5배 증가한다.
- 인증서 수뿐 아니라 실제 인증서가 배포된 서버/Endpoint 수가 운영 부담을 결정한다.
- 동일 인증서가 여러 서버, WAF, LB, WAS 등에 공유될 경우 실제 교체 작업량은 인증서 수보다 훨씬 커질 수 있다.
Excel, 이메일 알림, 담당자 기억, 수동 티켓 중심 프로세스는 갱신 이벤트가 증가할수록 누락·책임소재 불명·잘못된 배포 등 휴먼에러 위험이 빠르게 커진다.

# 3. 고객 문제 정의

## 3.1 핵심 질문

많은 조직이 다음 질문에 즉시 답하기 어렵다.

1. 현재 관리 중인 전체 도메인은 몇 개인가?
2. 전체 TLS 인증서는 몇 개인가?
3. 각 인증서는 어느 서버, 포트, WAF, LB, CDN, 서비스에 배포되어 있는가?
4. 동일 인증서를 몇 개의 서버/서비스가 공유하고 있는가?
5. 각 인증서와 서비스의 실제 소유자/운영 담당자는 누구인가?
6. 어떤 인증서가 곧 만료되는가?
7. 만료 경고를 누가 받고, 미조치 시 누구에게 escalation 되는가?
8. 갱신된 인증서가 실제 endpoint에 정상 배포되었는가?

## 3.2 Lifecycle별 Pain Point

### Discovery

- 외부/내부망/비표준 포트에 인증서가 분산됨.
- 동일 IP:PORT에서 SNI에 따라 서로 다른 인증서가 제공될 수 있음.
- WAF/LB/CDN에서 TLS termination이 발생할 수 있음.
- 파일, keystore, truststore 내부 인증서는 네트워크 스캔만으로 발견되지 않을 수 있음.

### Inventory

- 도메인 수, 인증서 수, endpoint 수가 서로 다른 개념인데 하나의 Excel 목록으로 혼합 관리됨.
- 동일 인증서가 여러 endpoint에 복제되어도 관계가 표현되지 않음.

### Ownership

- 인증서 담당자와 서비스 담당자가 다름.
- 담당자가 조직·서비스별로 분산됨.
- 퇴사/이동/조직변경 시 ownership이 유실될 수 있음.

### Renewal

- CA 포털, 이메일, Excel, ITSM 티켓 등을 사람이 연결함.
- 짧은 인증서 수명에서 반복 작업량이 증가함.

### Deployment

- 인증서 발급과 실제 서비스 반영은 별도 문제임.
- 서버/웹서버/WAS/WAF/LB/CDN별로 설치 방식과 reload/restart 방식이 다름.

### Audit / Compliance

- 누가 언제 어떤 인증서를 변경했는지 추적이 약함.
- 미승인 CA, 약한 알고리즘, 만료 임박 인증서, ownership 부재를 중앙에서 확인하기 어려움.

# 4. 사고 영향

`FACT`

대표적으로 공개적으로 알려진 인증서 만료 관련 사례에는 다음이 있다.

- Microsoft Teams, 2020: 인증서 만료로 서비스 접근 장애 발생.
- Ericsson/O2/SoftBank, 2018: 인증서 만료 관련 문제로 모바일 데이터 서비스 장애 발생.
- Equifax, 2017: 트래픽 모니터링 장비의 만료 인증서가 침해 탐지 공백과 관련된 문제로 지적됨.

`IMPLICATION`

인증서 만료는 단순 경고 이벤트가 아니다. 서비스 가용성, 보안 모니터링, 공급망, 대외 서비스 연속성에 영향을 줄 수 있다.

# 5. 제품 전략

## 5.1 단계적 접근

`DECISION`

처음부터 Full CLM 또는 모든 서버의 인증서 자동교체를 만들지 않는다.

### Phase 1 — Discovery / Inventory / Alert

목표: 고객이 현재 무엇을 갖고 있고 어디에 배포되어 있는지 파악하고 만료 장애를 예방한다.

핵심 기능:

- 온프레미스 경량 센서
- CIDR/port 기반 TLS endpoint scan
- 내부 DNS/FQDN 후보 수집
- SNI probe
- certificate chain/SAN/issuer/fingerprint/expiry 수집
- 중앙 인벤토리
- certificate ↔ endpoint ↔ service ↔ owner 관계 관리
- 만료 알림 및 escalation
- audit log
- CSV/Excel import/export
- API

### Phase 1.5 — Ownership / Workflow / Reporting

- 서비스 오너 지정
- 승인/acknowledgement
- 알림 escalation 정책
- policy violation score
- 기간별 리포트
- CMDB, ITSM, K8s, LB/WAF 등 보조 connector 확대

### Phase 2 — Issuance / Renewal Orchestration

- ACME 또는 CA API 기반 발급/갱신
- CSR/주문/검증 workflow
- CA adapter abstraction
- policy/approval
- 계정키/EAB/credential 관리

### Phase 3 — Deployment Automation / Multi-CA

- WebtoB/Nginx/Apache/IIS/WAS/LB/WAF/CDN/K8s 등 환경별 deployment adapter
- 인증서 파일/secret 전달
- bind/config 변경
- reload/restart
- rollback
- 실제 endpoint post-check
- 상용 CA / Private CA 확대

# 6. Phase 1 기술 아키텍처

## 6.1 상위 구성

```text
[Network Segment A] -- Sensor A --\
[Network Segment B] -- Sensor B ----> [Central Management Server]
[DMZ / IDC / VPC] -- Sensor C -----/       |
                                           +-- Discovery Engine
                                           +-- Inventory / Topology
                                           +-- Risk / Policy Engine
                                           +-- Alert / Workflow
                                           +-- Admin Portal / API
```

`DECISION`

- SaaS 연결은 필수가 아니다.
- 고객 내부망만으로 운영 가능한 On-Prem First 구조를 우선한다.
- sensor와 central server는 VM 또는 container 형태를 기본 후보로 한다.
- 초기 센서는 private key를 수집하지 않는다.
- 네트워크 스캔은 TLS handshake 중심으로 제한하고 scan rate/window/allowlist/denylist를 지원한다.

# 7. Discovery Engine 핵심 설계

## 7.1 중요한 원칙: context path는 중요하지 않다

`FACT`

TLS 인증서 교환은 HTTP application request보다 먼저 수행된다.

따라서 다음은 인증서 탐지의 핵심 정보가 아니다.

- `/`
- `/api`
- `/context/path`

ROOT에서 서비스를 제공하지 않아도 TLS handshake 단계에서 인증서를 받을 수 있다.

Web Server 없이 WAS가 직접 HTTPS/TLS를 terminate하는 경우도 동일한 원리가 적용된다.

## 7.2 중요한 원칙: IP:PORT만으로는 충분하지 않다

`FACT`

SNI(Server Name Indication)를 사용하는 서버/WAF/LB에서는 같은 `IP:PORT`에서도 ClientHello에 포함된 hostname에 따라 다른 인증서를 반환할 수 있다.

예시:

```bash
openssl s_client \
  -connect 1.2.3.4:443 \
  -servername service.example.com
```

`DECISION`

Discovery의 기본 단위는 단순 `IP:PORT`가 아니라 다음과 같이 정의한다.

```text
Endpoint Identity = IP + Port + Protocol + SNI(optional)
```

## 7.3 권장 Discovery Pipeline

```text
1. CIDR / IP Discovery
        ↓
2. Port Probe
        ↓
3. TLS Detection
        ↓
4. Default TLS Handshake
        ↓
5. Hostname Candidate Collection
   - Internal DNS
   - CMDB
   - Service Registry
   - LB / WAF
   - K8s Ingress
   - CSV / Manual Import
        ↓
6. FQDN → DNS → IP correlation
        ↓
7. SNI별 TLS Re-Probe
        ↓
8. Certificate Fingerprint Correlation
        ↓
9. Certificate Topology 생성
```

## 7.4 같은 IP:PORT에서 여러 인증서가 존재하는 예

```text
10.1.2.3:443
  |
  +-- default handshake
  |     -> cert-default
  |
  +-- SNI=service-a.internal.example.com
  |     -> cert-A
  |
  +-- SNI=service-b.internal.example.com
        -> cert-B
```

`RISK`

“내부망 센서라서 IP:PORT만 스캔하면 인증서를 모두 찾을 수 있다”는 가정은 틀리다.

`DECISION`

내부망의 강점은 SNI 문제가 사라지는 것이 아니라, 내부 DNS/CMDB/LB/K8s/Service Registry 등 hostname 후보를 얻을 수 있어 SNI probe의 coverage를 높일 수 있다는 점이다.

# 8. Certificate Topology / 관계 모델

## 8.1 핵심 개념

인증서는 단순 파일이 아니라 서비스 의존성 그래프의 한 노드다.

```text
Domain / FQDN
      N
      |
      M
Endpoint(IP:Port:SNI)
      N
      |
      M
Certificate
      |
      +-- Service
      +-- Owner
      +-- Policy
      +-- Alert / Event
```

## 8.2 Certificate와 Installation 분리

`DECISION`

같은 인증서가 여러 endpoint에 설치될 수 있으므로 certificate identity와 installation instance를 분리한다.

### certificate

권장 필드:

```yaml
certificate:
  id: internal-id
  fingerprint_sha256: string
  serial_number: string
  issuer: string
  subject_cn: string|null
  san_dns: [string]
  not_before: datetime
  not_after: datetime
  public_key_algorithm: string
  public_key_size: integer|null
  signature_algorithm: string
  chain_hash: string|null
```

### endpoint

```yaml
endpoint:
  id: internal-id
  ip: string
  port: integer
  protocol: string
  sni: string|null
  fqdn: string|null
  network_segment: string|null
```

### installation

```yaml
installation:
  endpoint_id: string
  certificate_id: string
  observed_at: datetime
  source_sensor_id: string
  discovery_method: active_scan|sni_probe|connector|manual_import|agent
  chain_validation_status: string
```

### ownership

```yaml
ownership:
  service_id: string
  team_id: string|null
  owner_user_id: string|null
  escalation_policy_id: string|null
  maintenance_window: string|null
  business_criticality: critical|high|medium|low|unknown
```

# 9. Discovery Coverage와 한계

## 9.1 네트워크 Active Scan으로 비교적 잘 찾는 영역

- 접근 가능한 HTTPS/TLS endpoint
- 443, 8443 및 고객 지정 포트
- LB VIP
- 내부/외부 웹서비스
- WAS direct HTTPS
- 일부 raw TLS 서비스

## 9.2 추가 정보가 필요한 영역

- SNI 기반 virtual host
- 비표준 포트
- 방화벽 정책상 sensor 접근이 제한된 endpoint
- WAF/LB/CDN managed certificate
- Kubernetes Ingress / Gateway
- Cloud managed listener

필요한 보완 방식:

- 내부 DNS
- CMDB
- LB/WAF API 또는 inventory import
- K8s API
- Service Registry
- 수동 CSV/Excel import

## 9.3 네트워크 스캔만으로 찾기 어려운 영역

- filesystem에만 저장된 PEM/P12/PFX
- Java JKS/PKCS#12 keystore
- application truststore
- mTLS client certificate
- 아직 서비스에 bind되지 않은 인증서
- 오프라인/비활성 시스템의 인증서

`DECISION`

초기 제품 마케팅 문구는 “모든 인증서 자동 발견”이 아니라 다음처럼 제한한다.

> Network-reachable TLS certificate discovery + DNS/SNI 기반 보조 discovery + 확장 connector

# 10. WebtoB 대응

## 10.1 탐지 관점

`FACT`

TmaxSoft WebtoB는 SSL 설정을 통해 인증서 파일과 키 파일을 지정하며, virtual host / SSL 설정과 연계할 수 있다. WebtoB 5 계열에서는 SNI를 통한 단일 포트의 복수 도메인 서비스 구성이 가능하다.

`IMPLICATION`

WebtoB 환경에서도 단순 `IP:PORT` 스캔만으로는 모든 virtual host 인증서를 확인할 수 없을 수 있다.

`DECISION`

Phase 1에서는 WebtoB를 일반 TLS endpoint로 active scan하고, DNS/SNI probe로 실제 인증서를 식별한다.

## 10.2 관리/교체 관점

Phase 1.5 후보:

- WebtoB 설정 파일 read-only parser
- `http.m` 등 설정에서 SSL/VHOST/certificate 관련 정보 추출
- active scan 결과와 설정 정보를 correlation
- 실제 사용 중인 인증서와 설정상 인증서의 차이 탐지

Phase 2/3 후보:

- 인증서/키 파일 교체 adapter
- 설정 validation
- reload/restart workflow
- maintenance window
- rollback
- post-deployment TLS verification

`RISK`

버전/배포 형태/고객 운영 표준에 따라 WebtoB 설정 구조와 재기동 절차가 다를 수 있으므로 “WebtoB 자동 교체 지원”을 MVP 약속으로 잡지 않는다.

# 11. Penta Security WAPPLES / WAF 대응

## 11.1 탐지 관점

내부 스캔과 외부 서비스 인증서 불일치 대응

`TECHNICAL_RISK`

내부망에서 Origin Server를 직접 스캔할 경우 서버에 설치된 인증서를 정상적으로 확인할 수 있다.

그러나 외부 사용자 요청 경로에서는 TLS가 Origin Server까지 직접 전달되지 않고 다음 장비에서 종료(TLS Termination)될 수 있다.

- WAF
- Load Balancer
- ADC
- Reverse Proxy
- CDN
- SSL VPN / SSL Inspection Gateway
- TLS Proxy

따라서 동일한 도메인이라도 내부에서 확인한 인증서와 외부에서 실제 사용자에게 제공되는 인증서가 서로 다를 수 있다.


text

Internal Scan

Sensor
  |
  v
Origin Server
Certificate B

External Access

Internet
  |
  v
WAF / LB / Reverse Proxy
Certificate A
  |
  v
Origin Server
Certificate B

RISK_EXAMPLE

Origin Server 인증서: 정상 갱신 완료
WAF/LB 인증서: 이전 인증서 유지
내부 스캔 결과: 정상
외부 사용자 관점: 만료 또는 구버전 인증서 노출
결과: 실제 서비스 장애 가능



인증서 점검은 내부 스캔과 외부 관점 점검을 분리해야 한다.

Internal Certificate Check
네트워크 Sensor에서 Origin Server 직접 점검
IP:PORT 기반 TLS 탐색
DNS/SNI 기반 Virtual Host 인증서 탐색
내부 서버 및 서비스 인증서 Inventory 생성
External Certificate Check
외부 인터넷 또는 별도 External Probe에서 실제 서비스 FQDN으로 접속
실제 사용자 경로에서 반환되는 인증서 확인
SNI 기반 TLS Handshake 수행
WAF/LB/CDN/Reverse Proxy 등 Edge 구간 인증서 확인
Certificate Consistency Check

동일 FQDN에 대해 내부 인증서와 외부 인증서를 비교한다.

FQDN
 |
 +-- Internal Probe
 |     -> Origin Certificate
 |
 +-- External Probe
       -> Edge Certificate

비교 항목:

Certificate Fingerprint
Serial Number
Subject / CN
SAN
Issuer
Not Before
Not After
Certificate Chain

PRODUCT_REQUIREMENT

제품은 최소 다음 두 종류의 인증서 상태를 구분하여 관리해야 한다.

Origin Certificate
Edge Certificate

예시 데이터 모델:

service.example.com
  |
  +-- External Endpoint
  |     WAF:443
  |     Certificate: cert-edge-001
  |
  +-- Internal Endpoint
        10.10.10.20:443
        Certificate: cert-origin-002

KEY_MESSAGE

내부 스캔만으로는 실제 외부 사용자에게 노출되는 인증서를 완전히 검증할 수 없다.

따라서 제품은 다음 구조를 지원하는 것이 필요하다.

Internal Discovery
        +
External Validation
        +
Certificate Correlation
        =
End-to-End Certificate Visibility



`FACT`

WAF가 TLS를 terminate하는 구성에서는 외부/클라이언트 관점의 서비스 인증서는 origin Web Server/WAS가 아니라 WAF listener에 존재할 수 있다.

예시:

text
Client
  |
  | TLS #1 / cert-public
  v
WAF / WAPPLES
  |
  | HTTP 또는 TLS #2 / cert-origin
  v
WebtoB / Nginx / WAS


`IMPLICATION`

한 서비스에도 서로 다른 인증서 계층이 존재할 수 있다.

- public-side certificate
- WAF-to-origin certificate
- origin local certificate

## 11.2 WAPPLES 대응 원칙

`DECISION`

Phase 1:

- WAF listener를 TLS endpoint로 active scan
- SNI별 certificate 수집
- endpoint에 vendor/type tag 부여 가능
- WAF 뒤 origin endpoint가 sensor에서 접근 가능하면 별도로 스캔
- 수동 inventory import 지원

Phase 1.5:

- WAF/LB 공통 inventory connector schema 정의
- 고객 환경에서 listener/domain/certificate mapping import
- Penta Security가 제공하는 고객별 관리/API 인터페이스가 확인되면 전용 connector 검토

Phase 2/3:

- WAF 장비별 certificate deployment adapter
- change validation
- reload/apply 여부 확인
- post-check
- rollback

`RISK`

공개 문서만으로 고객이 사용하는 WAPPLES 버전의 인증서 등록/교체 API, 파일 구조, 운영 자동화 인터페이스를 일반화하면 안 된다.

따라서 고객 환경 인터뷰 및 PoC에서 다음을 확인해야 한다.

1. WAPPLES 버전과 배포 형태
2. 인증서 등록 방식
3. private key 저장 위치/방식
4. CLI/API/관리자 UI 지원 범위
5. 변경 적용 시 서비스 영향
6. 이중화/HA 환경에서 인증서 동기화 방식
7. listener/VHOST와 인증서 mapping 구조

# 12. Capability Matrix 설계

제품은 모든 환경을 동일 수준으로 지원한다고 주장하지 않는다.

각 플랫폼별 capability를 명시적으로 관리한다.

| Platform | Discover | Inventory Correlation | Issue/Renew | Deploy | Reload/Apply | Verify |
|---|---|---|---|---|---|---|
| Generic TLS Endpoint | Yes | Yes | Later | No | No | Yes |
| Nginx | Yes | Later | Later | Phase 3 | Phase 3 | Yes |
| Apache | Yes | Later | Later | Phase 3 | Phase 3 | Yes |
| IIS | Yes | Later | Later | Phase 3 | Phase 3 | Yes |
| WebtoB | Yes via TLS/SNI | Phase 1.5 parser candidate | Later | Adapter candidate | Adapter candidate | Yes |
| WAPPLES/WAF | Yes on listener | Connector candidate | Vendor dependent | Vendor dependent | Vendor dependent | Yes |
| Kubernetes Ingress | Yes + K8s connector | Strong candidate | cert-manager/ACME | Secret update | Controller dependent | Yes |
| Cloud LB/CDN | Public/private endpoint scan | API connector | Provider dependent | API dependent | Usually managed | Yes |

# 13. Phase 1 MVP 기능 명세

## 13.1 Must Have

```yaml
mvp_must_have:
  discovery:
    - CIDR/IP range scan
    - configurable port scan
    - TLS endpoint detection
    - default certificate collection
    - DNS/FQDN candidate import
    - SNI probe
    - certificate chain parsing
    - SAN/CN/issuer/fingerprint/validity extraction
  inventory:
    - certificate inventory
    - endpoint inventory
    - domain/FQDN inventory
    - certificate-to-endpoint relationship
    - shared certificate detection
    - owner/service tagging
  risk:
    - expiry buckets
    - unowned certificate
    - unauthorized CA
    - weak algorithm policy
    - certificate change detection
  alert:
    - D-90
    - D-60
    - D-30
    - D-14
    - D-7
    - escalation
  integration:
    - SMTP/email
    - generic webhook
    - CSV import/export
    - REST API
  audit:
    - scan history
    - alert history
    - acknowledgement history
    - ownership change history
```

## 13.2 Should Have

- Slack/Teams integration
- Jira/ServiceNow integration
- CMDB connector
- Kubernetes connector
- LB/WAF inventory import
- policy report
- scan coverage report
- risk score

## 13.3 Non-Goals

`NON_GOAL`

MVP에서는 다음을 하지 않는다.

- private key 중앙 수집
- 모든 서버에 agent 설치
- 인증서 자동 배포
- Web Server/WAS/WAF 자동 reload/restart
- 모든 filesystem/keystore deep scan
- CA/PKI 자체 대체

# 14. 보안 및 비기능 요구사항

```yaml
non_functional_requirements:
  deployment:
    on_prem_only_mode: required
    vm_package: preferred
    container_package: preferred
    offline_environment_support: required_for_enterprise
  data_security:
    collect_private_key: false
    metadata_minimization: true
    encryption_at_rest: required
    encryption_in_transit: required
  access_control:
    rbac: required
    audit_log: required
    admin_separation: recommended
  scanner_safety:
    rate_limit: required
    scan_window: required
    allowlist: required
    denylist: required
    handshake_only_mode: required
  update_security:
    signed_artifacts: required
    offline_update: recommended
  operability:
    high_availability: post_mvp_or_enterprise
    backup_restore: required
    observability: required
```

# 15. Phase 2 ACME / CA Architecture

## 15.1 구조

```text
ACME Clients
(certbot / cert-manager / appliance / custom client)
        |
        v
Internal ACME-compatible RA / Gateway
        |
        +-- Policy / Approval
        +-- Account / EAB / Credential
        +-- Audit
        |
        v
CA Adapter Abstraction Layer
        |
        +-- Let's Encrypt / Upstream ACME
        +-- Commercial CA APIs / ACME
        +-- Private CA / MSCA / step-ca / Vault etc.
```

## 15.2 중요한 기술 판단

`DECISION`

“우리가 ACME Server를 운영하고 Let’s Encrypt를 backend CA로 둔다”는 표현은 기술적으로 단순화되어 있다.

더 현실적인 표현은 다음이다.

> 내부에서 ACME-compatible RA/Gateway 역할을 제공하고, upstream CA에 대해 별도의 ACME/API order를 수행하는 구조

`FACT`

Let’s Encrypt의 domain validation, rate limit, CAA, CT logging, account/key 정책은 upstream CA의 정책을 따른다.

상용 CA는 다음에 따라 adapter 구현이 달라질 수 있다.

- ACME 지원 여부
- External Account Binding(EAB)
- 계약 방식
- 인증서 profile
- 조직 검증
- API credential

# 16. 중요한 기술 경고: 발급과 배포는 다르다

`FACT`

ACME 또는 CA API로 새 인증서를 발급했다고 해서 실제 서비스가 새 인증서를 사용하게 되는 것은 아니다.

자동화 lifecycle:

```text
Issue
  ↓
Deliver
  ↓
Bind / Configure
  ↓
Reload / Restart / Apply
  ↓
Post-deployment Verify
  ↓
Rollback if needed
```

`DECISION`

기능 단계를 분리한다.

- Phase 1 = 발견/인벤토리/알림
- Phase 2 = 발급/갱신 오케스트레이션
- Phase 3 = 실제 배포/적용/검증

# 17. 제품 포지셔닝

## 17.1 경쟁 구도

대형 엔터프라이즈 CLM / machine identity 제품 예시:

- DigiCert Trust Lifecycle Manager
- CyberArk / Venafi
- Keyfactor
- Sectigo Certificate Manager
- AppViewX CLM

경량 또는 특정 영역 도구 예시:

- cert-manager
- OpenSSL
- Nmap
- 자체 스크립트

`DECISION`

초기 포지션은 Full CLM 대체가 아니다.

> 작고 안전하게 시작하는 On-Prem Certificate Discovery / Inventory / Expiry Risk Management

주요 차별화 가설:

- On-Prem First
- agentless 우선
- 내부 DNS/SNI correlation
- certificate topology
- shared certificate / blast radius 가시화
- 낮은 PoC 마찰
- 추후 자동화로 확장 가능한 데이터 기반

# 18. 핵심 화면

## 18.1 Executive Risk Dashboard

표시 정보:

- 전체 인증서 수
- 전체 endpoint 수
- 전체 FQDN/domain 수
- D-90/D-60/D-30/D-14/D-7
- critical service의 만료 예정 인증서
- owner 미지정 인증서
- 최근 certificate change
- shared certificate 상위 목록
- scan coverage

## 18.2 Certificate Inventory

컬럼 예시:

```text
fingerprint
subject/SAN
issuer
not_after
remaining_days
endpoint_count
service_count
owner
criticality
risk_status
last_observed_at
```

## 18.3 Certificate Topology / Dependency Map

질문에 답해야 한다.

> 이 인증서가 만료되면 어떤 서비스와 endpoint가 영향을 받는가?

## 18.4 Alert Inbox

- alert status
- owner
- ack
- snooze
- escalation
- ticket URL
- remediation status

# 19. 프로젝트 실행 계획

## 19.1 목표 일정

`ASSUMPTION`

Discovery/Inventory/Alert 범위에 한정할 경우 20주 안에 파일럿 가능한 MVP를 목표로 한다.

| 단계 | 기간 | 목표 |
|---|---:|---|
| 가설 검증 | 2주 | ICP 인터뷰 10~15건, 요구사항 검증 |
| PoC | 4주 | scanner/SNI probe/parser/basic inventory |
| MVP | 8주 | dashboard/ownership/alert/report/API |
| Pilot | 4주 | 고객 2~3곳 설치 및 scan accuracy 검증 |
| GA 준비 | 2주 | packaging/security review/pricing/contracts |

## 19.2 팀 가정

```yaml
team_assumption:
  product_manager: 1
  backend_engineer: 2
  frontend_engineer: 1
  sensor_infra_engineer: 1
  security_architect: 0.5
  qa_devops: 0.5
```

## 19.3 KPI

- 발견 인증서 수
- 고객 기존 inventory 대비 신규 발견 인증서 비율
- SNI별 인증서 관계 정확도
- shared certificate detection 정확도
- owner 미지정 인증서 감소율
- alert delivery rate
- alert acknowledgement rate
- expiry incident/near-miss 감소
- PoC → 유료 전환률

# 20. ICP / Buyer / User

## ICP

- 관리 인증서·도메인·서비스 수가 많은 조직
- 서비스 중단 비용이 큰 조직
- 온프레미스/망분리 요구가 강한 조직
- 보안팀과 인프라/SRE 책임이 분산된 조직

산업 후보:

- 금융
- 공공
- 제조
- 통신
- 대형 SaaS / 플랫폼

## Buyer

- CISO
- IT 운영 책임자
- 인프라 책임자

## User

- 보안운영
- SRE
- 네트워크
- 서버/웹/WAS 운영자
- 서비스 담당자

## Champion

인증서 만료 사고 또는 교체 작업을 직접 처리하는 운영 리더.

# 21. 가격/수익모델 가설

가능한 과금 단위:

- 관리 certificate 수
- managed endpoint 수
- domain/FQDN 수
- sensor 수
- tier별 subscription
- annual maintenance/support
- Phase 2 automation module add-on
- Phase 3 vendor deployment adapter add-on

`HYPOTHESIS`

초기 가격은 Full CLM보다 낮은 저마찰 구간으로 설정하여 PoC→도입을 쉽게 하고, 2/3단계 automation에서 ARPU를 확장한다.

# 22. GTM 메시지 후보

1. `47일 인증서 시대 전에 숨은 인증서를 찾아라.`
2. `SaaS 반출 없이 내부망에서 시작하는 인증서 가시성.`
3. `엑셀 만료관리에서 중앙 통제·감사 가능한 운영으로.`
4. `어떤 인증서가 어디에 쓰이는지 모르면 자동 갱신도 안전하지 않다.`
5. `Discovery first, automation next.`

# 23. 주요 사업/기술 리스크

```yaml
risks:
  - id: R1
    type: technical
    risk: network scan coverage가 고객 기대보다 낮을 수 있음
    mitigation: DNS/SNI/CMDB/K8s/LB/WAF connector와 coverage report 제공
  - id: R2
    type: product
    risk: 단순 expiry monitoring만으로 예산 확보가 어려울 수 있음
    mitigation: topology, ownership, blast radius, audit로 가치 확대
  - id: R3
    type: market
    risk: 이미 Full CLM을 사용하는 대기업에는 wedge가 약함
    mitigation: 미도입/부분도입 조직 또는 isolated network/사업부 단위 공략
  - id: R4
    type: deployment
    risk: 망별 sensor 설치가 보안 심사로 인해 생각보다 무거울 수 있음
    mitigation: no-private-key, handshake-only, signed image, offline deployment
  - id: R5
    type: automation
    risk: WebtoB/WAF/LB 등 환경별 인증서 교체 adapter 비용이 폭증할 수 있음
    mitigation: capability matrix와 adapter prioritization, customer-backed integrations
  - id: R6
    type: architecture
    risk: ACME issuance를 deployment automation으로 오해할 수 있음
    mitigation: Phase 2 issuance와 Phase 3 deployment를 명확히 분리
```

# 24. 고객 인터뷰 질문

1. 현재 전체 인증서 수를 1시간 이내에 정확히 답할 수 있는가?
2. 전체 domain/FQDN 수와 인증서 수가 어떻게 연결되는가?
3. 동일 인증서가 어디에 복제되어 있는지 추적 가능한가?
4. 최근 12개월 동안 인증서 만료/오배포 사고 또는 near miss가 있었는가?
5. 인증서 만료 알림은 어디서 발생하고 누구에게 전달되는가?
6. 담당자가 미조치하면 자동 escalation 되는가?
7. WAF/LB/WebtoB/WAS/K8s 등 TLS termination 지점은 어떻게 관리하는가?
8. 내부망 인증서를 외부 SaaS CLM에 등록할 수 있는가?
9. SaaS CLM 또는 Full CLM을 도입하지 않은 이유는 무엇인가?
10. 인증서 자동 갱신보다 먼저 해결하고 싶은 문제는 무엇인가?
11. PoC에서 어떤 숨은 인증서/위험을 발견하면 구매를 논의할 수 있는가?
12. WAPPLES/WebtoB 등 국산 미들웨어의 인증서 교체는 현재 어떤 runbook으로 수행하는가?

# 25. Go / No-Go 기준

`HYPOTHESIS_TO_VALIDATE`

Go 판단을 위한 최소 조건 예시:

1. 최소 3개 ICP에서 다음 세 가지가 동시에 확인됨.
   - 현재 인증서 현황 파악이 어렵다.
   - 만료/교체 운영이 불안하다.
   - On-Prem 또는 내부망 솔루션 선호가 있다.
2. PoC에서 고객이 기존에 알지 못한 다음 중 하나 이상을 실제 발견함.
   - 숨은 인증서
   - 곧 만료되는 인증서
   - owner 없는 인증서
   - 동일 인증서의 다중 endpoint 배포
   - SNI별 별도 인증서
3. 발견 결과를 기반으로 유료 Pilot 또는 구매 논의가 진행됨.

# 26. 단기 실행 항목

```yaml
next_90_days:
  - customer_interviews: 10-15
  - scanner_poc: true
  - sni_probe_poc: true
  - webtob_config_parser_poc: true
  - waf_inventory_schema: true
  - wapples_customer_environment_interviews: true
  - pilot_candidates: 2-3
```

PoC 핵심 검증 항목:

- CIDR/port scan 안전성
- default cert 수집
- DNS/FQDN 후보 기반 SNI cert 수집
- certificate fingerprint deduplication
- 동일 certificate의 다중 endpoint correlation
- WebtoB SNI 환경 탐지
- WAF listener 탐지
- 만료 알림/ownership/escalation

# 27. 권장 기술 스택 후보

`ASSUMPTION`

초기 후보이며 확정 아님.

```yaml
technology_candidates:
  sensor:
    language: [Go, Rust]
  backend:
    database: PostgreSQL
    api: [REST, GraphQL]
  frontend:
    framework: React
  packaging:
    - container
    - virtual_machine
```

PostgreSQL을 사용하더라도 graph database를 반드시 도입할 필요는 없다. certificate/endpoint/service/owner 관계를 relational schema로 먼저 구현하고 실제 쿼리 복잡도와 규모가 확인된 이후 graph database 도입을 판단한다.

# 28. 핵심 제품 원칙

```yaml
product_principles:
  - discovery_before_automation
  - on_prem_first
  - agentless_first_not_agentless_only
  - no_private_key_collection_in_mvp
  - model_certificate_as_dependency_graph
  - separate_certificate_identity_from_installation
  - sni_aware_discovery
  - explicit_coverage_limits
  - separate_issuance_from_deployment
  - adapters_are_capabilities_not_assumptions
  - customer_evidence_before_heavy_integrations
```

# 29. AI가 반드시 유지해야 할 기술적 정정 사항

다음 내용은 후속 문서/PPT/개발명세에서 잘못 단순화하면 안 된다.

1. `context path를 알아야 인증서를 탐지할 수 있다` → 틀림. TLS handshake가 HTTP 요청보다 먼저다.
2. `ROOT(/) 서비스가 없으면 인증서를 못 받는다` → 틀림.
3. `Web Server가 없으면 인증서를 못 탐지한다` → 틀림. WAS/WAF/LB가 직접 TLS terminate할 수 있다.
4. `IP:PORT당 인증서는 하나다` → 틀림. SNI에 따라 여러 인증서가 가능하다.
5. `내부망에서 스캔하므로 SNI 문제는 없다` → 틀림. 내부망은 hostname 후보 정보에 접근하기 쉬울 뿐이다.
6. `망별 센서만 설치하면 모든 인증서를 찾는다` → 과장. network-reachable certificate 중심이다.
7. `ACME로 발급하면 자동으로 서버 인증서가 교체된다` → 틀림. deliver/bind/reload/verify가 별도다.
8. `WebtoB/WAPPLES를 generic server처럼 자동교체할 수 있다` → 검증 전에는 주장 금지. 제품별 adapter와 고객 환경 검증이 필요하다.

# 30. 근거 자료 목록

아래 자료가 기존 기획안의 주요 근거다.

1. CA/Browser Forum, Ballot SC081v3: Introduce Schedule of Reducing Validity and Data Reuse Periods, 2025-04-11
2. DigiCert, TLS Certificate Lifetimes Will Officially Reduce to 47 Days
3. DigiCert Trust Lifecycle Manager documentation
4. IETF RFC 8555, Automatic Certificate Management Environment (ACME), 2019
5. Let’s Encrypt Boulder/Pebble ACME server implementation
6. Smallstep step-ca documentation
7. cert-manager documentation
8. Microsoft Teams 2020 expired certificate outage 관련 공개 보도
9. Ericsson/O2/SoftBank 2018 certificate-related outage 관련 공개 보도
10. Equifax breach investigation / expired monitoring certificate 관련 공개 자료
11. CyberArk의 Venafi 인수 관련 공식 자료, 2024
12. TmaxSoft WebtoB 공식 문서: SSLFlag/SslName, SSL Section, SNI 관련 문서/릴리스 노트
13. Penta Security WAPPLES 공식 자료: HTTPS, TLS 1.3, HTTP/2, WAF/WAAP 관련 자료
14. IETF RFC 6066, Server Name Indication
15. IETF RFC 8446, TLS 1.3
16. Keyfactor Command 공식 자료
17. Sectigo Certificate Manager 공식 자료
18. AppViewX CLM 공식 자료

# 31. 최종 제안

`DECISION`

초기 신규사업은 다음 범위로 추진하는 것이 가장 현실적이다.

> On-Prem Sensor 기반 TLS Certificate Discovery + DNS/SNI Correlation + Certificate Topology + Expiry/Ownership Management

초기 제품의 경쟁력은 “인증서를 발급하는 기능” 자체가 아니라 다음 네 가지에 있다.

1. 고객이 몰랐던 TLS endpoint와 인증서를 찾는다.
2. 같은 인증서가 어디에 얼마나 넓게 배포되어 있는지 보여준다.
3. 서비스와 소유자를 연결해 만료 리스크를 실제 담당자에게 전달한다.
4. 이후 ACME/CA API 및 WebtoB/WAF/LB별 adapter 자동화를 위한 정확한 inventory를 먼저 구축한다.

이 데이터를 확보한 뒤 자동화 범위를 넓히는 것이 기술 리스크와 초기 도입 마찰을 모두 낮춘다.
