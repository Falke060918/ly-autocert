import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Docker 배포용 — .next/standalone에 자기완결 서버를 생성한다.
  output: "standalone",
};

export default nextConfig;
