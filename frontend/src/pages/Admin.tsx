import React, { useEffect, useState } from "react";
import { UserPlus, Users, ShieldCheck, Power, PowerOff, KeyRound } from "lucide-react";
import { api, type AdminUser } from "../lib/api";
import { AppShell } from "../components/AppShell";
import { Badge, Button, Card, EmptyState, Input, Modal, Spinner } from "../components/ui";
import { formatDate } from "../lib/utils";

export function AdminPage() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [creating, setCreating] = useState(false);
  const [busyID, setBusyID] = useState<number | null>(null);
  const [resetTarget, setResetTarget] = useState<AdminUser | null>(null);
  const [resetPassword, setResetPassword] = useState("");
  const [resetError, setResetError] = useState("");

  async function loadUsers() {
    setPageError("");
    try { setUsers(await api.listAdminUsers()); }
    catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not load users"); }
    finally { setLoading(false); }
  }

  useEffect(() => { void loadUsers(); }, []);

  async function createUser(e: React.FormEvent) {
    e.preventDefault();
    setCreating(true); setPageError("");
    try {
      await api.createAdminUser(email.trim(), password);
      setEmail(""); setPassword("");
      await loadUsers();
    } catch (reason: unknown) {
      setPageError(reason instanceof Error ? reason.message : "Could not create user");
    } finally { setCreating(false); }
  }

  async function deactivate(user: AdminUser) {
    if (!window.confirm(`Deactivate ${user.email}? Login and existing sessions will be blocked, but all links, domains, DNS records and analytics will remain intact.`)) return;
    setBusyID(user.id); setPageError("");
    try { await api.deactivateAdminUser(user.id); await loadUsers(); }
    catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not deactivate user"); }
    finally { setBusyID(null); }
  }

  async function reactivate(user: AdminUser) {
    setBusyID(user.id); setPageError("");
    try { await api.reactivateAdminUser(user.id); await loadUsers(); }
    catch (reason: unknown) { setPageError(reason instanceof Error ? reason.message : "Could not reactivate user"); }
    finally { setBusyID(null); }
  }

  async function resetUserPassword(e: React.FormEvent) {
    e.preventDefault();
    if (!resetTarget) return;
    setResetError(""); setBusyID(resetTarget.id);
    try {
      await api.resetAdminUserPassword(resetTarget.id, resetPassword);
      setResetTarget(null); setResetPassword("");
    } catch (reason: unknown) {
      setResetError(reason instanceof Error ? reason.message : "Could not reset password");
    } finally { setBusyID(null); }
  }

  return <AppShell><div className="p-4 sm:p-7 space-y-5">
    <div>
      <h1 className="text-xl sm:text-2xl font-bold">User Administration</h1>
      <p className="text-sm text-muted-foreground mt-0.5">Create accounts and control login access without deleting user content.</p>
    </div>

    {pageError && <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{pageError}</div>}

    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
      <Card className="order-2 xl:order-1">
        <div className="flex items-center justify-between gap-3 mb-4">
          <div><h2 className="font-semibold">Accounts</h2><p className="text-xs text-muted-foreground mt-1">Deactivation revokes sessions and blocks login only.</p></div>
          <Badge variant="outline">{users.filter((u) => u.role === "user" && !u.disabled).length} active users</Badge>
        </div>
        {loading ? <div className="py-16 flex justify-center"><Spinner /></div> : users.length === 0 ?
          <EmptyState icon={<Users className="w-6 h-6" />} title="No accounts" description="Create the first user account" /> :
          <div className="overflow-x-auto -mx-4 sm:-mx-5">
            <table className="w-full min-w-[760px] text-sm">
              <thead><tr className="border-y border-border bg-muted/40 text-xs text-muted-foreground">
                <th className="text-left px-4 sm:px-5 py-3 font-medium">ACCOUNT</th>
                <th className="text-left px-3 py-3 font-medium">STATUS</th>
                <th className="text-left px-3 py-3 font-medium">CONTENT</th>
                <th className="text-left px-3 py-3 font-medium">CREATED</th>
                <th className="px-4 sm:px-5 py-3" />
              </tr></thead>
              <tbody className="divide-y divide-border">{users.map((user) => <tr key={user.id} className="hover:bg-muted/20">
                <td className="px-4 sm:px-5 py-3"><p className="font-medium">{user.email}</p><p className="text-xs text-muted-foreground mt-0.5">{user.role === "admin" ? "System administrator" : `User #${user.id}`}</p></td>
                <td className="px-3 py-3">{user.role === "admin" ? <Badge variant="outline"><ShieldCheck className="w-3 h-3 mr-1" />Admin</Badge> : user.disabled ? <Badge variant="destructive">Deactivated</Badge> : <Badge variant="success">Active</Badge>}</td>
                <td className="px-3 py-3 text-xs text-muted-foreground">{user.link_count} links · {user.owned_domains} owned · {user.shared_domains} shared</td>
                <td className="px-3 py-3 text-xs text-muted-foreground">{formatDate(user.created_at)}</td>
                <td className="px-4 sm:px-5 py-3"><div className="flex justify-end gap-2">
                  {user.role === "user" && <>
                    <Button size="sm" variant="secondary" onClick={() => { setResetTarget(user); setResetPassword(""); setResetError(""); }} disabled={busyID === user.id}><KeyRound className="w-3.5 h-3.5" />Reset</Button>
                    {user.disabled ?
                      <Button size="sm" onClick={() => reactivate(user)} loading={busyID === user.id}><Power className="w-3.5 h-3.5" />Reactivate</Button> :
                      <Button size="sm" variant="secondary" onClick={() => deactivate(user)} loading={busyID === user.id}><PowerOff className="w-3.5 h-3.5" />Deactivate</Button>}
                  </>}
                </div></td>
              </tr>)}</tbody>
            </table>
          </div>}
      </Card>

      <Card className="order-1 xl:order-2 h-fit">
        <div className="flex items-center gap-2 mb-1"><UserPlus className="w-4 h-4 text-primary" /><h2 className="font-semibold">Create User</h2></div>
        <p className="text-xs text-muted-foreground mb-4">The user can add domains with their own Cloudflare token and can receive access to domains shared by other owners.</p>
        <form onSubmit={createUser} className="space-y-3">
          <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required placeholder="user@example.com" autoComplete="off" />
          <Input label="Initial password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required placeholder="At least 15 characters" autoComplete="new-password" />
          <Button type="submit" className="w-full" loading={creating}><UserPlus className="w-3.5 h-3.5" />Create Account</Button>
        </form>
      </Card>
    </div>
  </div>

  <Modal open={!!resetTarget} onClose={() => { setResetTarget(null); setResetPassword(""); setResetError(""); }} title={`Reset password — ${resetTarget?.email || ""}`}>
    <form onSubmit={resetUserPassword} className="space-y-4">
      <p className="text-xs text-muted-foreground">Saving a new password immediately revokes all active sessions for this user.</p>
      <Input label="New password" type="password" value={resetPassword} onChange={(e) => setResetPassword(e.target.value)} required placeholder="At least 15 characters" autoComplete="new-password" />
      {resetError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{resetError}</p>}
      <div className="flex gap-2"><Button type="button" variant="secondary" className="flex-1" onClick={() => setResetTarget(null)}>Cancel</Button><Button type="submit" className="flex-1" loading={busyID === resetTarget?.id}>Reset Password</Button></div>
    </form>
  </Modal>
  </AppShell>;
}
