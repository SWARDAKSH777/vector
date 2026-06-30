import React, { useEffect, useState } from "react";
import { Download, Plus, QrCode } from "lucide-react";
import { api, type Domain, type Link } from "../lib/api";
import { AppShell } from "../components/AppShell";
import { CreateLinkDialog } from "../components/CreateLinkDialog";
import { Button, Spinner, EmptyState, Card } from "../components/ui";
import { timeAgo, formatNumber } from "../lib/utils";

export function QRPage() {
  const [links, setLinks] = useState<Link[]>([]);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all([api.listLinks(), api.listDomains()])
      .then(([items, domainItems]) => { setLinks(items); setDomains(domainItems); })
      .catch((reason: unknown) => setError(reason instanceof Error ? reason.message : "Could not load QR codes"))
      .finally(() => setLoading(false));
  }, []);

  function downloadQR(link: Link) {
    const a = document.createElement("a");
    a.href = api.qrCodeURL(link.id);
    a.download = `${link.short_code}.png`;
    document.body.appendChild(a); a.click(); document.body.removeChild(a);
  }

  return (
    <AppShell>
      <div className="p-4 sm:p-7">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-xl sm:text-2xl font-bold">QR Codes</h1>
            <p className="text-sm text-muted-foreground mt-0.5 hidden sm:block">Download QR codes for any link</p>
          </div>
          <Button onClick={() => setCreateOpen(true)} size="sm" className="sm:h-9 sm:px-4">
            <Plus className="w-4 h-4" /><span className="hidden sm:inline ml-1">Create Link</span>
          </Button>
        </div>

        {error && <div className="mb-4 rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">{error}</div>}

        {loading ? (
          <div className="flex justify-center py-20"><Spinner /></div>
        ) : links.length === 0 ? (
          <EmptyState icon={<QrCode className="w-6 h-6" />} title="No links yet"
            description="Create a link to generate a QR code"
            action={<Button onClick={() => setCreateOpen(true)}><Plus className="w-3.5 h-3.5" /> Create Link</Button>} />
        ) : (
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 gap-3 sm:gap-4">
            {links.map((link) => (
              <Card key={link.id} className="flex flex-col items-center gap-3 p-3 sm:p-4">
                <div className="w-full aspect-square rounded-lg overflow-hidden border border-border bg-white flex items-center justify-center">
                  <img src={api.qrCodeURL(link.id)} alt={`QR for ${link.short_url}`} className="w-full h-full object-contain p-1" />
                </div>
                <div className="text-center min-w-0 w-full">
                  <p className="text-xs font-mono font-medium text-primary truncate">
                    {link.short_url.replace(/^https?:\/\//, "")}
                  </p>
                  <p className="text-[10px] text-muted-foreground truncate mt-0.5">{link.destination_url}</p>
                  <p className="text-[10px] text-muted-foreground mt-0.5">{formatNumber(link.click_count)} clicks · {timeAgo(link.created_at)}</p>
                </div>
                <Button variant="secondary" size="sm" className="w-full text-xs" onClick={() => downloadQR(link)}>
                  <Download className="w-3 h-3" /> Download
                </Button>
              </Card>
            ))}
          </div>
        )}
      </div>

      <CreateLinkDialog open={createOpen} onClose={() => setCreateOpen(false)}
        onCreated={(l) => { setLinks((ls) => [l, ...ls]); setCreateOpen(false); }} domains={domains} />
    </AppShell>
  );
}
