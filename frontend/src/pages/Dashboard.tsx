import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Plus, TrendingDown, TrendingUp } from "lucide-react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api, type AnalyticsReport, type Domain } from "../lib/api";
import { AppShell } from "../components/AppShell";
import { CreateLinkDialog } from "../components/CreateLinkDialog";
import { Badge, Button, Card, Spinner } from "../components/ui";
import { formatNumber } from "../lib/utils";

type Range = "7d" | "30d" | "90d" | "1y";

export function DashboardPage() {
  const [report, setReport] = useState<AnalyticsReport | null>(null);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [range, setRange] = useState<Range>("30d");
  const [createOpen, setCreateOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const requestID = useRef(0);

  const filters = useMemo(() => ({ range }), [range]);
  const load = useCallback(async (force = false) => {
    const id = ++requestID.current;
    setLoading(true);
    setError("");
    try {
      const [nextReport, nextDomains] = await Promise.all([
        api.analyticsReport(filters, undefined, force),
        api.listDomains(),
      ]);
      if (id === requestID.current) { setReport(nextReport); setDomains(nextDomains); }
    } catch (reason: unknown) {
      if (id === requestID.current) setError(reason instanceof Error ? reason.message : "Could not load dashboard");
    } finally {
      if (id === requestID.current) setLoading(false);
    }
  }, [filters]);

  useEffect(() => { void load(false); }, [load]);

  const overview = report?.overview;
  const delta = overview?.total_clicks_delta ?? 0;

  return (
    <AppShell>
      <div className="p-4 sm:p-7 space-y-5 sm:space-y-7">
        <div className="flex items-center justify-between">
          <div><h1 className="text-xl sm:text-2xl font-bold">Analytics Overview</h1><p className="text-sm text-muted-foreground mt-0.5 hidden sm:block">Fast workspace snapshot from the same analytics engine</p></div>
          <Button onClick={() => setCreateOpen(true)} size="sm" className="sm:h-9 sm:text-sm sm:px-4"><Plus className="w-4 h-4"/><span className="hidden sm:inline ml-1">Create Link</span></Button>
        </div>

        {error && <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{error}</div>}
        {loading && !report ? <div className="flex justify-center py-12"><Spinner/></div> : report && <>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3 sm:gap-4">
            <StatCard label="CLICKS IN RANGE" value={formatNumber(overview?.total_clicks ?? 0)} sub={range === "1y" ? "Last year" : `Last ${range.replace("d", " days")}`} delta={overview?.delta_available ? delta : undefined}/>
            <StatCard label="UNIQUE VISITORS" value={overview?.analytics_enabled ? formatNumber(overview.unique_visitors) : "N/A"} sub={overview?.analytics_enabled ? `${overview.detailed_coverage}% detail coverage` : "Analytics disabled"}/>
            <StatCard label="REPEAT RATE" value={overview?.analytics_enabled ? `${overview.repeat_click_rate}%` : "N/A"} sub={overview?.analytics_enabled ? "Anonymous detailed events" : "Analytics disabled"}/>
            <StatCard label="ACTIVE LINKS" value={formatNumber(overview?.active_links ?? 0)} sub={`${formatNumber(overview?.all_time_clicks ?? 0)} currently counted clicks`}/>
          </div>

          <Card>
            <div className="flex items-center justify-between mb-4">
              <div><p className="font-semibold text-sm sm:text-base">Click Performance</p><p className="text-xs text-muted-foreground mt-0.5 hidden sm:block">One consistent report snapshot; unique visitors appear when detailed analytics is enabled</p></div>
              <div className="flex gap-1">{(["7d","30d","90d","1y"] as Range[]).map((item)=><button key={item} onClick={()=>setRange(item)} className={`px-2 py-1 text-xs rounded-md font-medium transition-colors ${range===item?"bg-primary text-primary-foreground":"text-muted-foreground hover:text-foreground hover:bg-accent"}`}>{item}</button>)}</div>
            </div>
            <ResponsiveContainer width="100%" height={190}>
              <AreaChart data={report.timeseries}>
                <defs><linearGradient id="gClicks" x1="0" y1="0" x2="0" y2="1"><stop offset="5%" stopColor="var(--color-primary)" stopOpacity={0.3}/><stop offset="95%" stopColor="var(--color-primary)" stopOpacity={0}/></linearGradient></defs>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false}/><XAxis dataKey="day" minTickGap={24} tick={{fontSize:9}} stroke="var(--color-border)"/><YAxis allowDecimals={false} tick={{fontSize:9}} stroke="var(--color-border)" width={30}/><Tooltip contentStyle={{background:"var(--color-card)",border:"1px solid var(--color-border)",borderRadius:"8px",fontSize:"12px"}}/><Area type="monotone" dataKey="clicks" stroke="var(--color-primary)" fill="url(#gClicks)" strokeWidth={2}/>{overview?.analytics_enabled&&<Area type="monotone" dataKey="unique" stroke="var(--color-chart-3)" fill="none" strokeWidth={1.5} strokeDasharray="4 4"/>}
              </AreaChart>
            </ResponsiveContainer>
          </Card>

          <Card>
            <div className="flex items-center justify-between mb-4"><p className="font-semibold">Top Performing Links</p><Link to="/links" className="text-xs text-primary hover:underline">View all →</Link></div>
            <div className="space-y-3">{report.top_links.length===0&&<p className="text-xs text-muted-foreground py-4 text-center">No clicks in this range</p>}{report.top_links.slice(0,5).map((item)=>{const shortURL=item.redirect_type==="subdomain"?`https://${item.short_code}.${item.domain}`:`https://${item.domain}/${item.short_code}`;return <div key={item.id} className="flex items-center justify-between gap-3"><div className="min-w-0 flex-1"><a href={shortURL} target="_blank" rel="noreferrer noopener" className="text-xs font-mono text-primary hover:underline truncate block">{shortURL.replace(/^https?:\/\//,"")}</a><p className="text-xs text-muted-foreground truncate">{item.destination_url}</p></div><Badge variant="outline" className="shrink-0">{formatNumber(item.clicks)}</Badge></div>;})}</div>
          </Card>
        </>}
      </div>
      <CreateLinkDialog open={createOpen} onClose={()=>setCreateOpen(false)} onCreated={()=>{setCreateOpen(false);void load(true);}} domains={domains}/>
    </AppShell>
  );
}

function StatCard({label,value,sub,delta}:{label:string;value:string;sub:string;delta?:number}) { return <Card className="p-3 sm:p-5"><p className="text-[10px] sm:text-xs font-medium text-muted-foreground tracking-wide">{label}</p><p className="text-xl sm:text-2xl font-bold mt-1 mb-0.5">{value}</p><div className="flex items-center gap-1">{delta!==undefined&&delta!==0&&(delta>0?<TrendingUp className="w-3 h-3 text-success"/>:<TrendingDown className="w-3 h-3 text-destructive"/>)}{delta!==undefined&&delta!==0&&<span className={`text-xs font-medium ${delta>0?"text-success":"text-destructive"}`}>{delta>0?"+":""}{delta.toFixed(1)}%</span>}<span className="text-xs text-muted-foreground truncate">{sub}</span></div></Card>; }
