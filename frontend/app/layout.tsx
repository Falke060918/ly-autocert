import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "INHERIT AutoCert",
  description: "온프레미스 인증서 라이프사이클 관리(CLM) 플랫폼",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="ko" className="h-full antialiased">
      <body className="min-h-full">{children}</body>
    </html>
  );
}
