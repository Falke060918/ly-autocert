"use client";

/**
 * 세션 사용자 컨텍스트.
 * proxy.ts가 쿠키 존재만 검사하는 1차 관문이고, 여기서 /auth/me로 실제 세션을
 * 검증한다 — 만료·폐기된 세션은 401을 받아 로그인으로 보낸다.
 */

import * as React from "react";
import { useRouter } from "next/navigation";

import {
  ApiError,
  bounceToLogin,
  clearSessionCookie,
  fetchMe,
  logout as apiLogout,
  type Role,
  type User,
} from "@/lib/api";

/** 인벤토리 변경(업로드·스캔 생성)이 허용되는 역할 — 백엔드 protected 등록과 일치. */
const MANAGE_ROLES: readonly Role[] = [
  "system_administrator",
  "pki_administrator",
  "operator",
];

export function canManageInventory(user: User | null): boolean {
  return user != null && MANAGE_ROLES.includes(user.role);
}

/**
 * 인벤토리 전체 CSV 반출이 허용되는 역할 — 백엔드 protected 등록과 일치.
 * 목록 조회(전 역할)보다 좁다: 한 페이지가 아니라 인벤토리 전체를 파일로 빼내는
 * 경로라 인증서 관리 책임이 있는 관리자급만 허용한다 (00-spec 7.11).
 */
const EXPORT_ROLES: readonly Role[] = ["system_administrator", "pki_administrator"];

export function canExportInventory(user: User | null): boolean {
  return user != null && EXPORT_ROLES.includes(user.role);
}

/** 센서 비활성화 등 시스템 관리자 전용 작업 여부. */
export function isSystemAdministrator(user: User | null): boolean {
  return user?.role === "system_administrator";
}

const ROLE_LABELS: Record<Role, string> = {
  system_administrator: "시스템 관리자",
  pki_administrator: "PKI 관리자",
  security_administrator: "보안 관리자",
  operator: "운영자",
  auditor: "감사자",
  viewer: "조회자",
};

export function roleLabel(role: Role): string {
  return ROLE_LABELS[role] ?? role;
}

/** 역할 6종 — 사용자 관리 화면의 선택 목록 순서(권한이 넓은 쪽부터). */
export const ALL_ROLES = Object.keys(ROLE_LABELS) as Role[];

interface AuthContextValue {
  /** null이면 아직 로딩 중이거나 미인증. */
  user: User | null;
  loading: boolean;
  logout: () => Promise<void>;
}

const AuthContext = React.createContext<AuthContextValue>({
  user: null,
  loading: true,
  logout: async () => {},
});

export function useAuth(): AuthContextValue {
  return React.useContext(AuthContext);
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [user, setUser] = React.useState<User | null>(null);
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    let cancelled = false;
    fetchMe()
      .then((u) => {
        if (!cancelled) {
          setUser(u);
          setLoading(false);
        }
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setLoading(false);
        if (err instanceof ApiError && err.status === 401) {
          // 세션이 폐기돼도 HttpOnly 쿠키는 브라우저에 남는다 — 쿠키를 만료시킨 뒤
          // 로그인으로 보낸다 (bounceToLogin). 그래야 proxy.ts가 되돌리지 않는다.
          bounceToLogin();
        }
      });
    return () => {
      cancelled = true;
    };
    // 이동마다 me를 다시 부르지 않는다 — 소프트 내비게이션 중 세션이 폐기되면
    // 각 화면의 API 401을 apiFetch가 잡아 같은 탈출 경로로 보낸다.
  }, []);

  const logout = React.useCallback(async () => {
    try {
      await apiLogout();
    } catch {
      // 세션이 이미 폐기됐으면 401이라 백엔드가 쿠키를 지워주지 못한다 — 직접 만료시킨다.
      await clearSessionCookie();
    }
    router.replace("/login");
  }, [router]);

  const value = React.useMemo(
    () => ({ user, loading, logout }),
    [user, loading, logout],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
