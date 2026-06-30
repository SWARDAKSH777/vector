import React, { useState, useEffect } from "react";
import { api, type Link } from "../lib/api";
import { Modal, Button, Input, Textarea, Select } from "./ui";

interface Props { link: Link | null; onClose: () => void; onUpdated: (link: Link) => void; }

export function EditLinkDialog({ link, onClose, onUpdated }: Props) {
  const [dest, setDest] = useState("");
  const [tag, setTag] = useState("");
  const [notes, setNotes] = useState("");
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [expiresIn, setExpiresIn] = useState("");
  const [customExpiry, setCustomExpiry] = useState("");
  const [clearExpiry, setClearExpiry] = useState(false);
  const [maxClicks, setMaxClicks] = useState("");
  const [clearMaxClicks, setClearMaxClicks] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (link) {
      setDest(link.destination_url); setTag(link.tag ?? ""); setNotes(link.notes ?? "");
      setPassword(""); setClearPassword(false);
      setExpiresIn(""); setCustomExpiry(link.expires_at ? link.expires_at.split("T")[0] : "");
      setClearExpiry(false);
      setMaxClicks(link.max_clicks ? String(link.max_clicks) : ""); setClearMaxClicks(false);
      setError("");
    }
  }, [link]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!link) return;
    setError(""); setLoading(true);
    try {
      const updated = await api.updateLink(link.id, {
        destination_url: dest, tag, notes,
        password: password || undefined, clear_password: clearPassword,
        expires_in: expiresIn || undefined,
        expires_at: !expiresIn && customExpiry ? customExpiry : undefined,
        clear_expiry: clearExpiry,
        max_clicks: maxClicks ? parseInt(maxClicks) : undefined,
        clear_max_clicks: clearMaxClicks,
      });
      onUpdated(updated); onClose();
    } catch (err: any) { setError(err.message); }
    finally { setLoading(false); }
  }

  return (
    <Modal open={!!link} onClose={onClose} title="Edit Link">
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <Input label="Destination URL" type="text" value={dest} onChange={(e) => setDest(e.target.value)} required />
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Input label="Tag" value={tag} onChange={(e) => setTag(e.target.value)} placeholder="campaign…" />
          <div />
        </div>
        <Textarea label="Notes (internal)" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Private notes…" />

        {/* Password */}
        <div className="space-y-2">
          {link?.has_password && (
            <label className="flex items-center gap-2 text-sm cursor-pointer select-none">
              <input type="checkbox" checked={clearPassword} onChange={(e) => setClearPassword(e.target.checked)} className="rounded" />
              Remove existing password
            </label>
          )}
          {!clearPassword && (
            <Input label={link?.has_password ? "Replace password" : "Password (optional)"} type="password"
              value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Leave blank to keep" />
          )}
        </div>

        {/* Expiry */}
        <div className="space-y-2">
          {link?.expires_at && (
            <label className="flex items-center gap-2 text-sm cursor-pointer select-none">
              <input type="checkbox" checked={clearExpiry} onChange={(e) => setClearExpiry(e.target.checked)} className="rounded" />
              Remove expiry date
            </label>
          )}
          {!clearExpiry && (
            <div className="space-y-2">
              <div className="flex flex-wrap gap-1.5">
                {(["","1h","6h","24h","7d","30d"] as const).map((p) => (
                  <button key={p} type="button" onClick={() => setExpiresIn(p)}
                    className={`px-2.5 py-1 text-xs rounded-lg border font-medium transition-all ${
                      expiresIn === p ? "border-primary bg-primary/10 text-primary" : "border-border text-muted-foreground hover:text-foreground"}`}>
                    {p === "" ? "Keep current" : `+${p}`}
                  </button>
                ))}
              </div>
              {expiresIn === "" && (
                <Input label="Or set exact date" type="date" value={customExpiry} onChange={(e) => setCustomExpiry(e.target.value)} />
              )}
            </div>
          )}
        </div>

        {/* Max clicks */}
        <div className="space-y-2">
          {link?.max_clicks && (
            <label className="flex items-center gap-2 text-sm cursor-pointer select-none">
              <input type="checkbox" checked={clearMaxClicks} onChange={(e) => setClearMaxClicks(e.target.checked)} className="rounded" />
              Remove click limit (current: {link.max_clicks})
            </label>
          )}
          {!clearMaxClicks && (
            <Input label="Max clicks" type="number" min="1" value={maxClicks}
              onChange={(e) => setMaxClicks(e.target.value)} placeholder="Leave blank to keep" />
          )}
        </div>

        {error && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{error}</p>}

        <div className="flex gap-2 pt-1">
          <Button type="button" variant="secondary" className="flex-1" onClick={onClose}>Cancel</Button>
          <Button type="submit" className="flex-1" loading={loading}>Save Changes</Button>
        </div>
      </form>
    </Modal>
  );
}
