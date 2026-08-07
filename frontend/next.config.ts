import type { NextConfig } from "next";

// 백엔드 프록시 대상. 브라우저는 같은 출처(/api/*)로만 호출하고 Next 서버가 백엔드로
// 중계한다 — HttpOnly 세션 쿠키가 CORS 설정 없이 동작한다 (폐쇄망: 외부 요청 0건 유지).
// standalone 출력에서는 빌드 시점에 값이 고정되므로 Docker 빌드 시 build-arg로 넘긴다.
const backendUrl = process.env.BACKEND_INTERNAL_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  // Docker 배포용 — .next/standalone에 자기완결 서버를 생성한다.
  output: "standalone",
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${backendUrl}/api/:path*` }];
  },
};

export default nextConfig;
