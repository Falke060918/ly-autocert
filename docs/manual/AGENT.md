# 배포 에이전트 운영

지원 대상은 Linux의 nginx·Apache(apache2/httpd)다. 기존 수집 센서와 별도 바이너리·신원·작업 테이블을 사용한다. Windows·IIS·WebtoB는 이번 배포 지원 범위에 포함하지 않는다. 적용 동작은 `reload`이며 전체 서비스 restart는 제공하지 않는다.

## 서버 준비

관리 서버에 `SENSOR_CA_ENC_KEY`와 `ISSUANCE_ENC_KEY`를 설정한다. 각각 독립적인 32바이트 키의 64자리 hex 값이다. 키를 생성한 운영자가 안전한 비밀 저장소에 보관하고 서버 환경에 주입한다. DB와 함께 두 키를 별도 백업한다. 발급 키를 분실하면 저장된 CA 자격증명과 발급 개인키를 복구할 수 없다. `.env.example`에는 운영자가 직접 `ISSUANCE_ENC_KEY=` 항목을 추가한다.

에이전트는 서버의 기존 mTLS 포트(기본 8443)로 outbound 연결한다. `SENSOR_ADVERTISED_HOSTS`에 에이전트가 사용하는 서버 DNS/IP를 설정한다. 에이전트와 센서 인증서는 각각의 등록 테이블로 대조하여 상호 대체할 수 없다. mTLS와 별도로 각 배포 작업을 Ed25519로 서명한다. 서명키가 미설정·손상된 서버는 작업 lease를 내주지 않는다.

서버 관리자는 기존 `ISSUANCE_ENC_KEY` 공급을 유지하면서 별도 작업 서명키를 최초 한 번 초기화한다. 아래 명령은 운영자가 준비할 절차이며 개발 검증에서 운영 키를 생성하지 않는다. `deploy-signing-key`는 `backend/cmd/deploy-signing-key`의 빌드 바이너리다. 대상 부모 디렉토리를 root 전용으로 준비한다.

```sh
deploy-signing-key init --file /etc/autocert/deploy-signing.sealed
deploy-signing-key public --file /etc/autocert/deploy-signing.sealed
```

공개키 내보내기는 64자리 hex 공개키만 출력한다. 관리자는 신뢰된 관리 경로로 그 값과 SHA-256 지문을 대조하여 대상의 `/etc/autocert-agent/job-signing.pub`에 한 줄로 설치한다. 등록 응답에서 받은 키를 자동 신뢰하지 않는다. 서버 설정에는 `DEPLOY_SIGNING_KEY_FILE=/etc/autocert/deploy-signing.sealed`를 지정한다. Docker 사용 시 같은 환경 항목과 파일의 read-only volume을 운영 compose override에 명시한다. 서명키는 AES-GCM으로 봉인된 채 보관하며 CA나 agent의 개인키를 재사용하지 않는다.

기존 CA 연결의 app_id 평문 행은 유지보수 창에서 0027 스키마 적용 후 `backend/cmd/issuance-migrate` 바이너리를 `DATABASE_URL`·기존 `ISSUANCE_ENC_KEY` 환경으로 실행한다. 활성·비활성 행 모두 잠금·복호화 검증·봉인·평문 NULL·감사를 한 트랜잭션으로 처리한다. 실패 시 전체 취소되며 재실행은 완료 행을 건너뛴다. 자동 시작 시 데이터 이행하지 않으며 미이행 연결 실행은 차단한다. 구버전 writer로 되돌리지 않는다. 기존 WAL·백업·스토리지 잔존 데이터 삭제까지 보장하는 절차는 아니다.

## 빌드·설치·등록

Go 1.25 이상인 빌드 환경에서 대상 아키텍처에 맞게 정적 바이너리를 만든다.

```sh
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/autocert-agent-linux-amd64 ./cmd/agent
```

ARM64 서버는 `GOARCH=arm64`로 빌드한다. 바이너리와 `scripts/install-agent.sh`를 대상 서버에 전달한다. 먼저 대상 root 관리자가 agent 전용 해제 키를 최초 한 번 만든다. 서버의 ISSUANCE_ENC_KEY를 대상에 전달하지 않는다.

```sh
sudo ./autocert-agent-linux-amd64 init-identity-key --key-file /etc/autocert-agent/identity.env
```

콘솔 **배포 에이전트 → 등록 토큰 발급**에서 이름을 입력한다. 토큰은 한 번만 표시되며 24시간 후 만료된다. 대상 서버에 권한 0600인 토큰 파일로 전달하고, 콘솔에서 제공한 CA 지문과 함께 등록한다. 토큰 문자열을 명령행 인자로 넘기지 않는다. `--token-file -`는 stdin 입력도 지원한다.

```sh
sudo ./autocert-agent-linux-amd64 enroll --server https://SERVER:8443 --token-file /root/agent-token --ca-fingerprint CA_SHA256 --identity-key-file /etc/autocert-agent/identity.env --job-signing-public-key-file /etc/autocert-agent/job-signing.pub
sudo sh install-agent.sh ./autocert-agent-linux-amd64 https://SERVER:8443 /etc/autocert-agent/identity.env /etc/autocert-agent/job-signing.pub
sudo systemctl enable --now autocert-agent
sudo systemctl restart autocert-agent
```

등록을 확인한 뒤 토큰 파일을 삭제한다. 상태 디렉토리 `/var/lib/autocert-agent`에는 봉인된 mTLS 신원과 공개 인증서·ID·작업 재전송 journal만 저장한다. 고객 인증서 개인키를 이 디렉토리에 보관하지 않는다. 클라이언트 인증서는 만료 30일 전부터 mTLS 갱신한다.

`identity.env`는 `AGENT_IDENTITY_ENC_KEY=<64자리 hex>` 한 줄인 **평문 운영 해제 비밀**이다. 디렉토리 root:root 0700, 파일과 공개키 pin은 root:root 0600으로 설치한다. 파일·부모 경로의 symlink와 state를 포함하는 설정 디렉토리는 거부한다. 신원 디렉토리와 별도 설정·백업 범위로 관리한다. 신원 백업만 유출되었을 때를 방어하며 root 침해·설정까지 포함한 전체 디스크나 백업 유출까지 방어하지 않는다. 파일 권한이 신원 개인키 암호화를 대체하는 것은 아니다.

서비스는 같은 파일을 `EnvironmentFile=`로 매 시작·재시작에 읽는다. 별도 supervisor·TPM이나 신규 systemd credential 기능을 요구하지 않는다. CLI 파일 인자와 환경값을 동시에 공급하면 거부하며 읽은 환경값은 즉시 제거한다. 배포 자식 프로세스에는 비밀값을 전달하지 않는다. 키 누락·오류 때 새 키를 자동 생성하지 않는다.

기존 설치의 업그레이드는 서비스를 중지한 뒤 기존 토큰·신원을 덮어쓰지 않고 이행한다.

```sh
sudo systemctl stop autocert-agent
sudo ./autocert-agent-linux-amd64 migrate-identity --state-dir /var/lib/autocert-agent --identity-key-file /etc/autocert-agent/identity.env
```

기존 key.pem과 인증서가 일치하는지 확인하고 봉인 임시 파일→fsync→재복호화 확인→rename·디렉토리 fsync 후에만 기존 key.pem을 제거한다. 중단되어 두 파일이 남으면 같은 명령으로 검증 후 정리한다. 잘못된 키로 재시도하면 원본을 보존한다. 평문 백업을 새로 만들지 않으며 구 백업·디스크 잔존 제거는 운영 책임이다. 기존 unit은 설치기가 덮어쓰지 않는다. 관리자가 바이너리를 교체하고 `EnvironmentFile=` 및 위 공개키 핀 인자를 unit에 추가한 뒤 `systemctl daemon-reload`·서비스 시작을 수행한다. 서버 signer와 agent pin/키를 함께 준비하는 유지보수 작업이며 unsigned 구버전 호환 폴백은 없다.

## 대상 설정·배포

발급 주문 상세에서 **새 대상 등록**을 눌러 에이전트·서비스·기존 인증서/키 파일·로컬 검증 포트·SNI를 지정한다. 대상 파일은 이미 존재해야 하며, 기존 키와 인증서가 일치하고 실제 서비스의 지문과도 일치해야 한다. 심볼릭 링크는 거부한다. 기본 허용 디렉토리는 `/etc/nginx,/etc/apache2,/etc/httpd,/etc/ssl`이다. 다른 디렉토리는 systemd `ExecStart`의 `--allow-dirs`에 명시적으로 설정한다. `/`는 허용 루트로 사용할 수 없다. 검증 IP는 루프백 주소만 허용한다.

웹서버 적용·임시·백업 개인키의 저장 예외 또는 서비스별 해제 방식은 아직 미답이다. 아래는 기존 구현의 동작 설명이며 운영 배포 승인이나 전체 보안 완료 판정이 아니다. nginx는 인증서 파일에 leaf+chain을 쓰고 Apache는 별도 체인 경로가 있으면 분리 저장한다. 개인키 파일 0600·인증서 0644 및 원래 소유자를 보존하는 기존 동작은 정책 결정 전까지 실배포 검증에서 제외한다. 사전 검사→백업→임시 파일/fsync→rename→설정 검사→reload→지문 검증·롤백은 가상 실행기로 검증한다.

## 실패·복원

교체 이후 설정 검사·reload·검증에 실패하면 백업 복원, 설정 재검사, reload, 옛 지문 확인을 수행한다. 성공하면 `rolled_back`으로 보고한다. 복원도 실패하면 `failed`로 남으며 대상 디렉토리의 `<원본파일>.autocert-bak-*` 백업을 유지한다. 단계별 상태는 주문 상세에서 확인한다. 명령 stdout/stderr는 키 유출 방지를 위해 수집하지 않는다.

작업 하트비트는 25초마다 전송하고 리스는 90초다. lease/start/작업 heartbeat/agent heartbeat는 변경과 감사를 같은 트랜잭션으로 처리하며 감사 실패 시 변경도 취소한다. material은 활성 agent의 running 상태·유효 lease에서만 제공하고 수령마다 감사한다. 서명은 고정 접두어와 payload 전체를 덮으며 버전·agent·대상·지문·시각·형식을 검증한다. 최초 수락 기한은 최대 90초, 미래 시계 허용은 30초다.

검증 후 job ID와 lease token의 해시를 `accepted-jobs.json`에 원자 저장·fsync한 뒤 start한다. `run.lock`에는 프로세스 파일 잠금을 잡아 동시 실행을 막고 프로세스 종료 시 잠금이 해제된다. 파일을 삭제해 잠금을 우회하지 않는다. 재시작 후 같은 payload는 journal로 거부하며 손상·쓰기 실패도 중단한다. 수락 기록 후 응답 손실·크래시는 재실행하지 않고 리스 만료 후 운영자가 서비스·백업·지문을 확인한다. journal을 지워 재시도하지 말고 콘솔에서 새로운 작업을 요청한다. 완료 보고의 기존 멱등성과 작업 재실행 허용은 다르다.

## 검증 범위

자동 검증은 가짜 CA·실제 임시 PostgreSQL·가짜 서비스 실행기·임시 파일을 사용한다. 고객 nginx/Apache 호스트의 systemd·보안 정책(SELinux 등)과 실제 CA 계정의 발급·검증 흐름은 도입 전 환경에서 확인해야 한다. 기존 오프라인 번들 스크립트는 에이전트를 포함하지 않으므로 위 빌드 산출물을 별도로 반입한다. 자동 갱신·재배포(E)는 아직 실행되지 않는다.
