"use client";

import "@/app/console.css";
import { useState } from "react";
import { usePolling } from "@/lib/use-polling";
import Link from "next/link";
import { ArrowUpRight, CalendarDays, Check, CircleAlert, FileKey2, Network, Radar, RefreshCw, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { CertStatusBadge, DdayText } from "@/components/cert-badges";
import { ScanStatusCard } from "@/components/charts/scan-status-card";
import { IssuerDistributionCard } from "@/components/charts/issuer-distribution";
import { getCertificateSummary, listCertificates, type Certificate, type CertificateSummary, type CertificateScope } from "@/lib/api";
import { canManageInventory, isSystemAdministrator, useAuth } from "@/lib/auth";
import { dnCommonName, formatDate, formatDateTime } from "@/lib/format";

type Data = { scope: CertificateScope; summary: CertificateSummary; certificates: Certificate[]; total: number; recent: Certificate[]; expired: Certificate[]; updatedAt: Date };
async function loadData(scope: CertificateScope): Promise<Data> {
  const horizon = async () => {
    const certificates: Certificate[] = []; let total = 0;
    for (let page = 1; page <= 3; page++) {
      const result = await listCertificates(new URLSearchParams({ scope, ...(scope === "inventory" ? { exclude_status: "replaced" } : {}), expires_within_days: "90", sort: "not_after", order: "asc", page: String(page), page_size: "200" }));
      certificates.push(...result.items); total = result.total;
      if (certificates.length >= total || result.items.length < 200) break;
    }
    return { certificates, total };
  };
  const [summary, upcoming, recent, expired] = await Promise.all([
    getCertificateSummary(scope), horizon(),
    listCertificates(new URLSearchParams({ scope, ...(scope === "inventory" ? { exclude_status: "replaced" } : {}), sort: "created_at", order: "desc", page_size: "5" })),
    listCertificates(new URLSearchParams({ scope, ...(scope === "inventory" ? { exclude_status: "replaced" } : {}), expired: "true", sort: "not_after", order: "desc", page_size: "8" })),
  ]);
  return { scope, summary, ...upcoming, recent: recent.items, expired: expired.items, updatedAt: new Date() };
}

export function ConsoleDashboard() {
  const [scope, setScope] = useState<CertificateScope>("server");
  return <div><div className="priority-tabs" role="group" aria-label="인증서 집계 범위">{(["server", "inventory"] as const).map(value => <button key={value} type="button" aria-pressed={scope === value} onClick={() => setScope(value)}>{value === "server" ? "서버 인증서" : "모든 관리자 보유 인증서"}</button>)}</div><ScopedDashboard key={scope} scope={scope} /></div>;
}

function ScopedDashboard({ scope }: { scope: CertificateScope }) {
  const [data, setData] = useState<Data | null>(null); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const { user } = useAuth(); const canScan = canManageInventory(user);
  const refresh = usePolling(async (current) => {
    await loadData(scope).then((result) => { if (current()) { setData(result); setError(""); setBusy(false); } }).catch((e: unknown) => { if (current()) { setError(e instanceof Error ? e.message : "운영 현황을 불러오지 못했습니다."); setBusy(false); } });
  }, [scope]);
  return <div className="operations-dashboard">
    <div className="operations-heading"><div><h1>인증서 운영</h1></div><div className="operations-heading-actions"><Button variant="outline" disabled={busy} onClick={() => { setBusy(true); refresh(); }}><RefreshCw size={15} className={busy ? "animate-spin" : ""} />새로고침</Button>{canScan && <Button asChild><Link href="/discovery"><Radar size={16} />인증서 스캔</Link></Button>}</div></div>
    {error && <Alert variant="destructive"><AlertDescription>{error} 새로고침으로 다시 시도하세요.</AlertDescription></Alert>}
    {!data ? !error && <div className="space-y-5" aria-label="운영 현황 불러오는 중"><Skeleton className="h-40 w-full" /><Skeleton className="h-72 w-full" /><Skeleton className="h-80 w-full" /></div> : <>
      <section className="operations-overview" aria-label="인증서 요약">
        <div className="overview-caption"><div><ShieldCheck size={19} /><strong>관리 현황</strong></div><span>{formatDateTime(data.updatedAt.toISOString())} 기준</span></div>
        <div className="overview-metrics">
          <Metric label="전체 인증서" count={data.summary.total} href={scopeHref(scope)} note="등록된 인증서" />
          <Metric label="이미 만료" count={data.summary.expired} href={scopeHref(scope, "expired=true")} tone="danger" note="서비스 영향 확인" />
          <Metric label="7일 이내 만료" count={data.summary.expiring_7d} href={scopeHref(scope, "expires_within_days=7")} tone="warn" note="우선 교체 검토" />
          <Metric label="30일 이내 만료" count={data.summary.expiring_30d} href={scopeHref(scope, "expires_within_days=30")} note="교체 일정 준비" />
        </div>
      </section>
      <div className="operations-main-grid">
        <div className="operations-primary">
          <ExpirySchedule data={data} />
          <PriorityCertificates data={data} />
          <section className="operations-surface recent-surface"><div className="surface-heading"><div><h2>최근 등록</h2></div><Link href={scopeHref(scope, "sort=created_at&order=desc")} className="text-link">전체 목록<ArrowUpRight size={15} /></Link></div>
            {data.recent.length ? data.recent.map((cert) => <Link className="recent-certificate" key={cert.id} href={`/certificates/${cert.id}`}><span className="certificate-glyph"><FileKey2 size={19} /></span><span className="recent-name"><strong>{cert.common_name || dnCommonName(cert.subject_dn)}</strong><small>{cert.issuer_organization || dnCommonName(cert.issuer_dn)}</small></span><span className="recent-date">{formatDate(cert.not_after)} 만료</span><ArrowUpRight size={15} /></Link>) : <Empty text="아직 등록된 인증서가 없습니다." href="/discovery" action="인증서 스캔으로 시작" />}
          </section>
        </div>
        <aside className="operations-secondary" aria-label="운영 작업과 분포">
          <section className="operations-surface next-actions"><div className="surface-heading"><div><h2>다음 작업</h2></div></div>
            <ActionLink href="/discovery" title="네트워크 인증서 스캔" description="대역과 센서를 선택해 스캔" icon={Radar} />
            <ActionLink href="/issuance" title="발급 · 배포 확인" description="발급 주문과 적용 결과 확인" icon={FileKey2} />
            <ActionLink href={isSystemAdministrator(user) ? "/agents" : "/sensors"} title="네트워크 연결 관리" description="센서와 장비 연결 상태 확인" icon={Network} />
          </section>
          <div className="dashboard-module"><ScanStatusCard /></div>
          <div className="dashboard-module"><IssuerDistributionCard key={scope} scope={scope} /></div>
          <div className="inventory-notes"><span>자체 서명<strong>{data.summary.self_signed.toLocaleString()}건</strong></span><span>담당자 미지정<strong>{data.summary.unowned.toLocaleString()}건</strong></span></div>
        </aside>
      </div>
    </>}
  </div>;
}
function scopeHref(scope: CertificateScope, query = "") { const params = new URLSearchParams(query); params.set("scope", scope); return `/certificates?${params}`; }
function Metric({label,count,href,tone="neutral"}:{label:string;count:number;href:string;note:string;tone?:string}) {
  return <Link href={href} className={`overview-metric tone-${tone}`}><span className="metric-label">{label}<ArrowUpRight size={14} /></span><strong>{count.toLocaleString()}<small>건</small></strong></Link>;
}
function ExpirySchedule({data}:{data:Data}) {
  const weeks = data.summary.expiry_weeks.map((count,index) => ({ start:index*7, count }));
  const max=Math.max(1,...weeks.map((week)=>week.count));
  return <section className="operations-surface expiry-schedule"><div className="surface-heading"><div><h2><CalendarDays size={18} />앞으로의 만료 일정</h2></div><span className="schedule-total">{data.summary.expiring_90d.toLocaleString()}<small>건</small></span></div>
    <div className="expiry-chart" aria-label="주간 인증서 만료 분포">
      {weeks.map((week,index) => {const days=index===0?7:index<4?30:index<8?60:90;return <Link key={index} href={scopeHref(data.scope, `expires_within_days=${days}`)} className={`expiry-week ${index===0?"week-urgent":index<4?"week-soon":""}`} aria-label={`${week.start}~${Math.min(90,week.start+7)}일 구간 ${week.count}건, ${days}일 이내 목록 보기`} title={`${week.start}~${Math.min(90,week.start+7)}일 구간: ${week.count}건`}><span className="week-count">{week.count>0?week.count:""}</span><span className="week-track"><i style={{height:week.count?`${Math.max(8,week.count/max*100)}%`:"3px"}} /></span><span className="week-label">{index===0?"이번 주":`${index+1}주`}</span></Link>;})}
    </div>
    <div className="schedule-footer"><span><i className="legend-urgent" />이번 주</span><span><i className="legend-soon" />2~4주</span><span><i className="legend-later" />5~13주</span><p>{"구간을 선택하면 해당 기간의 인증서 목록으로 이동합니다."}</p></div>
  </section>;
}
function PriorityCertificates({data}:{data:Data}) {
  const [tab,setTab]=useState<"soon"|"expired">("soon");
  const certs=tab==="expired"?data.expired:data.certificates.filter((cert)=>{const days=(Date.parse(cert.not_after)-data.updatedAt.getTime())/86400000;return days>=0&&days<30;}).slice(0,8);
  const href=scopeHref(data.scope, tab==="expired"?"expired=true":"expires_within_days=30");
  return <section className="operations-surface"><div className="surface-heading"><div><h2>우선 확인할 인증서</h2></div><Link href={href} className="text-link">전체 보기<ArrowUpRight size={15} /></Link></div><div className="priority-tabs" role="group" aria-label="우선 확인 범위"><button aria-pressed={tab==="soon"} onClick={()=>setTab("soon")}>30일 이내 만료<span>{data.summary.expiring_30d}</span></button><button aria-pressed={tab==="expired"} onClick={()=>setTab("expired")}>이미 만료<span>{data.summary.expired}</span></button></div>
    {certs.length ? <div className="overflow-x-auto"><table className="operations-table"><thead><tr><th>인증서 이름</th><th>만료일</th><th>상태</th><th aria-label="상세 보기" /></tr></thead><tbody>{certs.map((cert)=><tr key={cert.id}><td><Link href={`/certificates/${cert.id}`} className="table-cert-name">{cert.common_name||dnCommonName(cert.subject_dn)}</Link><span className="table-cert-issuer">{cert.issuer_organization||dnCommonName(cert.issuer_dn)}</span></td><td><span>{formatDate(cert.not_after)}</span><div><DdayText notAfter={cert.not_after} /></div></td><td><CertStatusBadge status={cert.status} notAfter={cert.not_after} /></td><td><Link href={`/certificates/${cert.id}`} aria-label={`${cert.common_name||dnCommonName(cert.subject_dn)} 상세 보기`}><ArrowUpRight size={16} /></Link></td></tr>)}</tbody></table></div> : <div className="operations-empty"><Check size={22} /><strong>이 범위에 해당하는 인증서가 없습니다.</strong><p>스캔을 통해 최신 인증서 상태를 계속 확인하세요.</p></div>}
  </section>;
}
function ActionLink({href,title,icon:Icon}:{href:string;title:string;description:string;icon:typeof Radar}) {return <Link href={href} className="operation-action"><Icon size={19} /><span><strong>{title}</strong></span><Chevron /></Link>;}
function Chevron(){return <ArrowUpRight size={15} />;}
function Empty({text,href,action}:{text:string;href:string;action:string}){return <div className="operations-empty"><CircleAlert size={22}/><p>{text}</p><Link className="text-link" href={href}>{action}</Link></div>;}
