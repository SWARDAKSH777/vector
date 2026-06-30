import React, { useMemo, useState } from "react";
import {
  Activity, BarChart3, CalendarRange, Clock3, DatabaseZap, Globe2, Info,
  Link2, MapPin, MousePointerClick, RefreshCw, Repeat2, ShieldCheck, Users,
} from "lucide-react";
import {
  Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, Legend, Pie, PieChart,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";
import { AppShell } from "../components/AppShell";
import { WorldAnalyticsMap, countryName } from "../components/WorldAnalyticsMap";
import { Badge, Button, Card, Select, Spinner } from "../components/ui";
import { useAnalyticsReport } from "../hooks/useAnalyticsReport";
import type { AnalyticsFilters, GroupedStat } from "../lib/api";
import { formatNumber } from "../lib/utils";

const COLORS = [
  "var(--color-chart-1)", "var(--color-chart-2)", "var(--color-chart-3)",
  "var(--color-chart-4)", "var(--color-chart-5)",
];
type Range = "7d" | "30d" | "90d" | "1y" | "all";

// Shorten a referrer source for display.
// "Direct" stays as-is. Valid URLs get their hostname (no www.).
// Anything else is truncated at 22 chars.
function shortLabel(value: string): string {
  if (!value || value === "Direct") return value || "Direct";
  try {
    const u = new URL(value);
    return u.hostname.replace(/^www\./, "");
  } catch {
    return value.length > 22 ? value.slice(0, 22) + "…" : value;
  }
}

function formatTimestamp(value: string): string {
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString();
}

export function AnalyticsPage() {
  const [range, setRange] = useState<Range>("30d");
  const [linkID, setLinkID] = useState(0);
  const [country, setCountry] = useState("");
  const [device, setDevice] = useState("");
  const [browser, setBrowser] = useState("");
  const [referrer, setReferrer] = useState("");

  const filters = useMemo<AnalyticsFilters>(() => ({
    range,
    link_id: linkID || undefined,
    country: country || undefined,
    device: device || undefined,
    browser: browser || undefined,
    // When the user picks "Direct" from the dropdown, pass "Direct" to the
    // backend. The backend WHERE clause uses COALESCE(NULLIF(referrer,''),'Direct')
    // so this matches rows with an empty referrer column.
    referrer: referrer || undefined,
  }), [range, linkID, country, device, browser, referrer]);

  const { report, error, loading, refreshing, reload } = useAnalyticsReport(filters);
  const filterCount = [linkID, country, device, browser, referrer].filter(Boolean).length;
  const overview = report?.overview;
  const geo = report?.geo;
  const options = report?.options;
  const browserOptions = useMemo(
    () => Array.from(new Set([...(options?.browsers ?? []), "Other", "Privacy-protected"])).sort(),
    [options?.browsers],
  );
  const analyticsUsable = Boolean(overview?.analytics_enabled);
  const audienceUsable = analyticsUsable && (overview?.identified_clicks ?? 0) > 0;
  const hasDetailedEvents = (overview?.detailed_clicks ?? 0) > 0;
  const maxHourClicks = Math.max(0, ...(report?.hours ?? []).map((h) => h.clicks));
  const peakHour = maxHourClicks > 0
    ? (report?.hours ?? []).reduce((best, item) => item.clicks > best.clicks ? item : best, { hour: 0, clicks: 0 })
    : null;

  function resetFilters() {
    setLinkID(0); setCountry(""); setDevice(""); setBrowser(""); setReferrer("");
  }

  return (
    <AppShell>
      <div className="p-3 sm:p-5 lg:p-7 space-y-4 sm:space-y-6">
        {/* ── Header ── */}
        <header className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="flex items-center gap-2 flex-wrap">
              <h1 className="text-xl sm:text-2xl font-bold">Analytics</h1>
              {filterCount > 0 && (
                <Badge variant="outline">{filterCount} active filter{filterCount === 1 ? "" : "s"}</Badge>
              )}
              {refreshing && report && <Badge variant="outline">Refreshing</Badge>}
            </div>
            <p className="text-sm text-muted-foreground mt-0.5">
              Clicks, audience, geography and traffic quality — one consistent snapshot
            </p>
          </div>
          <div className="flex items-center gap-2 self-stretch sm:self-auto">
            <div className="flex flex-1 sm:flex-none gap-1 rounded-lg bg-muted p-1 overflow-x-auto">
              {(["7d", "30d", "90d", "1y", "all"] as Range[]).map((r) => (
                <button
                  key={r}
                  onClick={() => setRange(r)}
                  className={`px-2.5 py-1.5 text-xs rounded-md font-medium whitespace-nowrap transition-colors ${
                    range === r
                      ? "bg-card text-foreground shadow-sm"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {r === "all" ? "All time" : r}
                </button>
              ))}
            </div>
            <Button
              variant="secondary"
              size="sm"
              onClick={reload}
              loading={refreshing}
              aria-label="Refresh analytics"
            >
              {!refreshing && <RefreshCw className="w-3.5 h-3.5" />}
            </Button>
          </div>
        </header>

        {/* ── Filters ── */}
        <Card className="p-3 sm:p-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-5 gap-3">
            <Select label="Link" value={linkID} onChange={(e) => setLinkID(Number(e.target.value))}>
              <option value={0}>All links</option>
              {(options?.links ?? []).map((l) => (
                <option key={l.id} value={l.id}>{l.domain}/{l.short_code}</option>
              ))}
            </Select>
            <Select label="Device" value={device} onChange={(e) => setDevice(e.target.value)}>
              <option value="">All devices</option>
              {(options?.devices ?? []).map((d) => <option key={d} value={d}>{d}</option>)}
            </Select>
            <Select label="Browser" value={browser} onChange={(e) => setBrowser(e.target.value)}>
              <option value="">All browsers</option>
              {browserOptions.map((b) => <option key={b} value={b}>{b}</option>)}
            </Select>
            <Select label="Referrer" value={referrer} onChange={(e) => setReferrer(e.target.value)}>
              <option value="">All referrers</option>
              {(options?.referrers ?? []).map((ref) => (
                <option key={ref} value={ref}>{shortLabel(ref)}</option>
              ))}
            </Select>
            <div className="flex items-end gap-2">
              <div className="min-w-0 flex-1 rounded-lg border border-border bg-muted/30 px-3 py-2">
                <p className="text-[11px] text-muted-foreground">Country</p>
                <p className="text-sm font-medium truncate">
                  {country ? countryName(country) : "Choose on map"}
                </p>
              </div>
              {filterCount > 0 && (
                <Button variant="ghost" size="sm" onClick={resetFilters} className="shrink-0">
                  Clear
                </Button>
              )}
            </div>
          </div>
        </Card>

        {/* ── Error ── */}
        {error && (
          <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {loading ? (
          <div className="flex justify-center py-24"><Spinner className="w-6 h-6" /></div>
        ) : report && (
          <>
            {/* ── Notices ── */}
            {!overview?.analytics_enabled && (
              <Notice>
                Detailed analytics is disabled. Aggregate click counters still work, but browser,
                device, geography and unique-visitor data will not be collected until it is enabled
                in Settings.
              </Notice>
            )}
            {analyticsUsable && (overview?.privacy_protected_clicks ?? 0) > 0 && (
        <Notice>
          {formatNumber(overview?.privacy_protected_clicks ?? 0)} click
          {(overview?.privacy_protected_clicks ?? 0) === 1 ? "" : "s"} honored GPC or DNT.
          They are grouped as Privacy-protected without a visitor identifier, IP-derived
          country, real referrer, or user-agent classification.
        </Notice>
      )}

      {overview?.analytics_enabled && hasDetailedEvents
              && (geo?.total_clicks ?? 0) > 0
              && (geo?.located_clicks ?? 0) === 0 && (
              <Notice>
                No country values have arrived yet. Enable Cloudflare Network → IP Geolocation or
                the "Add visitor location headers" Managed Transform. IPinfo Lite remains an
                optional fallback in Settings.
              </Notice>
            )}
            {!report.capture.healthy && (
              <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive flex items-start gap-2">
                <Info className="w-4 h-4 mt-0.5 shrink-0" />
                <span>
                  Analytics capture reported an error. New redirects still work safely, but inspect
                  server logs. Last error: {report.capture.last_error || "unknown"}
                </span>
              </div>
            )}

            {/* ── KPI cards ── */}
            <div className="grid grid-cols-2 lg:grid-cols-4 2xl:grid-cols-8 gap-3 sm:gap-4">
              <MetricCard icon={MousePointerClick} label="All clicks" value={formatNumber(overview?.all_time_clicks ?? 0)} hint="Current link counters" />
              <MetricCard icon={CalendarRange} label="Clicks in range" value={formatNumber(overview?.total_clicks ?? 0)} hint={overview?.data_mode === "detailed" ? "Filtered detailed events" : "Complete aggregate count"} />
              <MetricCard
        icon={Users}
        label="Unique visitors"
        value={audienceUsable ? formatNumber(overview?.unique_visitors ?? 0) : "N/A"}
        hint={`${formatNumber(overview?.identified_clicks ?? 0)} eligible clicks`}
      />
              <MetricCard icon={Activity} label="Daily average" value={String(overview?.avg_daily_clicks ?? 0)} hint={`${overview?.range_days ?? 0} calendar days`} />
              <MetricCard
        icon={Repeat2}
        label="Repeat rate"
        value={audienceUsable ? `${overview?.repeat_click_rate ?? 0}%` : "N/A"}
        hint="Among identifiable detailed clicks only"
      />
      <MetricCard
        icon={ShieldCheck}
        label="Privacy-protected"
        value={analyticsUsable ? formatNumber(overview?.privacy_protected_clicks ?? 0) : "N/A"}
        hint="GPC/DNT; no visitor ID or geo"
      />
              <MetricCard icon={DatabaseZap} label="Detail coverage" value={analyticsUsable ? `${overview?.detailed_coverage ?? 0}%` : "N/A"} hint={`${formatNumber(overview?.detailed_clicks ?? 0)} captured events`} />
              <MetricCard icon={Globe2} label="Geo coverage" value={analyticsUsable ? `${geo?.coverage ?? 0}%` : "N/A"} hint={`${formatNumber(geo?.located_clicks ?? 0)} country matches`} />
            </div>

            {/* ── Timeseries ── */}
            <Card>
              <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between mb-4">
                <div>
                  <p className="font-semibold">Click performance</p>
                  <p className="text-xs text-muted-foreground">
                    {report.granularity === "day" ? "Daily" : report.granularity === "week" ? "Weekly" : "Monthly"} UTC buckets from the same report snapshot
                  </p>
                </div>
                <div className="flex items-center gap-2 text-xs flex-wrap justify-end">
                  {overview?.delta_available ? (
                    <span className={`font-medium ${(overview.total_clicks_delta ?? 0) >= 0 ? "text-success" : "text-destructive"}`}>
                      {overview.total_clicks_delta >= 0 ? "+" : ""}{overview.total_clicks_delta}% vs previous period
                    </span>
                  ) : (
                    <span className="text-muted-foreground">No previous-period baseline</span>
                  )}
                  <Badge variant="outline">v{report.schema_version}</Badge>
                </div>
              </div>
              <div className="h-[250px] sm:h-[320px]">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={report.timeseries} margin={{ left: -18, right: 6, top: 8, bottom: 0 }}>
                    <defs>
                      <linearGradient id="analyticsClicks" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="var(--color-primary)" stopOpacity={0.32} />
                        <stop offset="95%" stopColor="var(--color-primary)" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
                    <XAxis dataKey="day" minTickGap={24} tick={{ fontSize: 10 }} stroke="var(--color-border)" />
                    <YAxis allowDecimals={false} tick={{ fontSize: 10 }} stroke="var(--color-border)" />
                    <Tooltip
                      contentStyle={tooltipStyle}
                      labelFormatter={(_, payload) => payload?.[0]?.payload?.date || ""}
                    />
                    <Legend iconType="circle" iconSize={7} wrapperStyle={{ fontSize: 11 }} />
                    <Area type="monotone" dataKey="clicks" name="Clicks" stroke="var(--color-primary)" fill="url(#analyticsClicks)" strokeWidth={2} />
                    {analyticsUsable && (
                      <Area type="monotone" dataKey="unique" name="Unique visitors" stroke="var(--color-chart-3)" fill="none" strokeWidth={1.7} strokeDasharray="5 4" />
                    )}
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            </Card>

            {/* ── Geo map + top countries ── */}
            <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1.55fr)_minmax(300px,0.75fr)] gap-4">
              <Card>
                <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between mb-4">
                  <div>
                    <p className="font-semibold flex items-center gap-2">
                      <MapPin className="w-4 h-4 text-primary" />Global traffic map
                    </p>
                    <p className="text-xs text-muted-foreground">
                      Verified Cloudflare country data with optional IPinfo fallback; select a country to filter every panel
                    </p>
                  </div>
                  {country && (
                    <Button variant="ghost" size="sm" onClick={() => setCountry("")}>Show world</Button>
                  )}
                </div>
                {(geo?.countries.length ?? 0) === 0 ? (
                  <EmptyPanel text="No country data yet. Enable Cloudflare IP Geolocation / location headers; add IPinfo in Settings as a fallback." />
                ) : (
                  <WorldAnalyticsMap data={geo?.countries ?? []} selectedCountry={country} onSelectCountry={setCountry} />
                )}
              </Card>
              <Card>
                <p className="font-semibold mb-1">Top countries</p>
                <p className="text-xs text-muted-foreground mb-4">Country share and anonymous unique visitors</p>
                {(geo?.countries.length ?? 0) === 0 ? (
                  <EmptyPanel text="No country data" compact />
                ) : (
                  <div className="space-y-3">
                    {geo!.countries.slice(0, 10).map((item, index) => {
                      const share = geo!.located_clicks > 0
                        ? Math.round(item.clicks / geo!.located_clicks * 1000) / 10
                        : 0;
                      return (
                        <button
                          key={item.code}
                          onClick={() => setCountry(item.code)}
                          className="w-full flex items-center gap-3 text-left group"
                        >
                          <span className="w-6 text-xs text-muted-foreground tabular-nums">{index + 1}</span>
                          <div className="min-w-0 flex-1">
                            <p className="text-sm font-medium truncate group-hover:text-primary">{countryName(item.code)}</p>
                            <div className="mt-1 h-1.5 rounded-full bg-muted overflow-hidden">
                              <div className="h-full rounded-full bg-primary/80" style={{ width: `${Math.max(share, 1)}%` }} />
                            </div>
                          </div>
                          <div className="text-right">
                            <p className="text-sm font-semibold tabular-nums">{formatNumber(item.clicks)}</p>
                            <p className="text-[10px] text-muted-foreground">{share}% · {formatNumber(item.unique)} unique</p>
                          </div>
                        </button>
                      );
                    })}
                  </div>
                )}
              </Card>
            </div>

            {/* ── Devices / Browsers / Peak hours ── */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
              <Card>
                <p className="font-semibold mb-1">Devices</p>
                <p className="text-xs text-muted-foreground mb-4">Captured from the visitor request</p>
                {report.devices.length === 0 ? (
                  <EmptyPanel
                    text={hasDetailedEvents ? "No device classification in this selection" : "No detailed events captured yet"}
                    compact
                  />
                ) : (
                  <div className="h-[230px]">
                    <ResponsiveContainer width="100%" height="100%">
                      <PieChart>
                        <Pie data={report.devices} dataKey="clicks" nameKey="name" innerRadius={52} outerRadius={80} paddingAngle={2}>
                          {report.devices.map((_, i) => <Cell key={i} fill={COLORS[i % COLORS.length]} />)}
                        </Pie>
                        <Tooltip contentStyle={tooltipStyle} />
                        <Legend iconType="circle" iconSize={7} wrapperStyle={{ fontSize: 11 }} />
                      </PieChart>
                    </ResponsiveContainer>
                  </div>
                )}
              </Card>
              <Card>
                <p className="font-semibold mb-1">Browsers</p>
                <p className="text-xs text-muted-foreground mb-4">
                  Known browsers, Other, and anonymous Privacy-protected traffic
                </p>
                {report.browsers.length === 0 ? (
                  <EmptyPanel
                    text={hasDetailedEvents ? "No browser classification in this selection" : "New clicks will populate browser data"}
                    compact
                  />
                ) : (
                  <BreakdownList data={report.browsers} />
                )}
              </Card>
              <Card>
                <p className="font-semibold mb-1 flex items-center gap-2">
                  <Clock3 className="w-4 h-4 text-primary" />Peak hours
                </p>
                <p className="text-xs text-muted-foreground mb-4">
                  {peakHour
                    ? `UTC · peak at ${String(peakHour.hour).padStart(2, "0")}:00`
                    : "UTC · no click data in range"}
                </p>
                {maxHourClicks === 0 ? (
                  <EmptyPanel text="No hourly data" compact />
                ) : (
                  <PeakHoursChart data={report.hours} peakHour={peakHour?.hour} />
                )}
              </Card>
            </div>

            {/* ── Top links + Referrer traffic ── */}
            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
              <Card>
                <p className="font-semibold mb-4 flex items-center gap-2">
                  <Link2 className="w-4 h-4 text-primary" />Top links
                </p>
                {report.top_links.length === 0 ? (
                  <EmptyPanel text="No clicks in this range" compact />
                ) : (
                  <div className="space-y-3">
                    {report.top_links.map((item, index) => (
                      <div key={item.id} className="grid grid-cols-[24px_minmax(0,1fr)_auto] gap-3 items-center">
                        <span className="text-xs text-muted-foreground">{index + 1}</span>
                        <div className="min-w-0">
                          <p className="font-mono text-xs text-primary truncate">
                            {item.redirect_type === "subdomain"
                              ? `${item.short_code}.${item.domain}`
                              : `${item.domain}/${item.short_code}`}
                          </p>
                          <p className="text-xs text-muted-foreground truncate">{item.destination_url}</p>
                        </div>
                        <div className="text-right">
                          <p className="text-sm font-semibold tabular-nums">{formatNumber(item.clicks)}</p>
                          <p className="text-[10px] text-muted-foreground">{formatNumber(item.unique)} unique</p>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </Card>

              {/* Referrer panel — fixed layout with dynamic Y-axis width */}
              <Card>
                <p className="font-semibold mb-1">Referrer traffic</p>
                <p className="text-xs text-muted-foreground mb-4">
                  Top traffic sources · Brave and privacy browsers may strip referrer headers (shown as Direct)
                </p>
                {report.referrers.length === 0 ? (
                  <EmptyPanel text="No referrer data in this range" compact />
                ) : (
                  <ReferrerChart data={report.referrers} />
                )}
              </Card>
            </div>

            {/* ── Status cards ── */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <StatusCard
                icon={ShieldCheck}
                label="Capture health"
                value={report.capture.healthy ? "Healthy" : "Needs attention"}
                hint={report.capture.last_success_at
                  ? `Last success ${formatTimestamp(report.capture.last_success_at)}`
                  : "Waiting for the first detailed event"}
              />
              <StatusCard
                icon={DatabaseZap}
                label="Event schema"
                value={`Version ${report.schema_version}`}
                hint="No raw IP or full user-agent stored"
              />
              <StatusCard
                icon={Globe2}
                label="Country source"
                value={overview?.geo_fallback_configured ? "Cloudflare + IPinfo" : "Cloudflare"}
                hint={`${overview?.geo_queue_depth ?? 0} fallback jobs · ${formatNumber(overview?.geo_cache_entries ?? 0)} cached`}
              />
            </div>

            {/* ── Footer note ── */}
            <div className="rounded-xl border border-border bg-muted/30 px-4 py-3 text-xs text-muted-foreground flex items-start gap-2">
              <Info className="w-4 h-4 shrink-0 mt-0.5" />
              <span>
                <strong className="text-foreground">How counts work:</strong> complete clicks come from
        transactional hourly rollups. Unknown browsers are grouped as "Other". Requests carrying
        Global Privacy Control or Do Not Track create only an anonymous "Privacy-protected" event:
        no visitor hash, client IP, country, real referrer, device, browser, or operating-system
        classification is retained. Those events count toward click and browser totals but are
        excluded from unique visitors, repeat rate, and geo coverage. Verified Cloudflare
        countries are stored immediately for eligible traffic; IPinfo runs asynchronously only
        as a fallback. Raw IP addresses are never saved.
              </span>
            </div>
          </>
        )}
      </div>
    </AppShell>
  );
}

// ── Sub-components ────────────────────────────────────────────────────────────

const tooltipStyle = {
  background: "var(--color-card)",
  border: "1px solid var(--color-border)",
  borderRadius: "10px",
  fontSize: "12px",
};

function PeakHoursChart({ data, peakHour }: { data: { hour: number; clicks: number }[]; peakHour?: number }) {
  return (
    <div className="h-44 min-w-0">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 8, right: 4, left: -24, bottom: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
          <XAxis
            dataKey="hour"
            interval={0}
            tick={{ fontSize: 9 }}
            stroke="var(--color-border)"
            tickFormatter={(h: number) => h % 3 === 0 ? String(h).padStart(2, "0") : ""}
          />
          <YAxis allowDecimals={false} tick={{ fontSize: 9 }} stroke="var(--color-border)" />
          <Tooltip
            contentStyle={tooltipStyle}
            labelFormatter={(h) => `${String(h).padStart(2, "0")}:00–${String((Number(h) + 1) % 24).padStart(2, "0")}:00 UTC`}
            formatter={(v) => [Number(v).toLocaleString(), "Clicks"]}
          />
          <Bar dataKey="clicks" radius={[3, 3, 0, 0]} maxBarSize={24}>
            {data.map((item) => (
              <Cell
                key={item.hour}
                fill={item.hour === peakHour ? "var(--color-chart-3)" : "var(--color-primary)"}
                fillOpacity={item.hour === peakHour ? 1 : 0.78}
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

// ReferrerChart: dynamic Y-axis width so long domain names aren't clipped.
function ReferrerChart({ data }: { data: { source: string; clicks: number; unique: number }[] }) {
  const top10 = data.slice(0, 10);
  // Compute the longest label to size the Y axis.
  const maxLabelLen = Math.max(...top10.map((d) => shortLabel(d.source).length));
  const yWidth = Math.min(Math.max(maxLabelLen * 6.5 + 4, 60), 140);
  return (
    <div className="h-[280px]">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={top10} layout="vertical" margin={{ left: 4, right: 8, top: 4, bottom: 4 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" horizontal={false} />
          <XAxis type="number" allowDecimals={false} tick={{ fontSize: 10 }} stroke="var(--color-border)" />
          <YAxis
            dataKey="source"
            type="category"
            tick={{ fontSize: 10 }}
            width={yWidth}
            stroke="var(--color-border)"
            tickFormatter={shortLabel}
          />
          <Tooltip
            contentStyle={tooltipStyle}
            formatter={(value, _name, props) => [
              `${Number(value).toLocaleString()} clicks (${props.payload?.unique ?? 0} unique)`,
            ]}
            labelFormatter={(label) => shortLabel(String(label))}
          />
          <Bar dataKey="clicks" fill="var(--color-primary)" radius={[0, 5, 5, 0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function Notice({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-yellow-500/30 bg-yellow-500/10 px-4 py-3 text-sm flex items-start gap-2">
      <Info className="w-4 h-4 mt-0.5 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

function MetricCard({ icon: Icon, label, value, hint }: {
  icon: React.ElementType; label: string; value: string; hint: string;
}) {
  return (
    <Card className="p-3 sm:p-4 min-w-0">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[10px] sm:text-xs uppercase tracking-wide text-muted-foreground truncate">{label}</p>
          <p className="text-xl sm:text-2xl font-bold mt-1 tabular-nums truncate">{value}</p>
          <p className="text-[10px] sm:text-xs text-muted-foreground mt-1 truncate">{hint}</p>
        </div>
        <div className="w-8 h-8 rounded-lg bg-primary/10 text-primary flex items-center justify-center shrink-0">
          <Icon className="w-4 h-4" />
        </div>
      </div>
    </Card>
  );
}

function StatusCard({ icon: Icon, label, value, hint }: {
  icon: React.ElementType; label: string; value: string; hint: string;
}) {
  return (
    <Card className="p-4">
      <div className="flex gap-3">
        <div className="w-9 h-9 rounded-lg bg-primary/10 text-primary flex items-center justify-center shrink-0">
          <Icon className="w-4 h-4" />
        </div>
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">{label}</p>
          <p className="font-semibold">{value}</p>
          <p className="text-[11px] text-muted-foreground truncate">{hint}</p>
        </div>
      </div>
    </Card>
  );
}

function BreakdownList({ data }: { data: GroupedStat[] }) {
  return (
    <div className="space-y-3">
      {data.slice(0, 8).map((item, index) => (
        <div key={item.name} className="space-y-1">
          <div className="flex items-center gap-2">
            <span className="text-sm flex-1 truncate">{item.name}</span>
            <span className="text-xs font-medium tabular-nums">{formatNumber(item.clicks)}</span>
            <span className="text-[10px] text-muted-foreground w-10 text-right">{item.value}%</span>
          </div>
          <div className="h-1.5 rounded-full bg-muted overflow-hidden">
            <div
              className="h-full rounded-full"
              style={{ width: `${Math.max(item.value, 1)}%`, background: COLORS[index % COLORS.length] }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

function EmptyPanel({ text, compact = false }: { text: string; compact?: boolean }) {
  return (
    <div className={`flex items-center justify-center text-center text-sm text-muted-foreground px-4 ${compact ? "py-10" : "py-20"}`}>
      <div>
        <BarChart3 className="w-6 h-6 mx-auto mb-2 opacity-50" />
        <p>{text}</p>
      </div>
    </div>
  );
}
