import React, { useState, useEffect, useRef } from "react";
import { Modal, Button, Input } from "./ui";
import { AlertTriangle } from "lucide-react";

interface Props {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmWord?: string; // if set, user must type this word
  danger?: boolean;
  loading?: boolean;
}

export function ConfirmDialog({ open, onClose, onConfirm, title, description, confirmWord, danger = true, loading }: Props) {
  const [typed, setTyped] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setTyped("");
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [open]);

  const canConfirm = !confirmWord || typed === confirmWord;

  return (
    <Modal open={open} onClose={onClose} maxWidth="sm:max-w-md">
      <div className="flex flex-col gap-4">
        <div className="flex items-start gap-3">
          <div className={`w-10 h-10 rounded-full flex items-center justify-center shrink-0 ${danger ? "bg-destructive/10" : "bg-muted"}`}>
            <AlertTriangle className={`w-5 h-5 ${danger ? "text-destructive" : "text-muted-foreground"}`} />
          </div>
          <div>
            <h2 className="font-semibold text-base">{title}</h2>
            <p className="text-sm text-muted-foreground mt-1 leading-relaxed">{description}</p>
          </div>
        </div>

        {confirmWord && (
          <div className="flex flex-col gap-2">
            <p className="text-sm text-muted-foreground">
              Type <code className="bg-muted px-1.5 py-0.5 rounded text-xs font-mono font-bold text-foreground">{confirmWord}</code> to confirm:
            </p>
            <input
              ref={inputRef}
              type="text"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && canConfirm) onConfirm(); }}
              placeholder={confirmWord}
              className="h-9 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm font-mono focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              autoComplete="off"
            />
          </div>
        )}

        <div className="flex gap-2">
          <Button variant="secondary" className="flex-1" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button
            variant={danger ? "destructive" : "primary"}
            className="flex-1"
            onClick={onConfirm}
            disabled={!canConfirm}
            loading={loading}
          >
            {confirmWord ? `Delete` : "Confirm"}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
