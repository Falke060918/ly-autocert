import { NextResponse, type NextRequest } from "next/server";

// 세션 쿠키 이름 — 백엔드 로그인 응답의 HttpOnly 쿠키와 일치해야 한다 (openapi cookieAuth).
const SESSION_COOKIE = "autocert_session";

// 쿠키 존재만 검사하는 1차 관문이다. 만료·폐기된 세션은 페이지의 /auth/me 호출이
// 401을 받아 로그인으로 보낸다 (lib/auth.tsx).
export default function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  const hasSession = request.cookies.has(SESSION_COOKIE);

  if (!hasSession && pathname !== "/login") {
    const url = request.nextUrl.clone();
    url.pathname = "/login";
    url.search =
      pathname === "/" ? "" : `?next=${encodeURIComponent(pathname + search)}`;
    return NextResponse.redirect(url);
  }
  if (hasSession && pathname === "/login") {
    return NextResponse.redirect(new URL("/", request.url));
  }
  return NextResponse.next();
}

export const config = {
  // /api는 백엔드 프록시 경로라 제외한다 (로그인 요청까지 리다이렉트되면 안 된다).
  matcher: ["/((?!api|_next/static|_next/image|favicon.ico).*)"],
};
