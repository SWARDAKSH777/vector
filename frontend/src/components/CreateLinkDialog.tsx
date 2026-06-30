import React, { useState, useEffect, useRef, useCallback } from "react";
import { Plus, ChevronDown, Check, X, Loader2, Globe, Hash } from "lucide-react";
import { api, type Link, type Domain } from "../lib/api";
import { Modal, Button, Input, Textarea } from "./ui";
import { cn } from "../lib/utils";

const SESSION_ID = Math.random().toString(36).slice(2);

interface Props {
  open: boolean;
  onClose: () => void;
  onCreated: (link: Link) => void;
  domains: Domain[];
}

type ExpiryPreset = "1h" | "6h" | "24h" | "7d" | "30d" | "custom" | "";

export function CreateLinkDialog({ open, onClose, onCreated, domains }: Props) {
  const [dest, setDest] = useState("");
  const [alias, setAlias] = useState("");
  const [domain, setDomain] = useState("");
  const [redirectType, setRedirectType] = useState<"slug" | "subdomain">("slug");
  const [tag, setTag] = useState("");
  const [notes, setNotes] = useState("");
  const [password, setPassword] = useState("");
  const [expiryPreset, setExpiryPreset] = useState<ExpiryPreset>("");
  const [customExpiry, setCustomExpiry] = useState("");
  const [maxClicks, setMaxClicks] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [showUTM, setShowUTM] = useState(false);
  const [utmSource, setUtmSource] = useState("");
  const [utmMedium, setUtmMedium] = useState("");
  const [utmCampaign, setUtmCampaign] = useState("");

  const [aliasStatus, setAliasStatus] = useState<{ status: string; message?: string } | null>(null);
  const [aliasChecking, setAliasChecking] = useState(false);
  const [blocklist, setBlocklist] = useState<string[]>([]);
  const [blocklistError, setBlocklistError] = useState("");
  const aliasTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<Link | null>(null);

  const activeDomains = domains.filter((d) => d.status === "active");
  const defaultDomain = activeDomains.find((d) => d.is_default) ?? activeDomains[0];
  const dnsReadyDomains = activeDomains.filter((d) => d.dns_ready);
  const selectedHostname = domain || defaultDomain?.hostname || "";
  const selectedDomain = activeDomains.find((d) => d.hostname === selectedHostname);

  // Keep the selected domain valid as the mode changes. In subdomain mode,
  // automatically fall back to the first domain with a live CF configuration.
  useEffect(() => {
    if (!open) return;
    if (redirectType === "subdomain") {
      if (selectedDomain?.dns_ready) return;
      const first = dnsReadyDomains[0];
      setDomain(first ? (first.is_default ? "" : first.hostname) : "");
      return;
    }
    if (domain && !activeDomains.some((d) => d.hostname === domain)) setDomain("");
  }, [open, redirectType, domains, domain, selectedDomain?.dns_ready]);

  // Load blocklist when domain changes (for subdomain conflict checking).
  useEffect(() => {
    const controller = new AbortController();
    setBlocklistError("");
    if (redirectType !== "subdomain" || !selectedHostname) { setBlocklist([]); return () => controller.abort(); }
    const dom = dnsReadyDomains.find((d) => d.hostname === selectedHostname);
    if (!dom) { setBlocklist([]); return () => controller.abort(); }
    api.getDomainBlocklist(dom.id, controller.signal)
      .then((items) => { setBlocklist(items); setBlocklistError(""); })
      .catch((err) => {
        if (err?.name === "AbortError") return;
        setBlocklist([]);
        setBlocklistError(err?.message || "Could not load existing DNS records");
      });
    return () => controller.abort();
  }, [selectedHostname, redirectType, domains]);

  const checkAlias = useCallback(async (val: string) => {
    if (!val) { setAliasStatus(null); return; }

    // For subdomain mode: check against CF blocklist immediately
    if (redirectType === "subdomain" && selectedHostname) {
      const fullHostname = `${val}.${selectedHostname}`;
      const conflict = blocklist.find((b) => b.toLowerCase() === fullHostname.toLowerCase());
      if (conflict) {
        setAliasStatus({ status: "taken", message: `CNAME already used by ${conflict}` });
        setAliasChecking(false);
        return;
      }
    }

    setAliasChecking(true);
    try {
      const result = await api.checkAlias(val, SESSION_ID, selectedHostname, redirectType);
      setAliasStatus(result);
    } catch (err: any) {
      if (err?.name !== "AbortError") {
        setAliasStatus({ status: "invalid", message: err?.message || "Could not check alias availability" });
      }
    }
    finally { setAliasChecking(false); }
  }, [redirectType, selectedHostname, blocklist]);

  useEffect(() => {
    clearTimeout(aliasTimer.current);
    if (!alias) { setAliasStatus(null); setAliasChecking(false); return; }
    setAliasChecking(true);
    aliasTimer.current = setTimeout(() => checkAlias(alias), 400);
    return () => clearTimeout(aliasTimer.current);
  }, [alias, checkAlias]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");

    if (redirectType === "subdomain" && !selectedDomain?.dns_ready) {
      setError("Choose a verified domain with a valid Cloudflare API token");
      return;
    }

    setLoading(true);
    try {
      const link = await api.createLink({
        destination_url: dest,
        custom_alias: alias || undefined,
        domain: domain || undefined,
        redirect_type: redirectType,
        tag: tag || undefined,
        notes: notes || undefined,
        password: password || undefined,
        expires_in: expiryPreset && expiryPreset !== "custom" ? expiryPreset : undefined,
        expires_at: expiryPreset === "custom" ? customExpiry : undefined,
        max_clicks: maxClicks ? parseInt(maxClicks) : undefined,
        utm_source: utmSource || undefined,
        utm_medium: utmMedium || undefined,
        utm_campaign: utmCampaign || undefined,
      });
      setCreated(link);
      onCreated(link);
    } catch (err: any) { setError(err.message); }
    finally { setLoading(false); }
  }

  function reset() {
    setDest(""); setAlias(""); setDomain(""); setRedirectType("slug");
    setTag(""); setNotes(""); setPassword(""); setExpiryPreset(""); setCustomExpiry("");
    setMaxClicks(""); setShowAdvanced(false); setShowUTM(false);
    setUtmSource(""); setUtmMedium(""); setUtmCampaign("");
    setAliasStatus(null); setError(""); setCreated(null);
  }

  function handleClose() { reset(); onClose(); }

  const aliasIndicator = () => {
    if (aliasChecking) return <Loader2 className="w-4 h-4 animate-spin text-muted-foreground" />;
    if (!aliasStatus || !alias) return null;
    if (aliasStatus.status === "available") return <Check className="w-4 h-4 text-success" />;
    return <X className="w-4 h-4 text-destructive" />;
  };

  const aliasError = () => {
    if (!aliasStatus || aliasStatus.status === "available" || !alias) return undefined;
    return aliasStatus.message ?? "Not available";
  };

  return (
    <Modal open={open} onClose={handleClose} title="Create Short Link" maxWidth="sm:max-w-xl">
      {created ? (
        <div className="flex flex-col gap-4">
          <div className="flex items-center gap-2 rounded-lg bg-success/10 px-4 py-3 border border-success/20">
            <Check className="w-4 h-4 text-success shrink-0" />
            <span className="text-sm font-medium text-success">Link created!</span>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-muted-foreground font-medium">Short URL</label>
            <div className="flex items-center gap-2">
              <code className="flex-1 font-mono text-sm bg-muted rounded-lg px-3 py-2 truncate">{created.short_url}</code>
              <Button variant="secondary" size="sm" onClick={async () => {
                try { await navigator.clipboard.writeText(created.short_url); }
                catch { setError("Could not copy the URL. Select and copy it manually."); }
              }}>Copy</Button>
            </div>
          </div>
          <p className="text-sm text-muted-foreground truncate">→ {created.destination_url}</p>
          <div className="flex gap-2">
            <Button className="flex-1" variant="secondary" onClick={handleClose}>Close</Button>
            <Button className="flex-1" onClick={() => { setCreated(null); setDest(""); setAlias(""); }}>
              <Plus className="w-3.5 h-3.5" /> Create another
            </Button>
          </div>
        </div>
      ) : (
        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <Input
            label="Destination URL *"
            type="text"
            placeholder="https://example.com/your-long-url"
            value={dest}
            onChange={(e) => setDest(e.target.value)}
            required
            autoFocus
          />

          {/* Redirect type toggle */}
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setRedirectType("slug")}
              className={cn(
                "flex-1 flex items-center justify-center gap-2 py-2 px-3 rounded-lg border text-sm font-medium transition-all",
                redirectType === "slug"
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-border text-muted-foreground hover:text-foreground hover:bg-accent"
              )}
            >
              <Hash className="w-3.5 h-3.5" /> Slug
            </button>
            <button
              type="button"
              onClick={() => setRedirectType("subdomain")}
              disabled={dnsReadyDomains.length === 0}
              className={cn(
                "flex-1 flex items-center justify-center gap-2 py-2 px-3 rounded-lg border text-sm font-medium transition-all disabled:opacity-40 disabled:cursor-not-allowed",
                redirectType === "subdomain"
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-border text-muted-foreground hover:text-foreground hover:bg-accent"
              )}
            >
              <Globe className="w-3.5 h-3.5" /> Subdomain
            </button>
          </div>

          {redirectType === "subdomain" && dnsReadyDomains.length === 0 && (
            <p className="text-xs text-muted-foreground bg-muted px-3 py-2 rounded-lg">
              Verify a domain and save a working Cloudflare token before using subdomain redirects.
            </p>
          )}
          {redirectType === "subdomain" && selectedHostname && (
            <>
              <p className="text-xs text-muted-foreground bg-primary/5 border border-primary/20 px-3 py-2 rounded-lg">
                Short URL will be <span className="font-mono font-medium text-foreground">{alias || "[auto-generated]"}.{selectedHostname}</span>
              </p>
              {blocklistError && (
                <p className="text-xs text-warning bg-warning/10 border border-warning/20 px-3 py-2 rounded-lg">
                  Existing Cloudflare DNS records could not be pre-checked: {blocklistError}. Vector will still verify the record on creation.
                </p>
              )}
            </>
          )}
          {redirectType === "slug" && selectedHostname && (
            <p className="text-xs text-muted-foreground bg-primary/5 border border-primary/20 px-3 py-2 rounded-lg">
              Slugs are case-sensitive and unique only on this domain: <span className="font-mono font-medium text-foreground">{selectedHostname}/{alias || "[auto-generated]"}</span>
            </p>
          )}

          {/* Alias + Domain row */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <Input
              label={redirectType === "subdomain" ? "Subdomain prefix (optional)" : "Custom slug (optional)"}
              placeholder={redirectType === "subdomain" ? "Auto-generate 7 characters" : "Auto-generate 7 characters"}
              value={alias}
              onChange={(e) => setAlias(
                redirectType === "subdomain"
                  ? e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "")
                  : e.target.value.replace(/[^a-zA-Z0-9-_]/g, "")
              )}
              error={aliasError()}
              hint={aliasStatus?.status === "available" && alias ? "✓ Available" : undefined}
              suffix={aliasIndicator()}
            />

            <DomainDropdown
              value={domain}
              onChange={setDomain}
              defaultDomain={defaultDomain}
              domains={redirectType === "subdomain" ? dnsReadyDomains : activeDomains}
              mode={redirectType}
            />
          </div>

          {/* Tag */}
          <Input
            label="Tag (optional)"
            placeholder="campaign, product…"
            value={tag}
            onChange={(e) => setTag(e.target.value)}
          />

          {/* Expiry presets */}
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-foreground">Link expires (optional)</label>
            <div className="flex flex-wrap gap-1.5">
              {(["", "1h", "6h", "24h", "7d", "30d", "custom"] as const).map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => setExpiryPreset(p)}
                  className={cn(
                    "px-2.5 py-1 text-xs rounded-lg border font-medium transition-all",
                    expiryPreset === p
                      ? "border-primary bg-primary/10 text-primary"
                      : "border-border text-muted-foreground hover:text-foreground hover:bg-accent"
                  )}
                >
                  {p === "" ? "Never" : p === "custom" ? "Custom date" : p}
                </button>
              ))}
            </div>
            {expiryPreset === "custom" && (
              <input
                type="date"
                value={customExpiry}
                onChange={(e) => setCustomExpiry(e.target.value)}
                className="mt-1 h-9 w-full rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              />
            )}
          </div>

          {/* Advanced options */}
          <button
            type="button"
            onClick={() => setShowAdvanced((v) => !v)}
            className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            <ChevronDown className={cn("w-3.5 h-3.5 transition-transform", showAdvanced && "rotate-180")} />
            Advanced options {!showAdvanced && <span className="text-muted-foreground/60">(password, click limit, UTM, notes)</span>}
          </button>

          {showAdvanced && (
            <div className="flex flex-col gap-3 p-4 bg-muted/40 rounded-xl border border-border">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <Input
                  label="Password protection"
                  type="password"
                  placeholder="Protect with a password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <Input
                  label="Max clicks"
                  type="number"
                  min="1"
                  placeholder="e.g. 100"
                  value={maxClicks}
                  onChange={(e) => setMaxClicks(e.target.value)}
                />
              </div>
              <Textarea
                label="Notes (internal)"
                placeholder="Private notes about this link…"
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
              />
              <button
                type="button"
                onClick={() => setShowUTM((v) => !v)}
                className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                <ChevronDown className={cn("w-3.5 h-3.5 transition-transform", showUTM && "rotate-180")} />
                UTM Parameters
              </button>
              {showUTM && (
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                  <Input label="utm_source" placeholder="google" value={utmSource} onChange={(e) => setUtmSource(e.target.value)} />
                  <Input label="utm_medium" placeholder="cpc" value={utmMedium} onChange={(e) => setUtmMedium(e.target.value)} />
                  <Input label="utm_campaign" placeholder="launch" value={utmCampaign} onChange={(e) => setUtmCampaign(e.target.value)} />
                </div>
              )}
            </div>
          )}

          {error && (
            <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{error}</p>
          )}

          <div className="flex gap-2 pt-1">
            <Button type="button" variant="secondary" className="flex-1" onClick={handleClose}>Cancel</Button>
            <Button type="submit" className="flex-1" loading={loading}>
              <Plus className="w-3.5 h-3.5" /> Create Link
            </Button>
          </div>
        </form>
      )}
    </Modal>
  );
}

type DomainDropdownProps = {
  value: string;
  onChange: (value: string) => void;
  defaultDomain?: Domain;
  domains: Domain[];
  mode: "slug" | "subdomain";
};

function DomainDropdown({ value, onChange, defaultDomain, domains, mode }: DomainDropdownProps) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const eligibleDefault = defaultDomain && domains.some((d) => d.id === defaultDomain.id) ? defaultDomain : undefined;
  const selected = value ? domains.find((d) => d.hostname === value) : eligibleDefault;
  const options = domains.filter((d) => !eligibleDefault || d.id !== eligibleDefault.id);

  useEffect(() => {
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);

  return (
    <div ref={rootRef} className="relative flex flex-col gap-1.5">
      <label className="text-xs font-medium text-foreground">Domain</label>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className={cn(
          "h-9 w-full rounded-lg border border-input bg-background px-3 text-left",
          "flex items-center justify-between gap-2 text-sm transition-all",
          "hover:bg-accent/50 focus:outline-none focus:ring-2 focus:ring-ring"
        )}
      >
        <span className="min-w-0 flex items-center gap-2">
          <Globe className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
          <span className="truncate font-mono">{selected?.hostname ?? "No eligible domain"}</span>
          {!value && eligibleDefault && (
            <span className="rounded-full bg-primary/10 px-1.5 py-0.5 text-[10px] font-semibold text-primary">DEFAULT</span>
          )}
        </span>
        <ChevronDown className={cn("w-3.5 h-3.5 text-muted-foreground transition-transform", open && "rotate-180")} />
      </button>

      {open && (
        <div
          role="listbox"
          className="absolute top-full z-30 mt-1.5 w-full overflow-hidden rounded-xl border border-border bg-popover shadow-xl animate-in fade-in-0 zoom-in-95"
        >
          <div className="p-1.5 max-h-56 overflow-y-auto">
            {eligibleDefault && (
              <button
                type="button"
                role="option"
                aria-selected={value === ""}
                onClick={() => { onChange(""); setOpen(false); }}
                className={cn(
                  "w-full rounded-lg px-3 py-2 text-left flex items-center justify-between gap-3 transition-colors",
                  value === "" ? "bg-primary/10 text-primary" : "hover:bg-accent text-foreground"
                )}
              >
                <span className="min-w-0">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    Default
                    <span className="rounded-full border border-primary/20 px-1.5 py-0.5 text-[10px] font-semibold">PRIMARY</span>
                  </span>
                  <span className="block truncate font-mono text-xs text-muted-foreground mt-0.5">{eligibleDefault.hostname}</span>
                </span>
                {value === "" && <Check className="w-4 h-4 shrink-0" />}
              </button>
            )}

            {options.map((d) => (
              <button
                key={d.id}
                type="button"
                role="option"
                aria-selected={value === d.hostname}
                onClick={() => { onChange(d.hostname); setOpen(false); }}
                className={cn(
                  "w-full rounded-lg px-3 py-2 text-left flex items-center justify-between gap-3 transition-colors",
                  value === d.hostname ? "bg-primary/10 text-primary" : "hover:bg-accent text-foreground"
                )}
              >
                <span className="min-w-0">
                  <span className="block truncate font-mono text-sm font-medium">{d.hostname}</span>
                  <span className="block text-[11px] text-muted-foreground mt-0.5">
                    {mode === "subdomain" ? "Cloudflare DNS ready" : "Verified domain"}
                  </span>
                </span>
                {value === d.hostname && <Check className="w-4 h-4 shrink-0" />}
              </button>
            ))}

            {!eligibleDefault && options.length === 0 && (
              <p className="px-3 py-3 text-xs text-muted-foreground">No eligible domains available.</p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

