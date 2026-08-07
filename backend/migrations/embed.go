// Package migrations는 SQL 마이그레이션 파일을 바이너리에 임베드한다.
// 폐쇄망 배포에서 별도 마이그레이션 CLI 없이 서버 기동 시 적용하기 위함이다.
// 파일 명명 규칙(golang-migrate): {version}_{title}.up.sql / {version}_{title}.down.sql
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
