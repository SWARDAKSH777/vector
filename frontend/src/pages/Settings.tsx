import React, { useEffect, useState } from "react";
import { useAuth } from "../lib/auth";
import { api, type IPInfoTokenStatus, type PrivacySettings } from "../lib/api";
import { AppShell } from "../components/AppShell";
import { Card, Button, Input, Checkbox, Badge } from "../components/ui";

export function SettingsPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [pwdError, setPwdError] = useState("");
  const [pwdSuccess, setPwdSuccess] = useState("");
  const [pwdLoading, setPwdLoading] = useState(false);
  const [privacy, setPrivacy] = useState<PrivacySettings | null>(null);
  const [privacyMessage, setPrivacyMessage] = useState("");
  const [privacyLoading, setPrivacyLoading] = useState(false);
  const [ipinfoStatus, setIPInfoStatus] = useState<IPInfoTokenStatus | null>(null);
  const [ipinfoToken, setIPInfoToken] = useState("");
  const [ipinfoEditing, setIPInfoEditing] = useState(false);
  const [ipinfoLoading, setIPInfoLoading] = useState(false);
  const [ipinfoMessage, setIPInfoMessage] = useState("");
  const [ipinfoError, setIPInfoError] = useState("");

  useEffect(() => {
    api.getPrivacySettings().then(setPrivacy).catch(() => setPrivacyMessage("Could not load privacy settings"));
    if (isAdmin) api.getIPInfoTokenStatus().then(setIPInfoStatus).catch(() => setIPInfoError("Could not load IPinfo token status"));
  }, [isAdmin]);

  async function handlePasswordChange(e: React.FormEvent) {
    e.preventDefault(); setPwdError(""); setPwdSuccess("");
    if (next !== confirm) { setPwdError("Passwords don't match"); return; }
    if ([...next].length < 15) { setPwdError("Use a passphrase of at least 15 characters"); return; }
    setPwdLoading(true);
    try { await api.updatePassword(current, next); setPwdSuccess("Password updated successfully"); setCurrent(""); setNext(""); setConfirm(""); }
    catch (reason: unknown) { setPwdError(reason instanceof Error ? reason.message : "Could not update password"); }
    finally { setPwdLoading(false); }
  }

  async function savePrivacy() {
    if (!privacy || !isAdmin) return;
    setPrivacyLoading(true); setPrivacyMessage("");
    try {
      setPrivacy(await api.updatePrivacySettings({ analytics_enabled: privacy.analytics_enabled, analytics_retention_days: privacy.analytics_retention_days, audit_retention_days: privacy.audit_retention_days }));
      setPrivacyMessage("Privacy settings saved");
    } catch (reason: unknown) { setPrivacyMessage(reason instanceof Error ? reason.message : "Could not save privacy settings"); }
    finally { setPrivacyLoading(false); }
  }

  async function saveIPInfoToken(e: React.FormEvent) {
    e.preventDefault(); setIPInfoError(""); setIPInfoMessage("");
    if (!ipinfoToken.trim()) { setIPInfoError("Enter an IPinfo Lite fallback token"); return; }
    setIPInfoLoading(true);
    try {
      setIPInfoStatus(await api.saveIPInfoToken(ipinfoToken.trim()));
      setIPInfoToken(""); setIPInfoEditing(false); setIPInfoMessage("IPinfo fallback token validated and saved securely");
    } catch (reason: unknown) { setIPInfoError(reason instanceof Error ? reason.message : "Could not save token"); }
    finally { setIPInfoLoading(false); }
  }

  async function deleteIPInfoToken() {
    if (!window.confirm("Remove the saved IPinfo fallback token?")) return;
    setIPInfoLoading(true); setIPInfoError(""); setIPInfoMessage("");
    try { setIPInfoStatus(await api.deleteIPInfoToken()); setIPInfoEditing(false); setIPInfoMessage("IPinfo fallback token removed"); }
    catch (reason: unknown) { setIPInfoError(reason instanceof Error ? reason.message : "Could not remove token"); }
    finally { setIPInfoLoading(false); }
  }

  async function clearAnalytics() {
    if (!window.confirm("Permanently delete analytics for your links and reset their visible click counters? Lifetime counters used for max-click enforcement remain intact.")) return;
    try { const result = await api.deleteAnalytics(); setPrivacyMessage(`Deleted ${result.deleted} analytics records and reset ${result.counters_reset} link counters`); }
    catch (reason: unknown) { setPrivacyMessage(reason instanceof Error ? reason.message : "Could not delete analytics"); }
  }

  const showIPInfoForm = !ipinfoStatus?.has_token || ipinfoEditing;

  return <AppShell><div className="p-4 sm:p-7 max-w-2xl space-y-5">
    <div><h1 className="text-xl sm:text-2xl font-bold">Settings</h1><p className="text-sm text-muted-foreground mt-0.5">Account, privacy and installation controls</p></div>

    <Card><div className="flex items-start justify-between gap-3"><div><h2 className="font-semibold mb-4">Account</h2><div className="space-y-3"><div><p className="text-xs text-muted-foreground mb-1">Email</p><p className="text-sm font-medium">{user?.email}</p></div><div><p className="text-xs text-muted-foreground mb-1">User ID</p><p className="text-sm font-mono">{user?.id}</p></div></div></div><div className="flex flex-col items-end gap-2"><Badge variant="outline">{isAdmin ? "Administrator" : "User"}</Badge><Badge variant={user?.multi_user ? "success" : "outline"}>{user?.multi_user ? "Multi-user" : "Single-user"}</Badge></div></div></Card>

    <Card><h2 className="font-semibold mb-4">Change Password</h2><form onSubmit={handlePasswordChange} className="space-y-3">
      <Input label="Current password" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required placeholder="••••••••" autoComplete="current-password" />
      <Input label="New password" type="password" value={next} onChange={(e) => setNext(e.target.value)} required placeholder="At least 15 characters" autoComplete="new-password" />
      <Input label="Confirm new password" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required placeholder="Repeat new password" autoComplete="new-password" />
      {pwdError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{pwdError}</p>}
      {pwdSuccess && <p className="text-sm text-success bg-success/10 px-3 py-2 rounded-lg">{pwdSuccess}</p>}
      <Button type="submit" loading={pwdLoading}>Update Password</Button>
    </form></Card>

    {isAdmin && <Card>
      <div className="flex items-start justify-between gap-4 mb-2"><div><h2 className="font-semibold">Country Analytics</h2><p className="text-xs text-muted-foreground mt-1">Installation-wide Cloudflare country headers · optional IPinfo Lite fallback</p></div>{ipinfoStatus?.has_token && <Badge variant="success">Saved</Badge>}</div>
      <p className="text-xs text-muted-foreground mb-4">This token is an installation secret. It is encrypted with Vector's master key, never returned by the API, and manageable only by the administrator.</p>
      {showIPInfoForm ? <form onSubmit={saveIPInfoToken} className="space-y-3">
        <Input label={ipinfoStatus?.has_token ? "Replacement IPinfo fallback token" : "IPinfo Lite fallback token"} type="password" value={ipinfoToken} onChange={(e) => setIPInfoToken(e.target.value)} required placeholder="Paste token" autoComplete="new-password" />
        <div className="flex flex-wrap gap-2"><Button type="submit" loading={ipinfoLoading}>{ipinfoStatus?.has_token ? "Validate & Replace" : "Validate & Save"}</Button>{ipinfoStatus?.has_token && <Button type="button" variant="secondary" onClick={() => { setIPInfoEditing(false); setIPInfoToken(""); setIPInfoError(""); }}>Cancel</Button>}</div>
      </form> : <div className="flex flex-wrap gap-2"><Button type="button" variant="secondary" onClick={() => { setIPInfoEditing(true); setIPInfoMessage(""); setIPInfoError(""); }}>Replace Token</Button><Button type="button" variant="secondary" onClick={deleteIPInfoToken} loading={ipinfoLoading}>Delete Token</Button></div>}
      {ipinfoError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg mt-3">{ipinfoError}</p>}
      {ipinfoMessage && <p className="text-sm text-success bg-success/10 px-3 py-2 rounded-lg mt-3">{ipinfoMessage}</p>}
    </Card>}

    {privacy && <Card><div className="flex items-start justify-between gap-3 mb-2"><div><h2 className="font-semibold">Privacy & Retention</h2><p className="text-xs text-muted-foreground mt-1">Installation policy applies to every account; each user can erase or export their own data.</p></div>{!isAdmin && <Badge variant="outline">Read only</Badge>}</div><div className="space-y-4 mt-4">
      <Checkbox disabled={!isAdmin} checked={privacy.analytics_enabled} onChange={(checked) => setPrivacy({ ...privacy, analytics_enabled: checked })} label="Collect privacy-preserving analytics" description="Collect pseudonymous browser, device, referrer-origin and coarse country data for eligible clicks." />
      <Input disabled={!isAdmin} label="Detailed analytics retention (days)" type="number" min={1} max={3650} value={privacy.analytics_retention_days} onChange={(e) => setPrivacy({ ...privacy, analytics_retention_days: Number(e.target.value) })} />
      <Input disabled={!isAdmin} label="Security audit retention (days)" type="number" min={30} max={3650} value={privacy.audit_retention_days} onChange={(e) => setPrivacy({ ...privacy, audit_retention_days: Number(e.target.value) })} />
      {privacyMessage && <p className="text-sm text-muted-foreground">{privacyMessage}</p>}
      <div className="flex flex-wrap gap-2">{isAdmin && <Button onClick={savePrivacy} loading={privacyLoading}>Save Installation Policy</Button>}<Button variant="secondary" onClick={clearAnalytics}>Delete My Analytics</Button><a href={api.dataExportURL} download><Button variant="secondary">Export My Data</Button></a></div>
    </div></Card>}
  </div></AppShell>;
}
