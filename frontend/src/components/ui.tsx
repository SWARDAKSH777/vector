import React from "react";
import { cn } from "../lib/utils";

// ---- Button ----
interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost" | "destructive" | "outline";
  size?: "sm" | "md" | "lg";
  loading?: boolean;
}
export function Button({ variant = "primary", size = "md", loading, className, children, disabled, ...props }: ButtonProps) {
  const base = "inline-flex items-center justify-center gap-2 font-medium rounded-lg transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 disabled:pointer-events-none select-none";
  const variants = {
    primary: "bg-primary text-primary-foreground hover:opacity-90 shadow-sm",
    secondary: "bg-secondary text-secondary-foreground hover:bg-accent border border-border",
    ghost: "hover:bg-accent text-foreground",
    destructive: "bg-destructive text-destructive-foreground hover:opacity-90",
    outline: "border border-border text-foreground hover:bg-accent",
  };
  const sizes = { sm: "px-3 py-1.5 text-xs h-8", md: "px-4 py-2 text-sm h-9", lg: "px-5 py-2.5 text-sm h-10" };
  return (
    <button {...props} disabled={disabled || loading} className={cn(base, variants[variant], sizes[size], className)}>
      {loading && <svg className="animate-spin h-3.5 w-3.5 shrink-0" fill="none" viewBox="0 0 24 24">
        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
      </svg>}
      {children}
    </button>
  );
}

// ---- Input ----
interface InputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "prefix"> {
  label?: string;
  error?: string;
  hint?: string;
  prefix?: React.ReactNode;
  suffix?: React.ReactNode;
}
export function Input({ label, error, hint, prefix, suffix, className, ...props }: InputProps) {
  return (
    <div className="flex flex-col gap-1.5">
      {label && <label className="text-xs font-medium text-foreground">{label}</label>}
      <div className="relative flex items-center">
        {prefix && <span className="absolute left-3 text-muted-foreground text-sm select-none pointer-events-none">{prefix}</span>}
        <input {...props} className={cn(
          "flex h-9 w-full rounded-lg border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 transition-shadow",
          Boolean(prefix) && "pl-8", Boolean(suffix) && "pr-9",
          Boolean(error) && "border-destructive focus-visible:ring-destructive",
          className
        )} />
        {suffix && <span className="absolute right-3 text-muted-foreground text-sm">{suffix}</span>}
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
      {hint && !error && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

// ---- Textarea ----
interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  label?: string;
  error?: string;
}
export function Textarea({ label, error, className, ...props }: TextareaProps) {
  return (
    <div className="flex flex-col gap-1.5">
      {label && <label className="text-xs font-medium text-foreground">{label}</label>}
      <textarea {...props} className={cn(
        "flex min-h-[80px] w-full rounded-lg border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 resize-none",
        error && "border-destructive",
        className
      )} />
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

// ---- Badge ----
interface BadgeProps { children: React.ReactNode; variant?: "default" | "success" | "warning" | "destructive" | "outline"; className?: string; }
export function Badge({ children, variant = "default", className }: BadgeProps) {
  const variants = {
    default: "bg-secondary text-secondary-foreground",
    success: "bg-success/15 text-success",
    warning: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
    destructive: "bg-destructive/15 text-destructive",
    outline: "border border-border text-muted-foreground",
  };
  return <span className={cn("inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium", variants[variant], className)}>{children}</span>;
}

// ---- Card ----
export function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("rounded-xl border border-border bg-card p-4 sm:p-5 shadow-sm", className)}>{children}</div>;
}

// ---- Modal (bottom sheet on mobile, centered on desktop) ----
interface ModalProps { open: boolean; onClose: () => void; title?: string; children: React.ReactNode; maxWidth?: string; }
export function Modal({ open, onClose, title, children, maxWidth = "sm:max-w-lg" }: ModalProps) {
  React.useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    // Prevent body scroll when modal open
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
      <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" onClick={onClose} />
      <div className={cn(
        "relative w-full bg-card shadow-2xl border border-border animate-slide-up",
        "rounded-t-2xl sm:rounded-2xl",
        "max-h-[92dvh] sm:max-h-[90dvh] overflow-y-auto",
        "sm:mx-4",
        maxWidth
      )}>
        {/* Mobile drag handle */}
        <div className="sm:hidden flex justify-center pt-3 pb-1">
          <div className="w-10 h-1 bg-border rounded-full" />
        </div>
        {title && (
          <div className="flex items-center justify-between px-5 pt-4 sm:pt-5 pb-4 border-b border-border sticky top-0 bg-card z-10">
            <h2 className="text-base font-semibold">{title}</h2>
            <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors p-1 rounded-md hover:bg-accent">
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        )}
        <div className="px-5 py-4 sm:py-5">{children}</div>
      </div>
    </div>
  );
}

// ---- Spinner ----
export function Spinner({ className }: { className?: string }) {
  return <svg className={cn("animate-spin h-5 w-5 text-muted-foreground", className)} fill="none" viewBox="0 0 24 24">
    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
  </svg>;
}

// ---- Empty state ----
export function EmptyState({ icon, title, description, action }: {
  icon: React.ReactNode; title: string; description?: string; action?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center py-16 gap-4 text-center px-4">
      <div className="w-14 h-14 rounded-full bg-muted flex items-center justify-center text-muted-foreground">{icon}</div>
      <div><p className="font-medium">{title}</p>{description && <p className="text-sm text-muted-foreground mt-1">{description}</p>}</div>
      {action}
    </div>
  );
}

// ---- Select ----
interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> { label?: string; }
export function Select({ label, className, children, ...props }: SelectProps) {
  return (
    <div className="flex flex-col gap-1.5">
      {label && <label className="text-xs font-medium text-foreground">{label}</label>}
      <select {...props} className={cn(
        "h-9 w-full rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50",
        className
      )}>{children}</select>
    </div>
  );
}

// ---- Toggle ----
export function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <label className="flex items-center gap-2.5 cursor-pointer select-none">
      <div
        onClick={() => onChange(!checked)}
        className={cn(
          "relative w-9 h-5 rounded-full transition-colors shrink-0",
          checked ? "bg-primary" : "bg-muted-foreground/30"
        )}
      >
        <div className={cn(
          "absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform",
          checked ? "translate-x-4" : "translate-x-0.5"
        )} />
      </div>
      {label && <span className="text-sm text-muted-foreground">{label}</span>}
    </label>
  );
}
