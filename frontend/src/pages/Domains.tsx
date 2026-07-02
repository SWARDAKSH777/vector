import React, { useEffect, useState } from "react";
import { Plus, Globe, Trash2, RefreshCw, CheckCircle, XCircle, Clock, AlertTriangle, Key, KeyRound, Loader2, Star, Users, UserMinus, ShieldCheck } from "lucide-react";
import { api, type Domain, type DomainMember } from "../lib/api";
import { useAuth } from "../lib/auth";
import { AppShell } from "../components/AppShell";
import { Button, Badge, Spinner, EmptyState, Modal, Input, Card } from "../components/ui";
import { ConfirmDeleteModal } from "../components/ConfirmDeleteModal";
import { formatDate } from "../lib/utils";

export function DomainsPage() {
  const { user } = useAuth();
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState("");

  const [addOpen, setAddOpen] = useState(false);
  const [hostname, setHostname] = useState("");
  const [addError, setAddError] = useState("");
  const [addLoading, setAddLoading] = useState(false);
  const [verifyingID, setVerifyingID] = useState<number | null>(null);
  const [settingDefaultID, setSettingDefaultID] = useState<number | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Domain | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const [tokenDomain, setTokenDomain] = useState<Domain | null>(null);
  const [token, setToken] = useState("");
  const [tokenLoading, setTokenLoading] = useState(false);
  const [tokenError, setTokenError] = useState("");
  const [deleteTokenTarget, setDeleteTokenTarget] = useState<Domain | null>(null);

  const [accessDomain, setAccessDomain] = useState<Domain | null>(null);
  const [members, setMembers] = useState<DomainMember[]>([]);
  const [memberEmail, setMemberEmail] = useState("");
  const [membersLoading, setMembersLoading] = useState(false);
  const [memberBusyID, setMemberBusyID] = useState<number | null>(null);
  const [memberError, setMemberError] = useState("");

  async function loadDomains() {
    setPageError("");
    try { setDomains(await api.listDomains()); }
    catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not load domains"); }
    finally { setLoading(false); }
  }

  useEffect(() => { void loadDomains(); }, []);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault(); setAddError(""); setAddLoading(true);
    try {
      const domain = await api.addDomain(hostname.trim());
      setDomains((items) => [domain, ...items]);
      setAddOpen(false); setHostname("");
    } catch (reason: unknown) { setAddError(reason instanceof Error ? reason.message : "Could not add domain"); }
    finally { setAddLoading(false); }
  }

  async function handleVerify(id: number) {
    setVerifyingID(id); setPageError("");
    try {
      const updated = await api.verifyDomain(id);
      setDomains((items) => items.map((item) => item.id === id ? updated : item));
    } catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not verify domain"); }
    finally { setVerifyingID(null); }
  }

  async function handleSetDefault(id: number) {
    setSettingDefaultID(id); setPageError("");
    try {
      const updated = await api.setDefaultDomain(id);
      setDomains((items) => items.map((item) => ({ ...item, is_default: item.id === updated.id })));
    } catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not change the default domain"); }
    finally { setSettingDefaultID(null); }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    setDeleteLoading(true); setPageError("");
    try { await api.deleteDomain(deleteTarget.id); setDeleteTarget(null); await loadDomains(); }
    catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not delete domain"); }
    finally { setDeleteLoading(false); }
  }

  async function handleSaveToken(e: React.FormEvent) {
    e.preventDefault();
    if (!tokenDomain) return;
    setTokenError(""); setTokenLoading(true);
    try {
      const updated = await api.saveDomainToken(tokenDomain.id, token);
      setDomains((items) => items.map((item) => item.id === updated.id ? updated : item));
      setTokenDomain(null); setToken("");
    } catch (reason: unknown) { setTokenError(reason instanceof Error ? reason.message : "Could not save token"); }
    finally { setTokenLoading(false); }
  }

  async function handleDeleteToken() {
    if (!deleteTokenTarget) return;
    setPageError("");
    try {
      const updated = await api.deleteDomainToken(deleteTokenTarget.id);
      setDomains((items) => items.map((item) => item.id === updated.id ? updated : item));
      setDeleteTokenTarget(null);
    } catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not remove token"); }
  }

  async function openAccess(domain: Domain) {
    setAccessDomain(domain); setMembers([]); setMemberEmail(""); setMemberError(""); setMembersLoading(true);
    try { setMembers(await api.listDomainMembers(domain.id)); }
    catch (reason: unknown) { setMemberError(reason instanceof Error ? reason.message : "Could not load access list"); }
    finally { setMembersLoading(false); }
  }

  async function addMember(e: React.FormEvent) {
    e.preventDefault();
    if (!accessDomain) return;
    setMemberError(""); setMembersLoading(true);
    try {
      const member = await api.addDomainMember(accessDomain.id, memberEmail.trim());
      setMembers((items) => [...items, member]); setMemberEmail("");
    } catch (reason: unknown) { setMemberError(reason instanceof Error ? reason.message : "Could not share domain"); }
    finally { setMembersLoading(false); }
  }

  async function removeMember(member: DomainMember) {
    if (!accessDomain || member.access_role === "owner") return;
    if (!window.confirm(`Remove ${member.email} from ${accessDomain.hostname}? Their existing links stay live, but they cannot create new links on this domain.`)) return;
    setMemberBusyID(member.user_id); setMemberError("");
    try {
      await api.deleteDomainMember(accessDomain.id, member.user_id);
      setMembers((items) => items.filter((item) => item.user_id !== member.user_id));
    } catch (reason: unknown) { setMemberError(reason instanceof Error ? reason.message : "Could not remove access"); }
    finally { setMemberBusyID(null); }
  }

  return <AppShell><div className="p-4 sm:p-7">
    <div className="flex items-center justify-between gap-3 mb-5">
      <div><h1 className="text-xl sm:text-2xl font-bold">Custom Domains</h1><p className="text-sm text-muted-foreground mt-0.5">Own domains with your token, or use domains another owner shared with you.</p></div>
      <Button onClick={() => setAddOpen(true)} size="sm"><Plus className="w-4 h-4" /><span className="hidden sm:inline ml-1">Add Domain</span></Button>
    </div>

    {pageError && <div className="mb-4 rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{pageError}</div>}
    <div className="mb-5 p-4 rounded-xl border border-border bg-muted/40 flex gap-3">
      <ShieldCheck className="w-4 h-4 text-primary shrink-0 mt-0.5" />
      <p className="text-sm text-muted-foreground">Each domain has exactly one owner. Only the owner can view token status, verify DNS, delete the domain, or manage access. Shared users can create links and select the domain as their own default; the Cloudflare token is never exposed to them.</p>
    </div>

    {loading ? <div className="flex justify-center py-20"><Spinner /></div> : domains.length === 0 ?
      <EmptyState icon={<Globe className="w-6 h-6" />} title="No domains available" description="Add a domain with your own Cloudflare token" action={<Button onClick={() => setAddOpen(true)}><Plus className="w-3.5 h-3.5" />Add Domain</Button>} /> :
      <div className="grid gap-4 xl:grid-cols-2">{domains.map((domain) => <Card key={domain.id} className="space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap"><p className="font-mono font-semibold break-all">{domain.hostname}</p>{domain.is_default && <Badge variant="outline"><Star className="w-3 h-3 mr-1 fill-current" />Default</Badge>}</div>
            <div className="flex items-center gap-2 mt-1 flex-wrap">
              <Badge variant={domain.access_role === "owner" ? "outline" : "warning"}>{domain.access_role === "owner" ? "Owned by you" : "Shared with you"}</Badge>
              {domain.access_role === "member" && <span className="text-xs text-muted-foreground">Owner: {domain.owner_email}</span>}
            </div>
          </div>
          <StatusBadge status={domain.status} />
        </div>

        {domain.message && <p className="text-xs text-muted-foreground bg-muted rounded-lg px-3 py-2">{domain.message}</p>}
        <div className="grid grid-cols-2 gap-3 text-xs">
          <div><p className="text-muted-foreground">DNS management</p><p className="font-medium mt-1">{domain.dns_ready ? "Ready for subdomains" : domain.can_manage ? "Needs a healthy token" : "Managed by owner"}</p></div>
          <div><p className="text-muted-foreground">Added</p><p className="font-medium mt-1">{formatDate(domain.created_at)}</p></div>
        </div>

        <div className="flex flex-wrap items-center gap-2 pt-1">
          {!domain.is_default && domain.status === "active" && <Button size="sm" variant="secondary" onClick={() => handleSetDefault(domain.id)} loading={settingDefaultID === domain.id}><Star className="w-3.5 h-3.5" />Set Default</Button>}
          {domain.can_manage && <>
            <Button size="sm" variant="secondary" onClick={() => handleVerify(domain.id)} loading={verifyingID === domain.id}><RefreshCw className="w-3.5 h-3.5" />Verify</Button>
            <TokenButton domain={domain} onSave={() => { setTokenDomain(domain); setToken(""); setTokenError(""); }} onDelete={() => setDeleteTokenTarget(domain)} />
            {user?.multi_user && <Button size="sm" variant="secondary" onClick={() => openAccess(domain)}><Users className="w-3.5 h-3.5" />Manage Access</Button>}
            <Button size="sm" variant="secondary" disabled={domain.is_default} onClick={() => !domain.is_default && setDeleteTarget(domain)}><Trash2 className="w-3.5 h-3.5" />Delete</Button>
          </>}
        </div>
      </Card>)}</div>}
  </div>

  <Modal open={addOpen} onClose={() => { setAddOpen(false); setHostname(""); setAddError(""); }} title="Add Custom Domain">
    <form onSubmit={handleAdd} className="space-y-4">
      <Input label="Hostname" placeholder="links.yourdomain.com" value={hostname} onChange={(e) => setHostname(e.target.value)} required autoFocus />
      <p className="text-xs text-muted-foreground">The new domain belongs only to your account. Vector may reuse the token from one of your own default domains, but never from a domain shared with you.</p>
      {addError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{addError}</p>}
      <div className="flex gap-2"><Button type="button" variant="secondary" className="flex-1" onClick={() => setAddOpen(false)}>Cancel</Button><Button type="submit" className="flex-1" loading={addLoading}><Plus className="w-3.5 h-3.5" />Add Domain</Button></div>
    </form>
  </Modal>

  <Modal open={!!tokenDomain} onClose={() => { setTokenDomain(null); setToken(""); setTokenError(""); }} title={`Cloudflare Token — ${tokenDomain?.hostname || ""}`}>
    <form onSubmit={handleSaveToken} className="space-y-4">
      <div className="p-3 bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800 rounded-lg"><p className="text-xs text-yellow-800 dark:text-yellow-300">The token is encrypted with Vector's master key and is never returned by the API or shown to shared users.</p></div>
      <Input label="Cloudflare API Token" type="password" placeholder="Paste your API token" value={token} onChange={(e) => setToken(e.target.value)} required autoFocus />
      <p className="text-xs text-muted-foreground">Use a scoped token with Zone:Read and DNS:Edit for this domain.</p>
      {tokenError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{tokenError}</p>}
      <div className="flex gap-2"><Button type="button" variant="secondary" className="flex-1" onClick={() => setTokenDomain(null)}>Cancel</Button><Button type="submit" className="flex-1" loading={tokenLoading}><Key className="w-3.5 h-3.5" />Save Token</Button></div>
    </form>
  </Modal>

  <Modal open={!!accessDomain} onClose={() => { setAccessDomain(null); setMembers([]); setMemberError(""); }} title={`Domain Access — ${accessDomain?.hostname || ""}`} maxWidth="sm:max-w-xl">
    <div className="space-y-4">
      <form onSubmit={addMember} className="flex flex-col sm:flex-row gap-2"><div className="flex-1"><Input label="Existing user email" type="email" value={memberEmail} onChange={(e) => setMemberEmail(e.target.value)} required placeholder="user@example.com" /></div><Button type="submit" className="sm:self-end" loading={membersLoading}><Plus className="w-3.5 h-3.5" />Grant Access</Button></form>
      <p className="text-xs text-muted-foreground">Access is immediate. Members can create links, but cannot manage the domain or token. Removing access does not break links they already created.</p>
      {memberError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{memberError}</p>}
      {membersLoading && members.length === 0 ? <div className="py-8 flex justify-center"><Spinner /></div> : <div className="rounded-xl border border-border divide-y divide-border">{members.map((member) => <div key={member.user_id} className="flex items-center justify-between gap-3 px-3 py-3"><div className="min-w-0"><p className="text-sm font-medium truncate">{member.email}</p><p className="text-xs text-muted-foreground">{member.access_role === "owner" ? "Owner" : "Can create links"}</p></div>{member.access_role === "member" && <Button size="sm" variant="secondary" onClick={() => removeMember(member)} loading={memberBusyID === member.user_id}><UserMinus className="w-3.5 h-3.5" />Remove</Button>}</div>)}</div>}
    </div>
  </Modal>

  <ConfirmDeleteModal open={!!deleteTarget} onClose={() => setDeleteTarget(null)} onConfirm={handleDelete} loading={deleteLoading} title={`Delete ${deleteTarget?.hostname || "domain"}?`} description="Vector will remove managed DNS and TLS configuration. Deletion is blocked while any user's links still use this domain." />
  <ConfirmDeleteModal open={!!deleteTokenTarget} onClose={() => setDeleteTokenTarget(null)} onConfirm={handleDeleteToken} title="Remove Cloudflare token?" description="The encrypted token will be removed. Active certificates or managed DNS records must be removed first." keyword="REMOVE" />
  </AppShell>;
}

function TokenButton({ domain, onSave, onDelete }: { domain: Domain; onSave: () => void; onDelete: () => void }) {
  if (domain.has_token) return <><Button size="sm" variant="secondary" onClick={onSave}><KeyRound className="w-3.5 h-3.5" />Replace Token</Button><Button size="sm" variant="secondary" onClick={onDelete}><Key className="w-3.5 h-3.5" />Remove Token</Button></>;
  return <Button size="sm" variant="secondary" onClick={onSave}><Key className="w-3.5 h-3.5" />Add Token</Button>;
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
