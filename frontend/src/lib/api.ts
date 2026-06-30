const BASE = "";

export class APIError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

type CacheEntry = { expiresAt: number; value: unknown };
const responseCache = new Map<string, CacheEntry>();
const inFlight = new Map<string, Promise<unknown>>();
const cacheGeneration = new Map<string, number>();
let globalCacheGeneration = 0;

function bumpCacheGeneration(key: string) {
  cacheGeneration.set(key, (cacheGeneration.get(key) || 0) + 1);
}

export function clearAPICache(prefixes?: string[]) {
  if (!prefixes || prefixes.length === 0) {
    globalCacheGeneration++;
    responseCache.clear();
    inFlight.clear();
    cacheGeneration.clear();
    return;
  }
  const keys = new Set([...responseCache.keys(), ...inFlight.keys(), ...cacheGeneration.keys()]);
  for (const key of keys) {
    if (!prefixes.some((prefix) => key.startsWith(prefix))) continue;
    bumpCacheGeneration(key);
    responseCache.delete(key);
    inFlight.delete(key);
  }
}

async function csrfToken(): Promise<string> {
  const res = await fetch(BASE + "/api/security/csrf", { method: "GET", credentials: "include", headers: { Accept: "application/json" } });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new APIError(res.status, body.error || `Could not establish a secure request token (HTTP ${res.status})`);
  }
  return (await res.json()).csrf_token;
}

async function req<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const unsafe = !["GET", "HEAD", "OPTIONS"].includes(method);
  const establishesSession = path === "/api/auth/login" || path === "/api/setup/bootstrap/login";
  const readOnlySetupCheck = path === "/api/setup/check-domain";
  if (unsafe && !establishesSession && !readOnlySetupCheck) headers["X-CSRF-Token"] = await csrfToken();
  const res = await fetch(BASE + path, { method, credentials: "include", headers, body: body !== undefined ? JSON.stringify(body) : undefined, signal });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: "Network error" }));
    throw new APIError(res.status, err.error || `HTTP ${res.status}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

async function cachedGet<T>(path: string, ttlMs: number, signal?: AbortSignal, force = false): Promise<T> {
  const now = Date.now();
  if (!force) {
    const cached = responseCache.get(path);
    if (cached && cached.expiresAt > now) return cached.value as T;
  }
  if (!signal && !force) {
    const pending = inFlight.get(path);
    if (pending) return pending as Promise<T>;
  }
  const requestGlobalGeneration = globalCacheGeneration;
  const requestKeyGeneration = cacheGeneration.get(path) || 0;
  let request: Promise<T>;
  request = req<T>("GET", path, undefined, signal).then((value) => {
    if (requestGlobalGeneration === globalCacheGeneration && requestKeyGeneration === (cacheGeneration.get(path) || 0)) {
      responseCache.set(path, { expiresAt: Date.now() + ttlMs, value });
    }
    return value;
  }).finally(() => {
    if (inFlight.get(path) === request) inFlight.delete(path);
  });
  if (!signal && !force) inFlight.set(path, request);
  return request;
}

async function mutate<T>(method: string, path: string, body: unknown, invalidate: string[]): Promise<T> {
  const result = await req<T>(method, path, body);
  clearAPICache(invalidate);
  return result;
}

function analyticsQuery(filters?: AnalyticsFilters): string {
  if (!filters) return "";
  const params = new URLSearchParams();
  if (filters.range) params.set("range", filters.range);
  if (filters.link_id) params.set("link_id", String(filters.link_id));
  if (filters.country) params.set("country", filters.country);
  if (filters.device) params.set("device", filters.device);
  if (filters.browser) params.set("browser", filters.browser);
  if (filters.referrer) params.set("referrer", filters.referrer);
  const query = params.toString();
  return query ? `?${query}` : "";
}

export const api = {
  setupStatus: () => req<{ setup_complete: boolean; domain: string; bootstrap_required: boolean; bootstrap_authenticated: boolean; bootstrap_available: boolean; bootstrap_message?: string }>("GET", "/api/setup/status"),
  bootstrapLogin: async (username: string, password: string) => { const result = await req<{ ok: boolean; expires_in: number }>("POST", "/api/setup/bootstrap/login", { username, password }); clearAPICache(["/api/setup/status"]); return result; },
  bootstrapLogout: async () => { const result = await req<{ ok: boolean }>("POST", "/api/setup/bootstrap/logout"); clearAPICache(["/api/setup/status"]); return result; },
  setupCheckDomain: (domain: string, cloudflareToken?: string, signal?: AbortSignal) => req<{ ok: boolean; error?: string; method?: "cloudflare_api" | "cloudflare_api_pending_dns" | "http_reachability" }>("POST", "/api/setup/check-domain", { domain, cloudflare_token: cloudflareToken || undefined }, signal),
  setupSubmit: async (data: { domain: string; admin_email: string; admin_password: string; cloudflare_token?: string }) => { const result = await req<{ ok: boolean; domain: string }>("POST", "/api/setup", data); clearAPICache(["/api/setup/status"]); return result; },
  setupNginx: async (domain: string) => { const result = await req<{ ok: boolean; log: string; error?: string; url?: string }>("POST", "/api/setup/nginx", { domain }); clearAPICache(["/api/setup/status", "/api/domains"]); return result; },

  login: async (email: string, password: string) => { const result = await req<{ ok: boolean }>("POST", "/api/auth/login", { email, password }); clearAPICache(); return result; },
  logout: async () => { const result = await req<{ ok: boolean }>("POST", "/api/auth/logout"); clearAPICache(); return result; },
  me: () => cachedGet<{ id: number; email: string }>("/api/auth/me", 60_000),
  updatePassword: (currentPassword: string, newPassword: string) => req<{ ok: boolean }>("POST", "/api/auth/update-password", { current_password: currentPassword, new_password: newPassword }),

  getPrivacySettings: () => cachedGet<PrivacySettings>("/api/settings/privacy", 15_000),
  updatePrivacySettings: (data: Partial<Pick<PrivacySettings, "analytics_enabled" | "analytics_retention_days" | "audit_retention_days">>) => mutate<PrivacySettings>("PUT", "/api/settings/privacy", data, ["/api/settings/privacy", "/api/analytics/", "/api/stats/"]),
  getIPInfoTokenStatus: () => cachedGet<IPInfoTokenStatus>("/api/settings/ipinfo-token", 15_000),
  saveIPInfoToken: (token: string) => mutate<IPInfoTokenStatus>("PUT", "/api/settings/ipinfo-token", { token }, ["/api/settings/ipinfo-token", "/api/analytics/", "/api/stats/"]),
  deleteIPInfoToken: () => mutate<IPInfoTokenStatus>("DELETE", "/api/settings/ipinfo-token", undefined, ["/api/settings/ipinfo-token", "/api/analytics/", "/api/stats/"]),
  deleteAnalytics: () => mutate<{ ok: boolean; deleted: number; counters_reset: number }>("DELETE", "/api/settings/analytics", undefined, ["/api/analytics/", "/api/stats/", "/api/links"]),
  dataExportURL: "/api/settings/export",

  listLinks: (q?: string, status?: string, limit = 200, offset = 0) => {
    const p = new URLSearchParams(); if (q) p.set("q", q); if (status) p.set("status", status);
    p.set("limit", String(limit)); p.set("offset", String(offset));
    const qs = p.toString(); return cachedGet<Link[]>(`/api/links${qs ? "?" + qs : ""}`, 8_000);
  },
  createLink: (data: CreateLinkInput) => mutate<Link>("POST", "/api/links", data, ["/api/links", "/api/analytics/"]),
  getLink: (id: number) => req<Link>("GET", `/api/links/${id}`),
  updateLink: (id: number, data: UpdateLinkInput) => mutate<Link>("PUT", `/api/links/${id}`, data, ["/api/links", "/api/analytics/"]),
  deleteLink: (id: number) => mutate<{ ok: boolean }>("DELETE", `/api/links/${id}`, undefined, ["/api/links", "/api/analytics/"]),
  qrCodeURL: (id: number) => `/api/links/${id}/qrcode.png`,
  checkAlias: (alias: string, sid: string, domain: string, redirectType: "slug" | "subdomain") => {
    const params = new URLSearchParams({ alias, sid, redirect_type: redirectType }); if (domain) params.set("domain", domain);
    return req<AliasCheckResult>("GET", `/api/links/check-alias?${params.toString()}`);
  },

  analyticsReport: (filters?: AnalyticsFilters, signal?: AbortSignal, force = false) => {
    const query = analyticsQuery(filters);
    const path = `/api/analytics/report${query}${force ? (query ? "&" : "?") + "refresh=1" : ""}`;
    return cachedGet<AnalyticsReport>(path, 3_000, signal, force);
  },
  statsOverview: (filters?: AnalyticsFilters) => req<StatsOverview>("GET", `/api/stats/overview${analyticsQuery(filters)}`),
  statsTimeseries: (filters?: AnalyticsFilters | string) => req<TimeseriesPoint[]>("GET", `/api/stats/timeseries${analyticsQuery(typeof filters === "string" ? { range: filters as AnalyticsFilters["range"] } : filters)}`),
  statsGeo: (filters?: AnalyticsFilters) => req<GeoResponse>("GET", `/api/stats/geo${analyticsQuery(filters)}`),
  statsDevices: (filters?: AnalyticsFilters) => req<GroupedStat[]>("GET", `/api/stats/devices${analyticsQuery(filters)}`),
  statsBrowsers: (filters?: AnalyticsFilters) => req<GroupedStat[]>("GET", `/api/stats/browsers${analyticsQuery(filters)}`),
  statsReferrers: (filters?: AnalyticsFilters) => req<ReferrerStat[]>("GET", `/api/stats/referrers${analyticsQuery(filters)}`),
  statsTopLinks: (filters?: AnalyticsFilters) => req<TopLinkStat[]>("GET", `/api/stats/top-links${analyticsQuery(filters)}`),
  statsHours: (filters?: AnalyticsFilters) => req<HourlyStat[]>("GET", `/api/stats/hours${analyticsQuery(filters)}`),
  statsOptions: () => cachedGet<AnalyticsOptions>("/api/stats/options", 15_000),

  listDomains: () => cachedGet<Domain[]>("/api/domains", 30_000),
  addDomain: (hostname: string) => mutate<Domain>("POST", "/api/domains", { hostname }, ["/api/domains"]),
  verifyDomain: (id: number) => mutate<Domain>("POST", `/api/domains/${id}/verify`, undefined, ["/api/domains"]),
  setDefaultDomain: (id: number) => mutate<Domain>("POST", `/api/domains/${id}/default`, undefined, ["/api/domains"]),
  deleteDomain: (id: number) => mutate<{ ok: boolean }>("DELETE", `/api/domains/${id}`, undefined, ["/api/domains"]),
  saveDomainToken: (id: number, token: string) => mutate<Domain>("POST", `/api/domains/${id}/token`, { token }, ["/api/domains"]),
  deleteDomainToken: (id: number) => mutate<Domain>("DELETE", `/api/domains/${id}/token`, undefined, ["/api/domains"]),
  getDomainBlocklist: (id: number, signal?: AbortSignal) => req<string[]>("GET", `/api/domains/${id}/blocklist`, undefined, signal),
};

export interface Link { id: number; short_code: string; short_url: string; destination_url: string; domain: string; redirect_type: "slug" | "subdomain"; tag: string; notes: string; has_password: boolean; expires_at: string | null; max_clicks: number | null; utm_source: string; utm_medium: string; utm_campaign: string; status: "active" | "paused" | "expired"; click_count: number; created_at: string; }
export interface CreateLinkInput { destination_url: string; custom_alias?: string; domain?: string; redirect_type?: "slug" | "subdomain"; tag?: string; notes?: string; password?: string; expires_at?: string; expires_in?: string; max_clicks?: number; utm_source?: string; utm_medium?: string; utm_campaign?: string; }
export interface UpdateLinkInput { destination_url?: string; tag?: string; notes?: string; status?: "active" | "paused"; password?: string; clear_password?: boolean; expires_at?: string; expires_in?: string; clear_expiry?: boolean; max_clicks?: number; clear_max_clicks?: boolean; }
export interface AliasCheckResult { status: "available" | "taken" | "reserved" | "invalid" | "empty"; message?: string; }
export interface AnalyticsFilters { range?: "7d" | "30d" | "90d" | "1y" | "all"; link_id?: number; country?: string; device?: string; browser?: string; referrer?: string; }
export interface StatsOverview { total_clicks: number; detailed_clicks: number; identified_clicks: number; privacy_protected_clicks: number; all_time_clicks: number; total_clicks_delta: number; delta_available: boolean; unique_visitors: number; active_links: number; avg_daily_clicks: number; repeat_click_rate: number; detailed_coverage: number; analytics_enabled: boolean; analytics_retention_days: number; geo_provider: "cloudflare_cf_ipcountry" | "cloudflare_cf_ipcountry+ipinfo_lite"; geo_provider_configured: boolean; geo_fallback_configured: boolean; geo_queue_depth: number; geo_cache_entries: number; range_days: number; range_start: string; range_end: string; data_mode: "aggregate" | "detailed"; all_time_ignores_dimensions: boolean; }
export interface TimeseriesPoint { date: string; day: string; clicks: number; unique: number; }
export interface GroupedStat { name: string; value: number; clicks: number; unique: number; }
export interface ReferrerStat { source: string; clicks: number; unique: number; }
export interface GeoCountryStat { code: string; clicks: number; unique: number; }
export interface GeoResponse { countries: GeoCountryStat[]; located_clicks: number; total_clicks: number; coverage: number; }
export interface TopLinkStat { id: number; short_code: string; domain: string; redirect_type: "slug" | "subdomain"; destination_url: string; clicks: number; unique: number; all_time_clicks: number; }
export interface HourlyStat { hour: number; clicks: number; }
export interface AnalyticsOptions { links: { id: number; short_code: string; domain: string }[]; countries: string[]; regions: { country_code: string; code: string; name: string }[]; devices: string[]; browsers: string[]; referrers: string[]; }
export interface AnalyticsCaptureHealth { failures: number; healthy: boolean; last_error?: string; last_error_at?: string; last_success_at?: string; }
export interface AnalyticsReport { generated_at: string; schema_version: number; granularity: "day" | "week" | "month"; filters: Required<Pick<AnalyticsFilters, "range">> & Omit<AnalyticsFilters, "range">; overview: StatsOverview; timeseries: TimeseriesPoint[]; geo: GeoResponse; devices: GroupedStat[]; browsers: GroupedStat[]; referrers: ReferrerStat[]; top_links: TopLinkStat[]; hours: HourlyStat[]; options: AnalyticsOptions; capture: AnalyticsCaptureHealth; }
export interface IPInfoTokenStatus { has_token: boolean; configured: boolean; provider: string; country_only: boolean; }
export interface PrivacySettings { analytics_enabled: boolean; analytics_retention_days: number; audit_retention_days: number; honors_gpc_and_dnt: boolean; stores_raw_ip: boolean; }
export interface Domain { id: number; hostname: string; status: "pending" | "active" | "error" | "token_missing"; message: string; has_token: boolean; dns_ready: boolean; proxied: boolean; is_default: boolean; created_at: string; }
