# 백엔드 검증 타깃. 로컬 Go가 없으면 docs/MAP.md의 Docker 경유 방법을 쓴다.
.PHONY: openapi-check sensor-dist

# 라우트 등록 목록과 api/openapi.yaml의 경로·메서드 정합 검증 (DB 불필요, 항상 실행 가능).
# 라우트 추가/제거 시 명세를 같이 고치지 않으면 실패한다. 전체 테스트에도 포함된다.
openapi-check:
	go test ./internal/api -run TestOpenAPISpecMatchesRoutes -count=1

# 센서 바이너리 배포판 빌드 (Phase 3a). 로컬 Go 없이 Docker golang 이미지 경유로
# linux/amd64·windows/amd64 정적 크로스 컴파일 후 SHA-256 체크섬을 만든다.
# 산출물: dist/ (gitignore 대상). 체크섬은 컨테이너 안에서 계산해 호스트 OS 비의존.
sensor-dist:
	docker run --rm -v "$(CURDIR)":/src -w /src -e CGO_ENABLED=0 golang:1.25-alpine \
		sh -ec '\
		mkdir -p dist; \
		GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/autocert-sensor-linux-amd64 ./cmd/sensor; \
		GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/autocert-sensor-windows-amd64.exe ./cmd/sensor; \
		cd dist && sha256sum autocert-sensor-linux-amd64 autocert-sensor-windows-amd64.exe > SHA256SUMS'
