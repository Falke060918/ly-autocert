# 백엔드 검증 타깃. 로컬 Go가 없으면 docs/MAP.md의 Docker 경유 방법을 쓴다.
.PHONY: openapi-check sensor-dist

# 라우트 등록 목록과 api/openapi.yaml의 경로·메서드 정합 검증 (DB 불필요, 항상 실행 가능).
# 라우트 추가/제거 시 명세를 같이 고치지 않으면 실패한다. 전체 테스트에도 포함된다.
openapi-check:
	go test ./internal/api -run TestOpenAPISpecMatchesRoutes -count=1

# 센서 바이너리 배포판 빌드 (Phase 3a). 로컬 Go 없이 Docker golang 이미지 경유로
# linux/amd64·windows/amd64 정적 크로스 컴파일 후 SHA-256 체크섬을 만든다.
# 산출물: dist/ (gitignore 대상). 체크섬은 컨테이너 안에서 계산해 호스트 OS 비의존.
# 레시피 본체는 ../scripts/build-sensor-dist.sh에 있다 — make가 없는 환경(오프라인
# 번들 스크립트)도 같은 빌드를 쓰기 위해서다. 레시피를 여기에 복제하지 않는다.
sensor-dist:
	sh $(CURDIR)/../scripts/build-sensor-dist.sh
