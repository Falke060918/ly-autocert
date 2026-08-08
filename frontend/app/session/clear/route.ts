import { NextResponse } from "next/server";

// 세션 쿠키 이름 — proxy.ts·백엔드 로그인 응답과 일치해야 한다.
const SESSION_COOKIE = "autocert_session";

/**
 * 남은 세션 쿠키를 만료시키는 로컬 라우트 (POST /session/clear).
 *
 * 백엔드 쿠키는 HttpOnly라 브라우저 JS로 지울 수 없고, /auth/logout은 유효한 세션을
 * 요구해 이미 폐기된 세션(계정 비활성화·비밀번호 재설정)에서는 401이라 쿠키를 지워주지
 * 못한다. 쿠키가 남으면 proxy.ts의 1차 관문이 /login을 다시 /로 되돌려 앱 안에 탈출구가
 * 없어진다 — 그래서 Next 서버가 직접 만료 쿠키를 내려준다.
 */
export async function POST() {
  const res = new NextResponse(null, { status: 204 });
  res.cookies.set(SESSION_COOKIE, "", {
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "lax",
  });
  return res;
}
