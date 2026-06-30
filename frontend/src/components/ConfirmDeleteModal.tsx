import React, { useState } from "react";
import { Modal, Button, Input } from "./ui";
import { Trash2 } from "lucide-react";

interface Props {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  title?: string;
  description?: string;
  keyword?: string; // word user must type, default "DELETE"
  loading?: boolean;
}

export function ConfirmDeleteModal({
  open, onClose, onConfirm,
  title = "Delete this item?",
  description = "This action cannot be undone.",
  keyword = "DELETE",
  loading = false,
}: Props) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);

  async function handleConfirm() {
    setBusy(true);
    try { await onConfirm(); } finally { setBusy(false); setTyped(""); }
  }

  function handleClose() { setTyped(""); onClose(); }

  const matches = typed === keyword;

  return (
    <Modal open={open} onClose={handleClose} title={title} maxWidth="sm:max-w-md">
      <div className="flex flex-col gap-4">
        <div className="flex items-start gap-3 p-4 bg-destructive/10 border border-destructive/20 rounded-xl">
          <Trash2 className="w-5 h-5 text-destructive shrink-0 mt-0.5" />
          <p className="text-sm text-destructive leading-relaxed">{description}</p>
        </div>

        <div className="flex flex-col gap-1.5">
          <label className="text-xs font-medium text-muted-foreground">
            Type <span className="font-mono font-bold text-foreground">{keyword}</span> to confirm
          </label>
          <input
            type="text"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={keyword}
            autoFocus
            className="flex h-9 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm font-mono placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-destructive transition-shadow"
          />
        </div>

        <div className="flex gap-2">
          <Button type="button" variant="secondary" className="flex-1" onClick={handleClose} disabled={busy || loading}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            className="flex-1"
            disabled={!matches || busy || loading}
            loading={busy || loading}
            onClick={handleConfirm}
          >
            <Trash2 className="w-3.5 h-3.5" /> Delete
          </Button>
        </div>
      </div>
    </Modal>
  );
}
