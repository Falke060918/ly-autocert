# INHERIT AutoCert 센서 설치·운영 매뉴얼

> **문서 상태**: Phase 3a(센서·원격 수집기) 구현 완료 기준. 서버 설치·운영은 INSTALL.md / OPERATIONS.md 참조.

> **이 문서를 읽는 법**
> - 센서를 설치한다 → **0장만** 보면 된다. 명령을 복사해 붙여 넣으면 끝난다.
> - 센서가 서버에 안 붙는다 → **9장 트러블슈팅**.
> - 센서가 왜 필요한지, 무엇을 하는지 → 1장.
> - 스크립트를 쓸 수 없는 환경이다 → **4–6장**이 손으로 하는 전체 절차다.

## 0. 빠른 설치

`install-sensor.sh`(Linux) / `install-sensor.ps1`(Windows) 하나가 설치의 전부다. 사전 점검·등록·서비스 등록까지 스크립트가 맡는다. 두 스크립트는 **센서 패키지 루트**에 들어 있다 (저장소 소스 트리에서는 `scripts/` 아래다).

### 0.1 먼저 — 서버 쪽에서 센서 기능을 켠다 (한 번만)

센서 게이트웨이(기본 8443)는 **기본적으로 열려 있지 않다.** 서버에서 설치 스크립트를 "설정 변경" 모드로 실행해 센서를 "사용함"으로 바꾸면 필요한 값이 자동 설정되고 backend가 재기동된다.

```sh
bash scripts/install.sh
```

손으로 설정하려면 3장(`SENSOR_CA_ENC_KEY`·`SENSOR_ADVERTISED_HOSTS`)을 본다.

### 0.2 웹 콘솔에서 등록 명령을 복사한다

웹 콘솔 [센서] 화면 → **[센서 등록]** → 센서 이름 입력(예: `sensor-c-segment`) → 화면에 **enroll 명령**이 표시된다.

> **이 창을 닫으면 다시 볼 수 없다** (서버는 토큰의 해시만 저장한다). 표시된 **enroll 명령 전체를 그대로 복사**해 둔다 — 서버 주소·등록 토큰·CA fingerprint가 이미 들어 있고, 포트도 그 서버에 실제로 설정된 값이다.

### 0.3 센서 호스트에서 — 명령 2줄

센서 패키지를 반입해 푼 디렉터리에서 실행한다 (반입 절차는 4장). **패키지 루트에서 그대로 실행한다** — 스크립트와 바이너리가 같은 디렉터리에 있다.

**Linux** — `sensor_linux_<버전>.tar.gz`를 푼 `sensor_linux_<버전>/` 안에서

```sh
sha256sum -c SHA256SUMS
sudo bash install-sensor.sh
```

**Windows** (관리자 권한 PowerShell) — `sensor_windows_<버전>.zip`을 푼 폴더 안에서

```powershell
Get-ChildItem -Recurse -File | Unblock-File
powershell -ExecutionPolicy Bypass -File .\install-sensor.ps1
```

- **Linux는 `sudo`가 필수다.** systemd 유닛 등록과 `/usr/local/bin` 배치를 하므로, root가 아니면 사전 점검에서 실패하고 멈춘다.
- **Windows는 `-ExecutionPolicy Bypass`가 필수다.** 서명되지 않은 `.ps1`이라 기본 실행 정책에서 차단될 수 있다.
- **`Unblock-File`은 매체 경유 zip 때문이다.** 다른 PC에서 내려받아 옮긴 zip은 Mark-of-the-Web가 붙어 압축을 풀어도 파일마다 "차단됨" 표시가 남고, 그러면 `Bypass`를 줘도 스크립트가 실행되지 않는다. 파일 속성 창의 [차단 해제] 체크박스와 같은 동작이다.
- Windows의 바이너리 체크섬 대조는 `install-sensor.ps1`이 대신 수행한다. 손으로 하려면 `certutil` — 4.1절.
- 스크립트가 **"enroll 명령을 붙여넣으라"** 고 물으면 0.2에서 복사한 **명령 전체를 그대로 붙여 넣는다.** 서버 주소·토큰·fingerprint를 알아서 파싱하므로 값을 나눠 입력할 필요가 없다.

스크립트가 대신 해 주므로 **직접 할 필요가 없는 것**:

- 시각 동기(NTP) 어긋남 사전 점검 — 어긋나면 등록이 실패하므로 먼저 잡아낸다
- 서버 8443 도달성 사전 점검
- 바이너리 실행 경로 배치와 상태 디렉토리 권한 설정
- `enroll` 실행
- 상시 실행 등록 — Linux systemd 유닛 / Windows 작업 스케줄러

### 0.4 설치 확인

웹 콘솔 [센서] 화면에서 해당 센서의 상태가 **대기 → 활성**으로 바뀌고 **마지막 접속(last_seen)** 이 갱신되면 끝이다. 센서는 유휴 시 5–60초 간격으로 서버를 폴링한다.

이후 [Discovery] 화면에서 **실행기**로 이 센서를 골라 스캔을 시작한다 (OPERATIONS.md 2.4절).

붙지 않으면 **9장 트러블슈팅**으로 간다.

## 1. 개요 — 어떤 경우 센서가 필요한가

Discovery 스캔은 기본적으로 AutoCert **서버가 직접** 실행한다 (INSTALL.md 3.2절). 그러나 세그먼트 간 방화벽 정책 때문에 어느 위치에 서버를 설치해도 도달할 수 없는 세그먼트가 남을 수 있다 (INSTALL.md 4.0절).

**센서(원격 수집기)** 는 그런 격리 세그먼트 안에 설치하는 경량 수집기다. 서버 대신 해당 세그먼트에서 에이전트리스 TLS 스캔을 실행하고 결과만 서버로 보고한다.

- 예: B↔C, C↔D는 통신 가능하지만 B↮D인 망에서 서버가 B망에 있다면, 센서를 C망에 두어 D망 인증서를 수집한다.

동작 원리 (관리자가 알아야 할 요점):

| 항목 | 내용 |
| --- | --- |
| 통신 방향 | 센서 → 서버 **아웃바운드 mTLS(HTTPS) 1경로**(기본 8443/TCP)만 사용. 센서에 인바운드 포트를 열지 않는다 |
| 작업 분배 | pull 방식 — 센서가 주기적으로 서버에 작업을 가져간다(리스). 서버가 센서로 접속하는 일이 없다 |
| 작업 유형 | `scan`(네트워크 TLS 스캔) 하나뿐이다. 임의 명령 실행 경로가 없다 |
| 결과 반영 | 스캔 결과는 배치로 전송되며 (작업, IP, 포트) 기준 멱등 — 재전송해도 중복 등록되지 않는다 |
| 개인키 | 센서 개인키는 등록 시 **센서 호스트에서 생성되어 호스트를 떠나지 않는다.** 서버는 등록 토큰의 SHA-256 해시만 저장한다 |

## 2. 사전 요구사항

센서를 설치할 호스트(격리 세그먼트 내 서버 1대) 기준:

- **OS**: Linux x86_64 또는 Windows x86_64. 센서는 정적 단일 바이너리라 별도 런타임(컨테이너·Go 등) 설치가 필요 없다.
- **네트워크 아웃바운드 2방향**: ① 센서 → AutoCert 서버 8443/TCP (mTLS 게이트웨이) ② 센서 → 스캔 대상 세그먼트의 TLS 서비스 포트. **인바운드 개방은 없다.**
- **시각 동기(NTP)**: 필수. mTLS 인증서 유효기간 검증 때문에 센서 호스트와 서버의 시계가 어긋나면 등록·통신이 실패한다. 폐쇄망 내부 NTP 서버에 동기화할 것.
- **권한**: 상태 디렉토리(5.3절)를 만들고 쓸 수 있는 계정 (root / Administrator 또는 해당 디렉토리를 소유한 전용 계정).

## 3. 서버 측 준비

> **0.1절의 "설정 변경" 모드를 썼다면 이 장은 이미 끝나 있다** — 키 생성과 주소 선택, backend 재기동까지 스크립트가 처리한다. 아래는 값을 확인·수정할 때 보는 정본이다.

### 3.1 환경 변수

`.env`에 아래 값을 추가하고 backend를 재기동한다 (`docker compose up -d backend`). `.env.example`에는 포함되어 있지 않으므로 직접 추가한다.

> **`SENSOR_CA_ENC_KEY`와 `SENSOR_ADVERTISED_HOSTS`는 세트다 — 하나만 넣으면 안 된다.**
> - 센서를 **쓰지 않을 것이면 둘 다 넣지 않는다.** 그래야 서버가 센서 게이트웨이 없이 정상 기동한다.
> - 센서를 **쓸 것이면 둘 다 실제 값으로 채운다.** `SENSOR_CA_ENC_KEY`만 넣고 `SENSOR_ADVERTISED_HOSTS`를 빈 값으로 두면 서버 인증서에 넣을 SAN이 없어 **센서 게이트웨이(8443)가 뜨지 않고 센서 기능을 쓸 수 없다.**
> - 이 경우에도 **코어 API(8080)와 웹 콘솔은 정상 기동한다** — 센서 설정 실패는 게이트웨이만 끄고 격리된다. 즉 증상이 화면에 드러나지 않으므로, 센서가 연결되지 않으면 backend 로그의 경고 한 줄로 확인한다: `sensor gateway disabled: startup failed — check SENSOR_ADVERTISED_HOSTS …`. `SENSOR_CA_ENC_KEY` 형식 오류(hex 64자가 아님)도 같은 방식으로 게이트웨이만 비활성화된다.

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `SENSOR_CA_ENC_KEY` | (없음) | 센서 mTLS 내부 CA의 개인키를 암호화(AES-256-GCM)해 보관하기 위한 키. **hex 64자(32바이트)** — `openssl rand -hex 32`로 생성. **미설정이면 8443 센서 게이트웨이가 아예 열리지 않는다**(센서를 안 쓰는 정상 상태). 설정할 때는 `SENSOR_ADVERTISED_HOSTS`도 반드시 함께 채운다. 분실하면 CA 개인키를 복호화할 수 없어 신규 센서 등록·인증서 갱신이 불가능해지므로 백업 매체에 안전하게 보관한다. CA 생성(첫 센서 등록) 이후 값을 바꾸면 안 된다 |
| `SENSOR_ADVERTISED_HOSTS` | (없음) | 센서가 서버에 접속할 때 쓰는 **실제 주소**(DNS 이름 또는 IP, 쉼표 구분 혼용 가능 — 예: `10.20.0.5,autocert.internal`, 한 대에서 시험할 때는 `127.0.0.1,localhost`). 게이트웨이 서버 인증서의 SAN에 들어간다 — 여기 없는 주소로 접속하는 센서는 TLS 검증에 실패한다. 센서 `--server`에 쓸 주소를 반드시 포함할 것. **`SENSOR_CA_ENC_KEY`를 설정했는데 이 값이 비어 있으면 게이트웨이가 뜨지 않는다**(위 경고 — 코어 API는 정상). 변경 시 backend 재기동 필요(서버 인증서는 기동 시 발급) |
| `SENSOR_GATEWAY_PORT` | `8443` | 센서가 접속하는 **호스트 포트**. 특별한 사유가 없으면 기본값 사용. 바꾸는 절차와 함께 고칠 항목은 INSTALL.md 5.5절 |

> `SENSOR_ADVERTISED_HOSTS`에 **포트를 적지 않는다** (`10.20.0.5:9443` ✗ / `10.20.0.5` ○). 이 값은 인증서 SAN이라 주소만 들어간다 — 포트를 붙이면 기동 로그에 경고를 남기고 그 항목이 통째로 무시되어, 센서 접속 단계에서 TLS 검증 실패로 드러난다. 포트는 `SENSOR_GATEWAY_PORT`가 정한다.
>
> compose 배포에서는 `SENSOR_LISTEN_ADDR`을 `.env`에 직접 넣지 않는다 — 리스너 포트와 포트 매핑이 어긋나는 것을 막기 위해 컨테이너 내부 포트(`SENSOR_GATEWAY_INTERNAL_PORT`) 하나에서 파생시킨다 (INSTALL.md 5.5절).

```sh
# 키 생성 예시 — 출력값을 .env의 SENSOR_CA_ENC_KEY에 넣는다
openssl rand -hex 32
```

```sh
# .env 추가 예시 (센서를 쓰는 경우)
SENSOR_CA_ENC_KEY=<위 명령의 출력값>
SENSOR_ADVERTISED_HOSTS=10.20.0.5,autocert.internal
# 포트는 기본값(8443)을 쓸 것이면 넣지 않아도 된다
#SENSOR_GATEWAY_PORT=8443
```

### 3.2 방화벽

- **센서 호스트 → AutoCert 서버 8443/TCP**(기본값 — 바꿨다면 그 포트) 아웃바운드 1경로를 연다 (INSTALL.md 4.1절). 필요한 경로는 이것 하나뿐이다.
- 센서 호스트 → 스캔 대상 세그먼트의 TLS 포트(443 등) 아웃바운드는 스캔 대상에 맞게 연다.
- 센서 호스트에 인바운드 규칙은 필요 없다.

## 4. 센서 패키지 반입

**센서는 서버와 별개의 패키지다.** 서버 패키지 `main_<버전>.tar.gz`에는 센서 바이너리가 들어 있지 않다 — 센서 호스트에 200 MB짜리 서버 이미지를 옮기지 않으려고 분리했다. 예전의 통합 번들(`autocert-offline-<버전>.tar`)과 그 안의 `sensor/` 디렉터리는 **폐기됐다.**

| 산출물 | 대상 | 들어 있는 것 |
| --- | --- | --- |
| `sensor_linux_<버전>.tar.gz` (+ `.sha256`) | Linux x86_64 센서 호스트 | `install-sensor.sh` · `autocert-sensor-linux-amd64` · `lib/tui.sh` · `manual/SENSOR.md` · `VERSION` · `MANIFEST.txt` · `SHA256SUMS` |
| `sensor_windows_<버전>.zip` (+ `.sha256`) | Windows x86_64 센서 호스트 | `install-sensor.ps1` · `autocert-sensor-windows-amd64.exe` · `manual\SENSOR.md` · `VERSION` · `MANIFEST.txt` · `SHA256SUMS` |

**서버와 센서의 버전은 같게 맞춘다** (버전 스큐 주의사항은 8.1절). 패키지 안 `VERSION`과 `MANIFEST.txt`에 버전이 적혀 있고, 설치 후에는 `autocert-sensor --version`으로도 확인할 수 있다 (7장).

> 4.1–4.2의 체크섬 대조와 배치는 **0.3절 빠른 설치에 이미 포함**돼 있다(설치 스크립트가 배치와 권한까지 처리한다). 아래는 바이너리를 따로 만들어야 하거나 손으로 배치할 때 본다.

### 4.1 반입과 무결성 검증 — 2단 검증

체크섬은 두 번 본다. **사이드카 `.sha256`은 매체 손상 판별용**이고, **패키지 안 `SHA256SUMS`는 내용물 전건 대조용**이다.

**Linux**

```sh
sha256sum -c sensor_linux_1.0.0.tar.gz.sha256
tar xzf sensor_linux_1.0.0.tar.gz
cd sensor_linux_1.0.0
sha256sum -c SHA256SUMS
```

**Windows** (PowerShell)

```powershell
Get-FileHash .\sensor_windows_1.0.0.zip -Algorithm SHA256
Get-Content .\sensor_windows_1.0.0.zip.sha256
Expand-Archive .\sensor_windows_1.0.0.zip -DestinationPath .
```

- Windows는 위 두 해시를 **눈으로 대조**한다. 압축을 푼 뒤 패키지 안 `SHA256SUMS` 전건 대조는 `install-sensor.ps1`이 실행 전에 자동으로 수행하므로 손으로 할 필요가 없다 (`certutil -hashfile <파일> SHA256`으로 개별 확인은 가능하다).
- **어긋나면 설치하지 않고 매체를 다시 받는다.** 부분 복사·재시도로 넘기지 않는다.
- 검증이 끝나면 0.3절대로 설치 스크립트를 실행한다. 스크립트가 실행 경로 배치(Linux `/usr/local/bin/autocert-sensor`, Windows `C:\Program Files\AutocertSensor\autocert-sensor.exe`)와 상태 디렉토리 권한까지 처리한다.

### 4.2 빌드 (인터넷이 되는 빌드 환경에서)

**센서 패키지를 반입해 쓴다면 이 절은 건너뛴다.** 바이너리만 따로 만들려면 저장소 루트에서 실행한다 (Docker 필요, 로컬 Go 불필요):

```sh
sh scripts/build-sensor-dist.sh
# backend/ 디렉터리에서 `make sensor-dist`로 실행해도 같다(같은 스크립트를 호출한다)
```

산출물은 `backend/dist/`에 생성된다:

| 파일 | 대상 |
| --- | --- |
| `autocert-sensor-linux-amd64` | Linux x86_64 |
| `autocert-sensor-windows-amd64.exe` | Windows x86_64 |
| `SHA256SUMS` | 위 두 파일의 SHA-256 체크섬 |

버전 문자열은 빌드할 때 바이너리에 새겨진다 — `AUTOCERT_VERSION` 환경변수 → 저장소 루트 `VERSION` 파일 → `dev` 순으로 결정한다. 배포판 패키지를 만드는 `scripts/bundle-offline.sh`도 같은 스크립트를 호출하므로 결과가 어긋나지 않는다.

손으로 배치할 경우 실행 경로는 Linux `/usr/local/bin/autocert-sensor`, Windows `C:\Program Files\AutocertSensor\autocert-sensor.exe`다.

## 5. 등록 절차 (수동)

> **0.3절의 스크립트를 썼다면 5.2절은 스크립트가 대신 실행한다** — 붙여 넣은 enroll 명령에서 서버 주소·토큰·fingerprint를 파싱해 그대로 돌린다. 5.1절(토큰 발급)은 어느 경로든 웹 콘솔에서 사람이 한다.

### 5.1 웹 콘솔에서 등록 토큰 발급 (System Administrator)

1. 웹 콘솔 사이드바 [센서] 화면 → **[센서 등록]** (등록·비활성화는 System Administrator 전용, 조회는 전 역할).
2. 센서 이름 입력(예: `sensor-c-segment`) → 등록 토큰 발급.
3. 표시되는 세 값을 안전하게 옮겨 적는다 — **이 창을 닫으면 다시 볼 수 없다** (서버는 토큰의 해시만 저장):
   - **등록 토큰** — 1회용, **24시간 유효**. 분실·만료 시 해당 센서를 비활성화하고 새로 등록한다.
   - **CA fingerprint** — 센서가 서버의 진위를 확인하는 데 쓰는 지문(SHA-256 hex 64자).
   - **enroll 명령 예시** — 아래 5.2절 명령의 완성형.

### 5.2 센서 호스트에서 등록 실행

```sh
autocert-sensor enroll \
  --server https://<서버 주소>:8443 \
  --token <등록 토큰> \
  --ca-fingerprint <CA fingerprint>
```

- `<서버 주소>`는 `SENSOR_ADVERTISED_HOSTS`에 포함된 DNS 이름 또는 IP여야 한다 (3.1절).
- **포트는 서버 설정을 따른다.** 위 `8443`은 기본값이며, 5.1절 화면의 "enroll 명령 예시"에는 그 서버에 **실제로 설정된 포트**가 이미 들어가 있으므로 그대로 복사해 쓰면 된다 (INSTALL.md 5.5절).
- 등록 과정: 센서가 로컬에서 키쌍 생성 → CSR 제출(서버 진위는 `--ca-fingerprint` 핀으로 검증) → 클라이언트 인증서(90일 유효) 수령·저장. 개인키는 전송되지 않는다.
- 성공하면 `enrollment complete` 로그와 함께 센서 ID가 출력되고, 웹 콘솔 [센서] 화면에서 상태가 **대기 → 활성**으로 바뀐다.

### 5.3 상태 디렉토리

등록 결과(개인키·클라이언트 인증서·CA 인증서·센서 ID)는 상태 디렉토리에 저장된다. `--state-dir`로 바꾸지 않으면 기본값:

| OS | 기본 경로 | 권한 |
| --- | --- | --- |
| Linux | `/var/lib/autocert-sensor` | 디렉토리 0700 / 파일 0600 |
| Windows | `%ProgramData%\AutocertSensor` | 관리자 전용 접근으로 유지할 것 |

**이 디렉토리에는 센서 개인키(`key.pem`)가 있다.** 권한을 완화하거나 다른 호스트로 복사하지 않는다. `enroll`과 `run`에 서로 다른 `--state-dir`를 주면 신원을 찾지 못하므로 같은 값을 쓴다.

## 6. 서비스 등록 (상시 실행, 수동)

> **0.3절의 스크립트를 썼다면 이 장은 이미 끝나 있다** — Linux는 systemd 유닛, Windows는 작업 스케줄러 항목을 스크립트가 만들고 시작까지 한다. 아래는 유닛을 손으로 만들거나 내용을 확인·수정할 때 본다.

등록(5장)을 마친 뒤 `run` 모드를 서비스로 상시 실행한다.

> 아래 예시의 `8443`은 기본 포트다. 서버에서 센서 게이트웨이 포트를 바꿨다면 `--server`의 포트를 그 값으로 적는다. **포트가 바뀌어도 재등록(enroll)은 필요 없다** — 포트는 인증서 SAN에도 상태 디렉토리에도 기록되지 않으므로, 유닛의 포트만 고쳐 서비스를 재시작하면 기존 신원으로 그대로 붙는다 (INSTALL.md 5.5.2).

### 6.1 Linux — systemd 유닛 예시

`/etc/systemd/system/autocert-sensor.service`:

```ini
[Unit]
Description=INHERIT AutoCert Sensor
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/autocert-sensor run --server https://<서버 주소>:8443
Restart=always
RestartSec=5
# 상태 디렉토리(/var/lib/autocert-sensor, 0700)를 소유한 계정으로 실행한다.
# 전용 계정을 쓰려면 디렉토리 소유권을 함께 넘길 것 (chown -R).
User=root

[Install]
WantedBy=multi-user.target
```

```sh
systemctl daemon-reload
systemctl enable --now autocert-sensor
systemctl status autocert-sensor      # active (running) 확인
journalctl -u autocert-sensor -f      # 구조화 JSON 로그
```

### 6.2 Windows — 시작 등록 예시

센서 바이너리는 콘솔 프로그램이므로 Windows 서비스 관리자(SCM)에 직접 등록되지 않는다. 두 방법 중 하나를 쓴다.

- **작업 스케줄러** (추가 도구 없음 — 부팅 시 자동 시작):

```bat
schtasks /Create /TN "AutocertSensor" /SC ONSTART /RU SYSTEM ^
  /TR "\"C:\Program Files\AutocertSensor\autocert-sensor.exe\" run --server https://<서버 주소>:8443"
schtasks /Run /TN "AutocertSensor"
```

- **서비스 래퍼**(NSSM 등 조직 표준 도구가 있는 경우): 실행 파일과 인자를 위와 동일하게 등록한다. 비정상 종료 시 자동 재시작이 필요하면 이 방식을 권장한다.

## 7. 운영

- **스캔 위임**: 웹 콘솔 [Discovery] 화면의 **실행기** 선택에서 해당 센서를 고르고 스캔을 시작한다 (OPERATIONS.md 2.4절). 센서는 최대 폴링 간격(60초) 안에 작업을 가져간다.
- **상태 확인**: [센서] 화면 목록에서 상태(대기/활성/비활성화)와 **마지막 접속(last_seen)** 을 본다. 센서는 유휴 시 5–60초 간격으로 서버를 폴링하므로, 정상 가동 중이면 last_seen이 수 분 이내여야 한다 (OPERATIONS.md 2.5절).
- **하트비트**: 스캔 실행 중 센서는 30초 주기로 하트비트를 보낸다. 하트비트는 [Discovery] 화면의 진행률을 갱신하는 동시에 작업 리스(90초)를 연장한다. 센서가 도중에 죽으면 서버가 리스 만료로 해당 작업을 실패 처리한다 — 같은 대상으로 재실행하면 된다(결과 반영은 중복 안전).
- **클라이언트 인증서 자동 갱신**: 인증서는 90일 유효이며, 센서가 기동 시와 하루 1회 잔여 기간을 검사해 **30일 미만이면 자동 갱신**한다(무중단, `sensor.renew` 감사 기록). 운영자가 할 일은 없다. 단, 센서를 90일 넘게 꺼두면 인증서가 만료되어 재등록이 필요하다.
- **비활성화**: [센서] 화면에서 비활성화(System Administrator 전용)하면 **다음 요청부터 즉시 차단**된다(401). 되돌릴 수 없으며 다시 쓰려면 새로 등록한다. 미사용 등록 토큰 회수(대기 상태 센서)도 같은 버튼으로 한다.
- **감사 로그**: 토큰 발급(`sensor.token.issue`)·등록(`sensor.enroll`, 실패 포함)·인증서 갱신(`sensor.renew`)·비활성화(`sensor.disable`)는 전건 감사 이벤트로 기록된다 (OPERATIONS.md 3.1절).
- **버전 확인**: `autocert-sensor --version`이 빌드된 버전을 출력한다(`autocert-sensor 1.0.0`). 버전을 새기지 않고 빌드한 바이너리는 `dev`로 나온다 — 정식 패키지에서 `dev`가 보이면 그 바이너리는 릴리즈 산출물이 아니다. 서버와 버전이 어긋나면 8.1절을 본다.
- **로그**: 센서는 구조화 JSON 로그를 표준 출력으로 남긴다. 개인키·인증서·토큰은 로그에 출력되지 않는다 — 발견되면 제품 결함이므로 즉시 보고할 것.

## 8. 업데이트

1. 새 버전 **센서 패키지**를 반입하고 체크섬을 검증한다 (4장). 서버 패키지에는 센서 바이너리가 없다.
2. 센서 중지 (`systemctl stop autocert-sensor` / 작업 종료).
3. 바이너리 교체 후 재시작. `autocert-sensor --version`으로 교체가 실제로 됐는지 확인한다.

설치 스크립트를 다시 돌려도 된다 — 이미 등록된 센서라면 `--skip-enroll`로 서비스만 재설치한다.

상태 디렉토리는 그대로 유지되므로 **재등록이 필요 없다.** 등록 정보를 지우지 않는 한 `enroll`을 다시 실행하지 않는다.

### 8.1 버전 스큐 — 구버전 센서는 `rate_pps`를 무시한다

서버는 작업 리스 응답에 스캔 속도 상한 `rate_pps`(초당 프로브 수)를 새로 실어 보낸다. 배경 스윕은 이 값을 낮게(기본 10pps) 잡아 며칠에 걸쳐 조용히 도는 것이 전제다.

**이 필드가 추가되기 전 버전의 센서는 `rate_pps`를 그냥 무시하고 레이트 제한 없이 전속력으로 스캔한다.** 서버가 값을 내려보내도 구버전 바이너리에는 이를 해석하는 코드가 없으므로, 스윕이 몇 시간 만에 끝나는 대신 IPS·방화벽이 포트스캔으로 탐지해 **센서 호스트 IP가 차단**될 수 있다. 서버 쪽 설정(`SCAN_SWEEP_RATE_PPS`)을 아무리 낮춰도 구버전 센서에는 효과가 없다.

- **스윕(전 포트 백그라운드 스캔)을 쓸 계획이라면 센서를 먼저 최신 버전으로 갱신한다** (위 1~3단계). 갱신 전에는 해당 센서를 스윕 실행기로 지정하지 않는다.
- 센서 버전은 `autocert-sensor --version`으로 확인한다. 헷갈리면 갱신부터 한다 — 재등록이 필요 없어 비용이 거의 없다.
- 단발 스캔(프리셋 포트)은 대상 수가 적어 영향이 작지만, 같은 이유로 속도 상한이 걸리지 않는다.

## 9. 트러블슈팅

`install-sensor.sh`(`.ps1`)는 설치 전에 **시각 동기(NTP)** 와 **서버 8443 도달성**을 먼저 점검한다 — 아래 표에서 가장 흔한 두 원인이다. 사전 점검에서 멈췄다면 표의 해당 행을 먼저 본다. 서버 쪽 설치 단계의 증상은 INSTALL.md 8장에 있다.

| 증상 | 원인 후보 | 조치 |
| --- | --- | --- |
| Linux 설치 스크립트가 시작하자마자 권한 실패로 멈춤 | `sudo` 없이 실행했다. systemd 유닛 등록·`/usr/local/bin` 배치에 root가 필요하다 | `sudo bash install-sensor.sh`로 다시 실행 (0.3절) |
| Windows에서 `... cannot be loaded because running scripts is disabled on this system` | 서명되지 않은 `.ps1`이 기본 실행 정책에 걸렸다 | `powershell -ExecutionPolicy Bypass -File .\install-sensor.ps1`로 실행 (0.3절) |
| Windows에서 `Bypass`를 줬는데도 스크립트가 열리지 않거나 보안 경고가 뜸 | 매체 경유 zip에 **Mark-of-the-Web**가 붙어 압축을 푼 파일마다 차단 표시가 남아 있다 | 푼 폴더에서 `Get-ChildItem -Recurse -File \| Unblock-File` 실행 후 재시도 (0.3절) |
| 패키지 안에 센서 바이너리가 없음 | 서버 패키지(`main_<버전>.tar.gz`)를 센서 호스트에 반입했다 | 센서는 별도 패키지다 — `sensor_linux_<버전>.tar.gz` / `sensor_windows_<버전>.zip`을 반입한다 (4장) |
| `autocert-sensor --version`이 `dev`를 출력 | 버전을 새기지 않고 직접 빌드한 바이너리다 | 정식 센서 패키지의 바이너리로 교체한다 (8장). 서버와의 버전 스큐는 8.1절 |
| enroll이 `server certificate chain does not match --ca-fingerprint` | 지문 오입력, 또는 다른 서버/재구축된 서버에 접속 | 웹 콘솔에서 발급받은 CA fingerprint를 다시 확인. 서버를 재구축(DB 초기화)했다면 CA가 바뀌었으므로 센서를 새로 등록 |
| enroll이 401 (`invalid_token`) | 등록 토큰 무효·이미 사용됨·**만료(24시간)** | [센서] 화면에서 해당 대기 센서를 비활성화(토큰 회수)하고 새로 등록해 새 토큰 발급 |
| **웹 콘솔·8080은 멀쩡한데 8443만 안 열림** (로그 `sensor gateway disabled: startup failed — check SENSOR_ADVERTISED_HOSTS …`) | `SENSOR_CA_ENC_KEY`는 설정했는데 `SENSOR_ADVERTISED_HOSTS`가 비어 있음 — 서버 인증서에 넣을 SAN이 없다. 게이트웨이만 비활성화되고 코어 API는 계속 뜨므로 화면에는 증상이 없다 | `SENSOR_ADVERTISED_HOSTS`에 센서가 접속할 실제 주소를 넣고 backend 재기동 (3.1절). 센서를 쓰지 않을 거라면 `SENSOR_CA_ENC_KEY`도 함께 지운다 |
| 로그에 `sensor gateway disabled: invalid sensor configuration` | `SENSOR_CA_ENC_KEY` 형식 오류 — hex 64자(32바이트)가 아님 | `openssl rand -hex 32`로 다시 생성해 넣고 재기동 (3.1절). 이미 센서를 등록한 뒤라면 기존 키를 되찾아야 한다 — 값을 바꾸면 기존 CA를 복호화할 수 없다 |
| enroll/run 연결 거부 (connection refused) | ① `SENSOR_CA_ENC_KEY` 미설정 — 8443 리스너 자체가 없음 ② 센서 설정 오류로 게이트웨이만 비활성화됨(위 두 행) ③ 방화벽 차단 | backend 로그에서 `sensor gateway listening` 유무부터 확인 — 없으면 `sensor gateway disabled …` 경고 문구로 원인을 가른다 → `.env`의 센서 변수 2종을 세트로 확인 후 재기동 (3.1절) → 센서→서버 8443 아웃바운드 방화벽 확인 (3.2절) |
| run에서 TLS 검증 실패 (certificate is valid for … not …) | `--server` 주소가 `SENSOR_ADVERTISED_HOSTS`에 없음. **주소는 맞는데 실패한다면** `SENSOR_ADVERTISED_HOSTS`에 포트를 함께 적었을 가능성이 크다 — 포트가 붙은 항목은 무시되므로 SAN에서 그 주소가 통째로 빠진다 | 서버 `.env`의 `SENSOR_ADVERTISED_HOSTS`에 **포트 없는** 주소를 넣고 backend 재기동 (3.1절). 기동 로그에 `SENSOR_ADVERTISED_HOSTS entry "…" includes a port …` 경고가 있는지 확인 |
| 인증서 유효기간 오류 (expired / not yet valid) | 센서 호스트 **시계 드리프트**, 또는 90일 이상 정지로 인증서 만료 | NTP 동기화 확인 (2장). 만료면 비활성화 후 재등록 |
| run 중 401 반복 | 센서가 비활성화됨, 또는 인증서가 교체·만료됨 | [센서] 화면에서 상태 확인. disabled면 재등록 |
| `state not found in … — run 'enroll' first` | 미등록 상태로 run 실행, 또는 `--state-dir` 불일치 | 5장 등록 먼저 수행. enroll과 run의 `--state-dir`가 같은지 확인 |
| 스캔 Job이 대기(pending)에서 안 움직임 — [Discovery] 표에 **대기 중** 배지와 "…수령 대기 · N분째" | 센서 미가동·서버 미도달 (pull 방식이라 센서가 안 가져가면 시작되지 않음). 배지가 앰버색이면 대기 90초 초과이거나 대상 센서가 활성이 아니다 | 배지 옆 사유("비활성화됨"/"등록 대기")를 먼저 본다 → 센서 프로세스·로그 확인, last_seen 확인 (7장) → 게이트웨이 기동 여부 확인(위 행) |
| 스캔 Job이 leased/실행 중 상태에서 실패로 바뀜 | 리스 만료(90초) — 센서 중단·통신 두절로 하트비트 중단 | 센서 로그에서 원인 확인 후 같은 대상으로 재실행 (중복 안전) |
