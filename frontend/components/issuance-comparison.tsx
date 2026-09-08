import type { IssuanceComparison } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const match = (value: boolean | null) => value === null ? "확인 불가" : value ? "일치" : "다름";
const date = (value: string) => new Date(value).toLocaleString("ko-KR");

export function IssuanceComparisonPanel({ comparison: c }: { comparison?: IssuanceComparison }) {
  if (!c) return null;
  const rows = [
    ["유효기간", `${c.requested.validity_days}일`, `${c.validity_days.toLocaleString("ko-KR", { maximumFractionDigits: 3 })}일 · ${date(c.actual.not_before)} ~ ${date(c.actual.not_after)}`, match(c.validity_matches)],
    ["DNS 도메인", c.requested.dns_names.join(", "), c.actual.dns_names.join(", ") || "DNS SAN 없음", match(c.dns_matches)],
    ["키 종류·크기", c.requested.key_algorithm, `${c.actual.key_algorithm} ${c.actual.key_size}비트`, match(c.requested_key_matches)],
    ["CSR 공개키 SHA-256", c.csr_public_key_sha256 || "확인 불가", c.actual.public_key_sha256, match(c.csr_key_matches)],
  ];
  return (
    <Card className="min-w-0 gap-3 overflow-hidden p-4">
      <h2 className="font-semibold">요청·발급 비교</h2>
      <Table className="min-w-[720px]">
        <TableHeader><TableRow><TableHead>검사</TableHead><TableHead>요청</TableHead><TableHead>실제 발급</TableHead> <TableHead>비교</TableHead></TableRow></TableHeader>
        <TableBody>{rows.map(([label, requested, actual, result]) => <TableRow key={label}>
          <TableCell className="align-top font-medium">{label}</TableCell>
          <TableCell className="max-w-64 whitespace-normal break-all align-top">{requested}</TableCell>
          <TableCell className="max-w-96 whitespace-normal break-all align-top">{actual}</TableCell>
          <TableCell className="align-top">{result}</TableCell>
        </TableRow>)}</TableBody>
      </Table>
      <p className="text-sm">발급 CN(보조 정보): {c.actual.common_name || "없음"} · CSR 서명: {c.csr_signature_valid ? "정상" : "확인 불가"} · 검사 당시 기간: {c.currently_valid ? "유효" : "유효기간 밖"}</p>
      <p className="text-sm">요청 개인키 연결: {c.csr_key_matches === true ? "공개키 일치" : "연결하지 않음"}</p>
      <h3 className="font-medium">인증서에 기록된 발급자</h3>
      <dl className="grid gap-1 text-sm">
        <div className="break-all"><dt className="inline font-medium">Issuer: </dt><dd className="inline">{c.actual.issuer}</dd></div>
        <div className="break-all"><dt className="inline font-medium">AKI: </dt><dd className="inline">{c.actual.aki || "없음"}</dd></div>
      </dl>
      <h3 className="font-medium">CA가 함께 전달한 체인</h3>
      <p className="text-sm text-muted-foreground">받은 순서 그대로 표시합니다. 확인하지 못한 상위 체인을 추정하거나 보정하지 않습니다.</p>
      {!c.chain_parseable && <p className="text-sm text-amber-700">체인 전체를 읽을 수 없습니다. 원문은 보존했습니다.</p>}
      <Table className="min-w-[640px]">
        <TableHeader><TableRow><TableHead>순서</TableHead><TableHead>전달된 인증서</TableHead><TableHead>앞 인증서 서명</TableHead></TableRow></TableHeader>
        <TableBody>{c.provided_chain.map((cert, i) => <TableRow key={`${i}-${cert.fingerprint_sha256}`}>
          <TableCell className="align-top text-right">{i + 1}</TableCell>
          <TableCell className="max-w-xl whitespace-normal break-all">
            <div>Subject: {cert.subject}</div><div>Issuer: {cert.issuer}</div>
            <div>SKI: {cert.ski || "없음"}</div><div>AKI: {cert.aki || "없음"}</div>
            <div>SHA-256: {cert.fingerprint_sha256}</div>
          </TableCell>
          <TableCell className="align-top">{cert.signs_previous === undefined ? "확인 불가" : cert.signs_previous ? "검증 성공" : "검증 실패"}{i === 0 && <span className="block text-xs">발급 인증서 기준</span>}</TableCell>
        </TableRow>)}</TableBody>
      </Table>
      <p className="break-all text-xs text-muted-foreground">CA 체인 원문 SHA-256: {c.chain_source_sha256} · 검사 {date(c.checked_at)}</p>
    </Card>
  );
}
