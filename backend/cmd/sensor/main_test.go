package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveEnrollToken은 enroll 토큰 입력 경로 3종(--token / --token-file / stdin)의
// 우선순위와 정리 규칙(첫 줄만, 공백·CR 제거)을 고정한다. 토큰이 프로세스 인자에
// 노출되지 않는 경로(--token-file·stdin)가 STATE 8번 항목의 근본 해결이다.
func TestResolveEnrollToken(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		return p
	}

	tests := []struct {
		name       string
		token      string
		tokenFile  string
		stdin      string
		stdinPiped bool
		want       string
		wantErr    bool
	}{
		{name: "token flag wins", token: "tok-flag", want: "tok-flag"},
		{name: "both flags rejected", token: "tok-flag", tokenFile: writeFile("both.txt", "tok-file\n"), wantErr: true},
		{name: "token file first line only", tokenFile: writeFile("multi.txt", "tok-file\nsecond-line\n"), want: "tok-file"},
		{name: "token file trims crlf and spaces", tokenFile: writeFile("crlf.txt", "  tok-crlf\r\n"), want: "tok-crlf"},
		{name: "token file dash reads stdin", tokenFile: "-", stdin: "tok-stdin-dash\n", want: "tok-stdin-dash"},
		{name: "piped stdin without flags", stdin: "tok-stdin-pipe\n", stdinPiped: true, want: "tok-stdin-pipe"},
		{name: "no flags and no pipe rejected", stdinPiped: false, wantErr: true},
		{name: "empty token file rejected", tokenFile: writeFile("empty.txt", "\n"), wantErr: true},
		{name: "missing token file rejected", tokenFile: filepath.Join(dir, "no-such-file"), wantErr: true},
		{name: "empty piped stdin rejected", stdin: "", stdinPiped: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveEnrollToken(tc.token, tc.tokenFile, strings.NewReader(tc.stdin), tc.stdinPiped)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveEnrollToken() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveEnrollToken(): %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveEnrollToken() = %q, want %q", got, tc.want)
			}
		})
	}
}
