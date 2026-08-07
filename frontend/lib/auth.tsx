"use client";

/**
 * 세션 사용자 컨텍스트.
 * proxy.ts가 쿠키 존재만 검사하는 1차 관문이고, 여기서 /auth/me로 실제 세션을
 * 검증한다 — 만료·폐기된 세션은 401을 받아 로그인으로 보낸다.
 */

import * as React from "react";
import { usePathname, useRouter } from "next/navigation";

import { ApiError, fetchMe, logout as apiLogout, type Role, type User } from "@/lib/api";

/** 인벤토리 변경(업로드·스캔 생성)이 허용되는 역할 — 백엔드 protected 등록과 일치. */
const MANAGE_ROLES: readonly Role[] = [
  "system_administrator",
  "pki_administrator",
  "operator",
];

export function canManageInventory(user: User | null): boolean {
  return user != null && MANAGE_ROLES.includes(user.role);
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
  const pathname = usePathname();
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
          const next = pathname === "/" ? "" : `?next=${encodeURIComponent(pathname)}`;
          router.replace(`/login${next}`);
        }
      });
    return () => {
      cancelled = true;
    };
    // pathname은 최초 마운트 시점 값만 쓴다 — 이동마다 me를 다시 부르지 않는다.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const logout = React.useCallback(async () => {
    try {
      await apiLogout();
    } catch {
      // 세션이 이미 만료됐어도 로그인 화면으로 보낸다.
    }
    router.replace("/login");
  }, [router]);

  const value = React.useMemo(
    () => ({ user, loading, logout }),
    [user, loading, logout],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
