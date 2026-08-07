/**
 * 백엔드 API 접근 헬퍼.
 * 베이스 URL은 환경변수 NEXT_PUBLIC_API_URL로 주입한다 (기본: http://localhost:8080).
 * NEXT_PUBLIC_ 값은 빌드 시점에 번들에 고정되므로 Docker 빌드 시 build-arg로 넘긴다.
 */
export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export async function apiFetch<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });
  if (!res.ok) {
    throw new Error(`API ${res.status}: ${path}`);
  }
  return res.json() as Promise<T>;
}
