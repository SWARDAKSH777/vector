import React, { useState, useEffect, useCallback, useRef } from "react";
import { Plus, Copy, Pencil, Trash2, Pause, Play, Check, ExternalLink, Link2, Globe } from "lucide-react";
import { api, type Link, type Domain } from "../lib/api";
import { AppShell } from "../components/AppShell";
import { CreateLinkDialog } from "../components/CreateLinkDialog";
import { EditLinkDialog } from "../components/EditLinkDialog";
import { ConfirmDeleteModal } from "../components/ConfirmDeleteModal";
import { Button, Badge, Spinner, EmptyState } from "../components/ui";
import { timeAgo, formatNumber } from "../lib/utils";

const LINK_PAGE_SIZE = 200;

export function LinksPage() {
  const [links, setLinks] = useState<Link[]>([]);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [editLink, setEditLink] = useState<Link | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Link | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [copied, setCopied] = useState<number | null>(null);
  const [error, setError] = useState("");
  const requestID = useRef(0);

  const load = useCallback(async () => {
    const id = ++requestID.current;
    setLoading(true);
    setError("");
    try {
      const [ls, doms] = await Promise.all([
        api.listLinks(search, statusFilter, LINK_PAGE_SIZE, 0),
        api.listDomains(),
      ]);
      if (id === requestID.current) {
        setLinks(ls);
        setHasMore(ls.length === LINK_PAGE_SIZE);
        setDomains(doms);
      }
    } catch (reason: unknown) {
      if (id === requestID.current) setError(reason instanceof Error ? reason.message : "Could not load links");
    } finally {
      if (id === requestID.current) setLoading(false);
    }
  }, [search, statusFilter]);

  async function loadMore() {
    if (loadingMore || !hasMore) return;
    const id = requestID.current;
    setLoadingMore(true);
    setError("");
    try {
      const next = await api.listLinks(search, statusFilter, LINK_PAGE_SIZE, links.length);
      if (id !== requestID.current) return;
      setLinks((items) => {
        const known = new Set(items.map((item) => item.id));
        return [...items, ...next.filter((item) => !known.has(item.id))];
      });
      setHasMore(next.length === LINK_PAGE_SIZE);
    } catch (reason: unknown) {
      if (id === requestID.current) setError(reason instanceof Error ? reason.message : "Could not load more links");
    } finally {
      if (id === requestID.current) setLoadingMore(false);
    }
  }

  useEffect(() => {
    const t = setTimeout(load, search ? 300 : 0);
    return () => clearTimeout(t);
  }, [load, search]);

  async function copy(link: Link) {
    try {
      await navigator.clipboard.writeText(link.short_url);
      setCopied(link.id); setTimeout(() => setCopied(null), 2000);
    } catch {
      setError("Could not copy the short URL to the clipboard");
    }
  }

  async function toggle(link: Link) {
    setError("");
    try {
      const updated = await api.updateLink(link.id, { status: link.status === "active" ? "paused" : "active" });
      setLinks((items) => items.map((item) => item.id === updated.id ? updated : item));
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : "Could not update link status");
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    setError("");
    try {
      await api.deleteLink(deleteTarget.id);
      setLinks((items) => items.filter((item) => item.id !== deleteTarget.id));
      setDeleteTarget(null);
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : "Could not delete link");
    } finally {
      setDeleteLoading(false);
    }
  }

  return (
    <AppShell search={search} onSearch={setSearch}>
      <div className="p-4 sm:p-7">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-xl sm:text-2xl font-bold">Links</h1>
            <p className="text-sm text-muted-foreground mt-0.5">{links.length} link{links.length !== 1 ? "s" : ""} loaded{hasMore ? "+" : ""}</p>
          </div>
          <Button onClick={() => setCreateOpen(true)} size="sm" className="sm:h-9 sm:text-sm sm:px-4">
            <Plus className="w-4 h-4" /><span className="hidden sm:inline ml-1">Create Link</span><span className="sm:hidden">New</span>
          </Button>
        </div>

        {error && <div className="mb-4 rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{error}</div>}

        <div className="flex gap-1.5 mb-5 overflow-x-auto pb-1">
          {["all", "active", "paused", "expired"].map((s) => (
            <button key={s} onClick={() => setStatusFilter(s)}
              className={`px-3 py-1.5 text-xs rounded-lg font-medium transition-colors capitalize whitespace-nowrap ${
                statusFilter === s ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:text-foreground"}`}>
              {s}
            </button>
          ))}
        </div>

        {loading ? (
          <div className="flex justify-center py-20"><Spinner /></div>
        ) : links.length === 0 ? (
          <EmptyState icon={<Link2 className="w-6 h-6" />}
            title={search ? "No links match your search" : "No links yet"}
            description={search ? "Try a different search term" : "Create your first short link to get started"}
            action={!search && <Button onClick={() => setCreateOpen(true)}><Plus className="w-3.5 h-3.5" /> Create Link</Button>} />
        ) : (
          <>
            {/* Desktop table */}
            <div className="hidden md:block bg-card border border-border rounded-xl overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border bg-muted/40">
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">SHORT LINK</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">DESTINATION</th>
                    <th className="text-right px-4 py-3 text-xs font-medium text-muted-foreground">CLICKS</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">STATUS</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground">CREATED</th>
                    <th className="px-4 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {links.map((link) => (
                    <tr key={link.id} className="hover:bg-muted/30 transition-colors">
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2 flex-wrap">
                          <a href={link.short_url} target="_blank" rel="noreferrer"
                            className="font-mono text-xs text-primary hover:underline">
                            {link.short_url.replace(/^https?:\/\//, "")}
                          </a>
                          {link.redirect_type === "subdomain" && <Badge variant="outline"><Globe className="w-2.5 h-2.5 mr-0.5 inline" />sub</Badge>}
                          {link.tag && <Badge variant="outline">{link.tag}</Badge>}
                          {link.has_password && <span title="Password protected" className="text-xs">🔒</span>}
                          {link.expires_at && <span title={`Expires ${link.expires_at}`} className="text-xs">⏱</span>}
                          {link.max_clicks && <span title={`Max ${link.max_clicks} clicks`} className="text-xs">🚫</span>}
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <a href={link.destination_url} target="_blank" rel="noreferrer"
                          className="text-muted-foreground hover:text-foreground flex items-center gap-1 max-w-xs group">
                          <span className="truncate text-xs">{link.destination_url}</span>
                          <ExternalLink className="w-3 h-3 shrink-0 opacity-0 group-hover:opacity-100" />
                        </a>
                      </td>
                      <td className="px-4 py-3 text-right font-medium tabular-nums">{formatNumber(link.click_count)}</td>
                      <td className="px-4 py-3"><Badge variant={link.status === "active" ? "success" : "warning"}>{link.status}</Badge></td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">{timeAgo(link.created_at)}</td>
                      <td className="px-4 py-3">
                        <LinkActions link={link} copied={copied} onCopy={copy}
                          onEdit={setEditLink} onToggle={toggle} onDelete={setDeleteTarget} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Mobile cards */}
            <div className="md:hidden space-y-3">
              {links.map((link) => (
                <div key={link.id} className="bg-card border border-border rounded-xl p-4">
                  <div className="flex items-start justify-between gap-2 mb-2">
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5 flex-wrap">
                        <a href={link.short_url} target="_blank" rel="noreferrer"
                          className="font-mono text-sm text-primary hover:underline font-medium">
                          {link.short_url.replace(/^https?:\/\//, "")}
                        </a>
                        <Badge variant={link.status === "active" ? "success" : "warning"}>{link.status}</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground truncate mt-1">{link.destination_url}</p>
                    </div>
                    <span className="text-sm font-bold tabular-nums shrink-0">{formatNumber(link.click_count)}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2 flex-wrap">
                      {link.tag && <Badge variant="outline">{link.tag}</Badge>}
                      {link.has_password && <span className="text-xs text-muted-foreground">🔒</span>}
                      <span className="text-xs text-muted-foreground">{timeAgo(link.created_at)}</span>
                    </div>
                    <LinkActions link={link} copied={copied} onCopy={copy}
                      onEdit={setEditLink} onToggle={toggle} onDelete={setDeleteTarget} />
                  </div>
                </div>
              ))}
            </div>

            {hasMore && (
              <div className="flex justify-center pt-5">
                <Button variant="secondary" onClick={loadMore} loading={loadingMore}>Load more links</Button>
              </div>
            )}
          </>
        )}
      </div>

      <CreateLinkDialog open={createOpen} onClose={() => setCreateOpen(false)}
        onCreated={(l) => { setLinks((ls) => [l, ...ls]); setCreateOpen(false); }} domains={domains} />
      <EditLinkDialog link={editLink} onClose={() => setEditLink(null)}
        onUpdated={(l) => setLinks((ls) => ls.map((x) => x.id === l.id ? l : x))} />
      <ConfirmDeleteModal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        loading={deleteLoading}
        title={`Delete /${deleteTarget?.short_code}?`}
        description="This link and all its click data will be permanently deleted."
      />
    </AppShell>
  );
}

function LinkActions({ link, copied, onCopy, onEdit, onToggle, onDelete }: {
  link: Link; copied: number | null;
  onCopy: (l: Link) => void; onEdit: (l: Link) => void;
  onToggle: (l: Link) => void; onDelete: (l: Link) => void;
}) {
  return (
    <div className="flex items-center gap-0.5">
      <button onClick={() => onCopy(link)} title="Copy" className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
        {copied === link.id ? <Check className="w-3.5 h-3.5 text-success" /> : <Copy className="w-3.5 h-3.5" />}
      </button>
      <button onClick={() => onEdit(link)} title="Edit" className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
        <Pencil className="w-3.5 h-3.5" />
      </button>
      <button onClick={() => onToggle(link)} title={link.status === "active" ? "Pause" : "Resume"}
        className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
        {link.status === "active" ? <Pause className="w-3.5 h-3.5" /> : <Play className="w-3.5 h-3.5" />}
      </button>
      <button onClick={() => onDelete(link)} title="Delete"
        className="p-1.5 rounded-md text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors">
        <Trash2 className="w-3.5 h-3.5" />
      </button>
    </div>
  );
}
