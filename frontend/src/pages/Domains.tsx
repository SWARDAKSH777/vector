import React, { useEffect, useState } from "react";
import { Plus, Globe, Trash2, RefreshCw, CheckCircle, XCircle, Clock, AlertTriangle, Key, KeyRound, Loader2, Star } from "lucide-react";
import { api, type Domain } from "../lib/api";
import { AppShell } from "../components/AppShell";
import { Button, Badge, Spinner, EmptyState, Modal, Input } from "../components/ui";
import { ConfirmDeleteModal } from "../components/ConfirmDeleteModal";
import { formatDate } from "../lib/utils";

export function DomainsPage() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState("");

  const [addOpen, setAddOpen] = useState(false);
  const [hostname, setHostname] = useState("");
  const [addError, setAddError] = useState("");
  const [addLoading, setAddLoading] = useState(false);

  const [verifyingId, setVerifyingId] = useState<number | null>(null);
  const [settingDefaultId, setSettingDefaultId] = useState<number | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<Domain | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const [tokenDomain, setTokenDomain] = useState<Domain | null>(null);
  const [token, setToken] = useState("");
  const [tokenLoading, setTokenLoading] = useState(false);
  const [tokenError, setTokenError] = useState("");

  const [deleteTokenTarget, setDeleteTokenTarget] = useState<Domain | null>(null);

  useEffect(() => {
    api.listDomains()
      .then(setDomains)
      .catch((reason: unknown) => setPageError(reason instanceof Error ? reason.message : "Could not load domains"))
      .finally(() => setLoading(false));
  }, []);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    setAddError(""); setAddLoading(true);
    try {
      const d = await api.addDomain(hostname.trim());
      setDomains((ds) => [d, ...ds]);
      setAddOpen(false); setHostname("");
    } catch (err: any) { setAddError(err.message); }
    finally { setAddLoading(false); }
  }

  async function handleVerify(id: number) {
    setVerifyingId(id); setPageError("");
    try {
      const updated = await api.verifyDomain(id);
      setDomains((items) => items.map((item) => item.id === id ? updated : item));
    } catch (reason: unknown) {
      setPageError(reason instanceof Error ? reason.message : "Could not verify domain");
    } finally {
      setVerifyingId(null);
    }
  }

  async function handleSetDefault(id: number) {
    setSettingDefaultId(id); setPageError("");
    try {
      const updated = await api.setDefaultDomain(id);
      setDomains((items) => items.map((item) => ({ ...item, is_default: item.id === updated.id })));
    } catch (reason: unknown) {
      setPageError(reason instanceof Error ? reason.message : "Could not change the default domain");
    } finally {
      setSettingDefaultId(null);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      await api.deleteDomain(deleteTarget.id);
      const refreshed = await api.listDomains();
      setDomains(refreshed);
      setDeleteTarget(null);
    } catch (reason: unknown) {
      setPageError(reason instanceof Error ? reason.message : "Could not delete domain");
    } finally { setDeleteLoading(false); }
  }

  async function handleSaveToken(e: React.FormEvent) {
    e.preventDefault();
    if (!tokenDomain) return;
    setTokenError(""); setTokenLoading(true);
    try {
      const updated = await api.saveDomainToken(tokenDomain.id, token);
      setDomains((ds) => ds.map((d) => d.id === tokenDomain.id ? updated : d));
      setTokenDomain(null); setToken("");
    } catch (err: any) { setTokenError(err.message); }
    finally { setTokenLoading(false); }
  }

  async function handleDeleteToken() {
    if (!deleteTokenTarget) return;
    setPageError("");
    try {
      const updated = await api.deleteDomainToken(deleteTokenTarget.id);
      setDomains((items) => items.map((item) => item.id === deleteTokenTarget.id ? updated : item));
      setDeleteTokenTarget(null);
    } catch (reason: unknown) {
      setPageError(reason instanceof Error ? reason.message : "Could not delete Cloudflare token");
    }
  }

  return (
    <AppShell>
      <div className="p-4 sm:p-7">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-xl sm:text-2xl font-bold">Custom Domains</h1>
            <p className="text-sm text-muted-foreground mt-0.5 hidden sm:block">Use your own domain via Cloudflare</p>
          </div>
          <Button onClick={() => setAddOpen(true)} size="sm" className="sm:h-9 sm:px-4">
            <Plus className="w-4 h-4" /><span className="hidden sm:inline ml-1">Add Domain</span>
          </Button>
        </div>

        {pageError && <div className="mb-4 rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{pageError}</div>}

        <div className="mb-5 p-4 rounded-xl border border-border bg-muted/40 flex gap-3">
          <AlertTriangle className="w-4 h-4 text-yellow-500 shrink-0 mt-0.5" />
          <p className="text-sm text-muted-foreground">
            Add a domain, then click <strong className="text-foreground">Verify</strong>. With a Cloudflare token, Vector configures DNS through the API and installs nginx + SSL without an HTTP ownership challenge. Subdomain links require a healthy token. The current default domain is protected from deletion.
          </p>
        </div>

        {loading ? (
          <div className="flex justify-center py-20"><Spinner /></div>
        ) : domains.length === 0 ? (
          <EmptyState
            icon={<Globe className="w-6 h-6" />}
            title="No custom domains yet"
            description="Add a domain and verify it points to this server"
            action={<Button onClick={() => setAddOpen(true)}><Plus className="w-3.5 h-3.5" /> Add Domain</Button>}
          />
        ) : (
          <>
            {/* Desktop table */}
            <div className="hidden md:block bg-card border border-border rounded-xl overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border bg-muted/40">
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">HOSTNAME</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">STATUS</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">CF TOKEN</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">INFO</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">ADDED</th>
                    <th className="px-4 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {domains.map((d) => (
                    <tr key={d.id} className="hover:bg-muted/30 transition-colors">
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-sm font-medium">{d.hostname}</span>
                          {d.is_default ? (
                            <Badge variant="outline" className="gap-1"><Star className="w-3 h-3 fill-current" />Default</Badge>
                          ) : d.status === "active" ? (
                            <button
                              onClick={() => handleSetDefault(d.id)}
                              disabled={settingDefaultId === d.id}
                              className="inline-flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-primary transition-colors disabled:opacity-50"
                            >
                              {settingDefaultId === d.id ? <Loader2 className="w-3 h-3 animate-spin" /> : <Star className="w-3 h-3" />}
                              Set default
                            </button>
                          ) : null}
                        </div>
                      </td>
                      <td className="px-4 py-3"><StatusBadge status={d.status} /></td>
                      <td className="px-4 py-3">
                        <TokenCell
                          domain={d}
                          onAdd={() => { setTokenDomain(d); setToken(""); setTokenError(""); }}
                          onDelete={() => setDeleteTokenTarget(d)}
                        />
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground max-w-xs truncate">{d.message || "—"}</td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">{formatDate(d.created_at)}</td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1 justify-end">
                          <button
                            onClick={() => handleVerify(d.id)}
                            disabled={verifyingId === d.id}
                            title="Verify domain"
                            className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
                          >
                            {verifyingId === d.id
                              ? <Loader2 className="w-3.5 h-3.5 animate-spin" />
                              : <RefreshCw className="w-3.5 h-3.5" />}
                          </button>
                          <button
                            onClick={() => !d.is_default && setDeleteTarget(d)}
                            disabled={d.is_default}
                            title={d.is_default ? "Default domain is protected. Set another domain as default first." : "Delete domain"}
                            className="p-1.5 rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:text-muted-foreground disabled:hover:bg-transparent"
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Mobile cards */}
            <div className="md:hidden space-y-3">
              {domains.map((d) => (
                <div key={d.id} className="bg-card border border-border rounded-xl p-4 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <div className="flex items-center gap-2 flex-wrap">
                        <p className="font-mono text-sm font-medium">{d.hostname}</p>
                        {d.is_default ? (
                          <Badge variant="outline" className="gap-1"><Star className="w-3 h-3 fill-current" />Default</Badge>
                        ) : d.status === "active" ? (
                          <button
                            onClick={() => handleSetDefault(d.id)}
                            disabled={settingDefaultId === d.id}
                            className="inline-flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-primary transition-colors disabled:opacity-50"
                          >
                            {settingDefaultId === d.id ? <Loader2 className="w-3 h-3 animate-spin" /> : <Star className="w-3 h-3" />}
                            Set default
                          </button>
                        ) : null}
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5">{formatDate(d.created_at)}</p>
                    </div>
                    <StatusBadge status={d.status} />
                  </div>
                  {d.message && (
                    <p className="text-xs text-muted-foreground bg-muted rounded-lg px-3 py-2">{d.message}</p>
                  )}
                  <div className="flex items-center justify-between">
                    <TokenCell
                      domain={d}
                      onAdd={() => { setTokenDomain(d); setToken(""); setTokenError(""); }}
                      onDelete={() => setDeleteTokenTarget(d)}
                    />
                    <div className="flex items-center gap-1">
                      <button
                        onClick={() => handleVerify(d.id)}
                        disabled={verifyingId === d.id}
                        className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
                      >
                        {verifyingId === d.id ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
                      </button>
                      <button
                        onClick={() => !d.is_default && setDeleteTarget(d)}
                        disabled={d.is_default}
                        title={d.is_default ? "Default domain is protected" : "Delete domain"}
                        className="p-1.5 rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:text-muted-foreground disabled:hover:bg-transparent"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      {/* Add domain */}
      <Modal
        open={addOpen}
        onClose={() => { setAddOpen(false); setHostname(""); setAddError(""); }}
        title="Add Custom Domain"
      >
        <form onSubmit={handleAdd} className="flex flex-col gap-4">
          <Input
            label="Hostname"
            placeholder="links.yourdomain.com"
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            required
            autoFocus
          />
          <p className="text-xs text-muted-foreground">
            Vector reuses the default domain's Cloudflare token when available. Click <strong>Verify</strong> to configure the DNS record, nginx virtual host, and SSL certificate.
          </p>
          {addError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{addError}</p>}
          <div className="flex gap-2">
            <Button type="button" variant="secondary" className="flex-1"
              onClick={() => { setAddOpen(false); setHostname(""); setAddError(""); }}>
              Cancel
            </Button>
            <Button type="submit" className="flex-1" loading={addLoading}>
              <Plus className="w-3.5 h-3.5" /> Add Domain
            </Button>
          </div>
        </form>
      </Modal>

      {/* CF Token */}
      <Modal
        open={!!tokenDomain}
        onClose={() => { setTokenDomain(null); setToken(""); setTokenError(""); }}
        title={`Cloudflare Token — ${tokenDomain?.hostname}`}
      >
        <form onSubmit={handleSaveToken} className="flex flex-col gap-4">
          <div className="p-3 bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded-lg">
            <p className="text-xs text-yellow-800 dark:text-yellow-300">
              This token is stored encrypted and <strong>cannot be viewed again</strong> after saving. You can replace or delete it.
            </p>
          </div>
          <Input
            label="Cloudflare API Token"
            type="password"
            placeholder="Paste your API token here"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            required
            autoFocus
          />
          <p className="text-xs text-muted-foreground">
            Create at{" "}
            <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noreferrer" className="text-primary underline">
              dash.cloudflare.com/profile/api-tokens
            </a>{" "}
            with <strong>Zone:Read</strong> and <strong>DNS:Edit</strong> permissions.
          </p>
          {tokenError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{tokenError}</p>}
          <div className="flex gap-2">
            <Button type="button" variant="secondary" className="flex-1"
              onClick={() => { setTokenDomain(null); setToken(""); }}>
              Cancel
            </Button>
            <Button type="submit" className="flex-1" loading={tokenLoading}>
              <Key className="w-3.5 h-3.5" /> Save Token
            </Button>
          </div>
        </form>
      </Modal>

      {/* Delete domain confirmation */}
      <ConfirmDeleteModal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        loading={deleteLoading}
        title={`Delete ${deleteTarget?.hostname}?`}
        description={`This will remove the domain from Vector and delete the Cloudflare DNS record. Short links using this domain will stop working.`}
      />

      {/* Delete CF token confirmation */}
      <ConfirmDeleteModal
        open={!!deleteTokenTarget}
        onClose={() => setDeleteTokenTarget(null)}
        onConfirm={handleDeleteToken}
        title="Remove Cloudflare token?"
        description="The API token will be removed. Vector will no longer be able to manage DNS records for this domain."
        keyword="REMOVE"
      />
    </AppShell>
  );
}

function TokenCell({ domain, onAdd, onDelete }: { domain: Domain; onAdd: () => void; onDelete: () => void }) {
  if (domain.has_token) {
    return (
      <div className="flex items-center gap-2 flex-wrap">
        <span className="inline-flex items-center gap-1 text-xs text-success font-medium">
          <KeyRound className="w-3 h-3" /> Saved
        </span>
        <button onClick={onAdd} className="text-xs text-primary hover:underline">Replace</button>
        <button onClick={onDelete} className="text-xs text-destructive hover:underline">Delete</button>
      </div>
    );
  }
  return (
    <button
      onClick={onAdd}
      className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-primary transition-colors"
    >
      <Key className="w-3 h-3" /> Add token
    </button>
  );
}

function StatusBadge({ status }: { status: Domain["status"] }) {
  switch (status) {
    case "active": return <Badge variant="success"><CheckCircle className="w-3 h-3 mr-1" />Active</Badge>;
    case "pending": return <Badge variant="outline"><Clock className="w-3 h-3 mr-1" />Pending</Badge>;
    case "error": return <Badge variant="destructive"><XCircle className="w-3 h-3 mr-1" />Error</Badge>;
    case "token_missing": return <Badge variant="warning"><AlertTriangle className="w-3 h-3 mr-1" />Token missing</Badge>;
    default: return <Badge>{status}</Badge>;
  }
}
