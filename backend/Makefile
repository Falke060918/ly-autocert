# 백엔드 검증 타깃. 로컬 Go가 없으면 docs/MAP.md의 Docker 경유 방법을 쓴다.
.PHONY: openapi-check

# 라우트 등록 목록과 api/openapi.yaml의 경로·메서드 정합 검증 (DB 불필요, 항상 실행 가능).
# 라우트 추가/제거 시 명세를 같이 고치지 않으면 실패한다. 전체 테스트에도 포함된다.
openapi-check:
	go test ./internal/api -run TestOpenAPISpecMatchesRoutes -count=1
