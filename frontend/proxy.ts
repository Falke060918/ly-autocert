import { NextResponse, type NextRequest } from "next/server";

// 세션 쿠키 이름 — 백엔드 로그인 응답의 HttpOnly 쿠키와 일치해야 한다 (openapi cookieAuth).
const SESSION_COOKIE = "autocert_session";

/**
 * 세션 없이 열리는 화면. /setup은 계정이 0명일 때의 초기 설정이며, 실제 개폐는
 * 서버가 판정한다(setup_required=false면 페이지가 스스로 /login으로 되돌아간다).
 */
const PUBLIC_PATHS = new Set(["/login", "/setup"]);

// 쿠키 존재만 검사하는 1차 관문이다. 만료·폐기된 세션은 페이지의 /auth/me 호출이
// 401을 받아 로그인으로 보낸다 (lib/auth.tsx).
export default function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  const hasSession = request.cookies.has(SESSION_COOKIE);

  // 로그인한 사용자에게 초기 설정 화면은 의미가 없다 (설정은 이미 끝났다).
  if (hasSession && pathname === "/setup") {
    return NextResponse.redirect(new URL("/", request.url));
  }
  if (!hasSession && !PUBLIC_PATHS.has(pathname)) {
    const url = request.nextUrl.clone();
    url.pathname = "/login";
    url.search =
      pathname === "/" ? "" : `?next=${encodeURIComponent(pathname + search)}`;
    return NextResponse.redirect(url);
  }
  if (hasSession && pathname === "/login") {
    // reason이 붙은 이동은 세션이 폐기돼 튕겨나온 경우다. 되돌리면 앱 안에 탈출구가
    // 사라지므로 통과시키고, 남아 있는 쿠키는 여기서 만료시킨다 — /session/clear 호출이
    // 실패했더라도 로그인 화면에는 도달한다.
    if (request.nextUrl.searchParams.has("reason")) {
      const response = NextResponse.next();
      response.cookies.delete(SESSION_COOKIE);
      return response;
    }
    return NextResponse.redirect(new URL("/", request.url));
  }
  return NextResponse.next();
}

export const config = {
  // /api는 백엔드 프록시 경로라 제외한다 (로그인 요청까지 리다이렉트되면 안 된다).
  // /session/clear는 쿠키를 만료시키는 로컬 라우트라 관문을 태우지 않는다.
  matcher: ["/((?!api|session/clear|_next/static|_next/image|favicon.ico).*)"],
};
