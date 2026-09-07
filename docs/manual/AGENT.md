# 배포 에이전트 운영

지원 대상은 Linux의 nginx·Apache(apache2/httpd)다. 기존 수집 센서와 별도 바이너리·신원·작업 테이블을 사용한다. Windows·IIS·WebtoB는 이번 배포 지원 범위에 포함하지 않는다. 적용 동작은 `reload`이며 전체 서비스 restart는 제공하지 않는다.

## 서버 준비

관리 서버에 `SENSOR_CA_ENC_KEY`와 `ISSUANCE_ENC_KEY`를 설정한다. 각각 독립적인 32바이트 키의 64자리 hex 값이다. 키를 생성한 운영자가 안전한 비밀 저장소에 보관하고 서버 환경에 주입한다. DB와 함께 두 키를 별도 백업한다. 발급 키를 분실하면 저장된 CA 자격증명과 발급 개인키를 복구할 수 없다. `.env.example`에는 운영자가 직접 `ISSUANCE_ENC_KEY=` 항목을 추가한다.

에이전트는 서버의 기존 mTLS 포트(기본 8443)로 outbound 연결한다. `SENSOR_ADVERTISED_HOSTS`에 에이전트가 사용하는 서버 DNS/IP를 설정한다. 에이전트와 센서 인증서는 각각의 등록 테이블로 대조하여 상호 대체할 수 없다. 설계의 작업 서명·검증은 이 구현에서 mTLS 채널 인증과 폐쇄형 작업 enum 검증으로 수행한다.

## 빌드·설치·등록

Go 1.25 이상인 빌드 환경에서 대상 아키텍처에 맞게 정적 바이너리를 만든다.

```sh
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/autocert-agent-linux-amd64 ./cmd/agent
```

ARM64 서버는 `GOARCH=arm64`로 빌드한다. 바이너리와 `scripts/install-agent.sh`를 대상 서버에 전달하고 실행한다.

```sh
sudo sh install-agent.sh ./autocert-agent-linux-amd64 https://SERVER:8443
```

콘솔 **배포 에이전트 → 등록 토큰 발급**에서 이름을 입력한다. 토큰은 한 번만 표시되며 24시간 후 만료된다. 대상 서버에 권한 0600인 토큰 파일로 전달하고, 콘솔에서 제공한 CA 지문과 함께 등록한다. 토큰 문자열을 명령행 인자로 넘기지 않는다. `--token-file -`는 stdin 입력도 지원한다.

```sh
sudo autocert-agent enroll --server https://SERVER:8443 --token-file /root/agent-token --ca-fingerprint CA_SHA256
sudo systemctl enable --now autocert-agent
```

등록을 확인한 뒤 토큰 파일을 삭제한다. 상태 디렉토리 `/var/lib/autocert-agent`에는 에이전트 자신의 mTLS 신원만 저장한다. 고객 인증서 개인키를 이 디렉토리에 보관하지 않는다. 클라이언트 인증서는 만료 30일 전부터 mTLS 갱신한다.

## 대상 설정·배포

발급 주문 상세에서 **새 대상 등록**을 눌러 에이전트·서비스·기존 인증서/키 파일·로컬 검증 포트·SNI를 지정한다. 대상 파일은 이미 존재해야 하며, 기존 키와 인증서가 일치하고 실제 서비스의 지문과도 일치해야 한다. 심볼릭 링크는 거부한다. 기본 허용 디렉토리는 `/etc/nginx,/etc/apache2,/etc/httpd,/etc/ssl`이다. 다른 디렉토리는 systemd `ExecStart`의 `--allow-dirs`에 명시적으로 설정한다. `/`는 허용 루트로 사용할 수 없다. 검증 IP는 루프백 주소만 허용한다.

nginx는 인증서 파일에 leaf+chain을 쓰고, Apache는 별도 체인 경로가 있으면 분리 저장한다. 없으면 인증서 파일에 번들로 쓴다. 개인키는 0600, 인증서는 0644로 저장하며 원래 파일 소유자를 보존한다. 사전 설정 검사 → 백업 → 같은 디렉토리 임시 파일/fsync → rename 교체 → 설정 검사 → reload → 로컬 TLS 지문 검증 순으로 실행한다. 성공 시 백업을 정리한다. 임의 셸 문자열이나 사용자 제공 명령은 실행하지 않는다.

## 실패·복원

교체 이후 설정 검사·reload·검증에 실패하면 백업 복원, 설정 재검사, reload, 옛 지문 확인을 수행한다. 성공하면 `rolled_back`으로 보고한다. 복원도 실패하면 `failed`로 남으며 대상 디렉토리의 `<원본파일>.autocert-bak-*` 백업을 유지한다. 단계별 상태는 주문 상세에서 확인한다. 명령 stdout/stderr는 키 유출 방지를 위해 수집하지 않는다.

하트비트는 25초마다 전송하고 리스는 90초다. 만료된 잡은 실패로 처리하며 자동 재실행하지 않는다. 완료 보고는 동일 리스 토큰으로 멱등 처리한다. 개인키는 유효한 리스를 소유한 에이전트에게만 전용 material 엔드포인트로 전달되고, 수령마다 감사 이벤트를 기록한다.

에이전트가 강제 종료되면 `run.lock`이 남아 재실행을 막는다. 기존 프로세스가 없는지 확인하고, 대상 파일·백업·서비스 지문을 점검해 필요한 복원을 마친 뒤 해당 잠금 파일만 삭제하고 서비스를 시작한다. 호스트 강제 종료까지 파일 여러 개의 교체가 하나의 트랜잭션으로 보장되는 것은 아니므로 이 복구 절차가 필요하다.

## 검증 범위

자동 검증은 가짜 CA·실제 임시 PostgreSQL·가짜 서비스 실행기·임시 파일을 사용한다. 고객 nginx/Apache 호스트의 systemd·보안 정책(SELinux 등)과 실제 CA 계정의 발급·검증 흐름은 도입 전 환경에서 확인해야 한다. 기존 오프라인 번들 스크립트는 에이전트를 포함하지 않으므로 위 빌드 산출물을 별도로 반입한다. 자동 갱신·재배포(E)는 아직 실행되지 않는다.
