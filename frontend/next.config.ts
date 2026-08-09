import type { NextConfig } from "next";

// 백엔드 프록시 대상. 브라우저는 같은 출처(/api/*)로만 호출하고 Next 서버가 백엔드로
// 중계한다 — HttpOnly 세션 쿠키가 CORS 설정 없이 동작한다 (폐쇄망: 외부 요청 0건 유지).
// standalone 출력에서는 빌드 시점에 값이 고정되므로 Docker 빌드 시 build-arg로 넘긴다.
const backendUrl = process.env.BACKEND_INTERNAL_URL ?? "http://localhost:8080";

const isDev = process.env.NODE_ENV === "development";

// 페이지용 CSP. 폐쇄망 전제: 폰트·이미지·API 전부 같은 출처(외부 도메인 0건)이므로
// 'self' 만으로 충분하다. 예외 두 가지만 허용한다.
// - script-src 'unsafe-inline': Next App Router가 하이드레이션 데이터를 인라인
//   <script>로 심는다 — nonce 없이는 필수 (nonce 전환은 미들웨어 도입 시 검토).
// - style-src 'unsafe-inline': Next 주입 인라인 스타일 + React style 속성.
// dev 한정 'unsafe-eval'은 React Refresh(HMR) 요구사항으로, 프로덕션 빌드에는 빠진다.
const csp = [
  "default-src 'self'",
  `script-src 'self' 'unsafe-inline'${isDev ? " 'unsafe-eval'" : ""}`,
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join("; ");

const nextConfig: NextConfig = {
  // Docker 배포용 — .next/standalone에 자기완결 서버를 생성한다.
  output: "standalone",
  // Next 16은 기본적으로 dev/build 때 프로젝트 루트에 AGENTS.md·CLAUDE.md를
  // 자동 생성한다. frontend/CLAUDE.md는 이 저장소의 프로젝트 지시 파일과
  // 이름이 겹쳐 혼동을 부르므로 생성 자체를 끈다(.gitignore는 이중 방어).
  agentRules: false,
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${backendUrl}/api/:path*` }];
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "Content-Security-Policy", value: csp },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Referrer-Policy", value: "no-referrer" },
          // HSTS(Strict-Transport-Security)는 현재 HTTP 배포라 넣지 않는다.
          // HTTPS 전환 시 활성화:
          // { key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" },
        ],
      },
    ];
  },
};

export default nextConfig;
