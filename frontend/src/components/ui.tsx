import React from "react";
import { createPortal } from "react-dom";
import { cn } from "../lib/utils";

// ---- Button ----
interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost" | "destructive" | "outline";
  size?: "sm" | "md" | "lg";
  loading?: boolean;
}
export function Button({ variant = "primary", size = "md", loading, className, children, disabled, ...props }: ButtonProps) {
  const base = "inline-flex max-w-full min-w-0 items-center justify-center gap-2 font-medium rounded-lg transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 disabled:pointer-events-none select-none";
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
  return <div className={cn("min-w-0 max-w-full rounded-xl border border-border bg-card p-4 sm:p-5 shadow-sm", className)}>{children}</div>;
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
        "relative w-full max-w-[calc(100vw-1rem)] min-w-0 bg-card shadow-2xl border border-border animate-slide-up",
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
interface SelectProps extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, "children"> {
  label?: React.ReactNode;
  children: React.ReactNode;
}

interface ParsedSelectOption {
  value: string;
  label: React.ReactNode;
  disabled: boolean;
}

export function Select({
  label,
  className,
  children,
  id,
  value,
  defaultValue,
  onChange,
  disabled,
  name,
}: SelectProps) {
  const generatedID = React.useId();
  const selectID = id ?? generatedID;
  const listboxID = `${selectID}-listbox`;
  const buttonRef = React.useRef<HTMLButtonElement>(null);
  const menuRef = React.useRef<HTMLDivElement>(null);
  const [open, setOpen] = React.useState(false);
  const [internalValue, setInternalValue] = React.useState(
    defaultValue == null ? "" : String(defaultValue),
  );
  const [activeIndex, setActiveIndex] = React.useState(0);
  const [position, setPosition] = React.useState({
    top: 0,
    left: 0,
    width: 240,
    maxHeight: 280,
  });

  const options = React.useMemo<ParsedSelectOption[]>(() => {
    return React.Children.toArray(children).flatMap((child) => {
      if (
        !React.isValidElement<
          React.OptionHTMLAttributes<HTMLOptionElement>
        >(child) ||
        child.type !== "option"
      ) {
        return [];
      }

      return [{
        value: child.props.value == null ? "" : String(child.props.value),
        label: child.props.children,
        disabled: Boolean(child.props.disabled),
      }];
    });
  }, [children]);

  const selectedValue = value === undefined ? internalValue : String(value);
  const selectedIndex = Math.max(
    0,
    options.findIndex((option) => option.value === selectedValue),
  );
  const selected = options[selectedIndex];

  const updatePosition = React.useCallback(() => {
    const trigger = buttonRef.current;
    if (!trigger) return;

    const rect = trigger.getBoundingClientRect();
    const viewportPadding = 8;
    const below = window.innerHeight - rect.bottom - viewportPadding;
    const above = rect.top - viewportPadding;
    const openUpward = below < 180 && above > below;
    const maxHeight = Math.max(140, Math.min(320, openUpward ? above : below));
    const width = Math.min(
      Math.max(rect.width, 180),
      window.innerWidth - viewportPadding * 2,
    );
    const left = Math.min(
      Math.max(viewportPadding, rect.left),
      window.innerWidth - width - viewportPadding,
    );

    setPosition({
      top: openUpward
        ? Math.max(viewportPadding, rect.top - maxHeight - 6)
        : rect.bottom + 6,
      left,
      width,
      maxHeight,
    });
  }, []);

  React.useLayoutEffect(() => {
    if (!open) return;
    updatePosition();

    const onViewportChange = () => updatePosition();
    window.addEventListener("resize", onViewportChange);
    window.addEventListener("scroll", onViewportChange, true);

    return () => {
      window.removeEventListener("resize", onViewportChange);
      window.removeEventListener("scroll", onViewportChange, true);
    };
  }, [open, updatePosition]);

  React.useEffect(() => {
    if (!open) return;

    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (
        !buttonRef.current?.contains(target) &&
        !menuRef.current?.contains(target)
      ) {
        setOpen(false);
      }
    };

    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  function firstEnabledIndex(direction = 1, from = selectedIndex) {
    if (options.length === 0) return 0;

    let index = from;
    for (let checked = 0; checked < options.length; checked += 1) {
      index = (index + direction + options.length) % options.length;
      if (!options[index]?.disabled) return index;
    }
    return selectedIndex;
  }

  function choose(option: ParsedSelectOption) {
    if (option.disabled) return;

    if (value === undefined) {
      setInternalValue(option.value);
    }

    const target = { value: option.value } as HTMLSelectElement;
    onChange?.({
      target,
      currentTarget: target,
    } as React.ChangeEvent<HTMLSelectElement>);

    setOpen(false);
    requestAnimationFrame(() => buttonRef.current?.focus());
  }

  function openMenu() {
    if (disabled || options.length === 0) return;
    setActiveIndex(selectedIndex);
    setOpen(true);
  }

  function onButtonKeyDown(event: React.KeyboardEvent<HTMLButtonElement>) {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (!open) openMenu();
      else setActiveIndex((index) => firstEnabledIndex(1, index));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) openMenu();
      else setActiveIndex((index) => firstEnabledIndex(-1, index));
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (!open) openMenu();
      else if (options[activeIndex]) choose(options[activeIndex]);
    } else if (event.key === "Escape") {
      setOpen(false);
    } else if (event.key === "Home" && open) {
      event.preventDefault();
      setActiveIndex(firstEnabledIndex(1, -1));
    } else if (event.key === "End" && open) {
      event.preventDefault();
      setActiveIndex(firstEnabledIndex(-1, 0));
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {label && (
        <label htmlFor={selectID} className="text-xs font-medium text-foreground">
          {label}
        </label>
      )}

      {name && <input type="hidden" name={name} value={selectedValue} />}

      <button
        ref={buttonRef}
        id={selectID}
        type="button"
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listboxID}
        disabled={disabled}
        onClick={() => (open ? setOpen(false) : openMenu())}
        onKeyDown={onButtonKeyDown}
        className={cn(
          "flex h-10 w-full min-w-0 items-center justify-between gap-3 rounded-lg border border-input bg-background px-3 text-left text-sm text-foreground shadow-sm transition-colors",
          "hover:border-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background",
          "disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
      >
        <span className="min-w-0 flex-1 truncate">
          {selected?.label ?? "Select"}
        </span>
        <svg
          aria-hidden="true"
          viewBox="0 0 20 20"
          fill="none"
          className={cn(
            "h-4 w-4 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        >
          <path
            d="m6 8 4 4 4-4"
            stroke="currentColor"
            strokeWidth="1.75"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>

      {open &&
        typeof document !== "undefined" &&
        createPortal(
          <div
            ref={menuRef}
            id={listboxID}
            role="listbox"
            aria-labelledby={selectID}
            tabIndex={-1}
            style={{
              position: "fixed",
              top: position.top,
              left: position.left,
              width: position.width,
              maxHeight: position.maxHeight,
            }}
            className="z-[100] overflow-y-auto overscroll-contain rounded-xl border border-border bg-popover p-1.5 text-popover-foreground shadow-2xl"
          >
            {options.map((option, index) => (
              <button
                key={`${option.value}-${index}`}
                type="button"
                role="option"
                aria-selected={option.value === selectedValue}
                disabled={option.disabled}
                onMouseEnter={() => {
                  if (!option.disabled) setActiveIndex(index);
                }}
                onClick={() => choose(option)}
                className={cn(
                  "flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm transition-colors",
                  option.disabled && "cursor-not-allowed opacity-40",
                  !option.disabled &&
                    index === activeIndex &&
                    "bg-accent text-accent-foreground",
                  option.value === selectedValue &&
                    "font-medium text-primary",
                )}
              >
                <span className="min-w-0 flex-1 truncate">{option.label}</span>
                {option.value === selectedValue && (
                  <svg
                    aria-hidden="true"
                    viewBox="0 0 20 20"
                    fill="none"
                    className="h-4 w-4 shrink-0"
                  >
                    <path
                      d="m4.5 10 3.25 3.25L15.5 5.5"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                )}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}

// ---- Checkbox ----
interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type" | "onChange"> {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: React.ReactNode;
  description?: React.ReactNode;
}

export function Checkbox({
  checked,
  onChange,
  label,
  description,
  className,
  disabled,
  id,
  ...props
}: CheckboxProps) {
  const generatedID = React.useId();
  const checkboxID = id ?? generatedID;

  return (
    <label
      htmlFor={checkboxID}
      className={cn(
        "group flex items-start gap-3 rounded-lg border border-border bg-muted/20 px-3 py-2.5 transition-colors",
        disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer hover:border-primary/40 hover:bg-accent/40",
        className,
      )}
    >
      <input
        {...props}
        id={checkboxID}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="peer sr-only"
      />
      <span
        aria-hidden="true"
        className={cn(
          "mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-md border shadow-sm transition-all",
          "peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background",
          checked
            ? "border-primary bg-primary text-primary-foreground"
            : "border-input bg-background text-transparent group-hover:border-primary/60",
        )}
      >
        <svg viewBox="0 0 20 20" fill="none" className="h-3.5 w-3.5">
          <path d="m4.5 10 3.25 3.25L15.5 5.5" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </span>
      <span className="min-w-0">
        <span className="block text-sm font-medium text-foreground">{label}</span>
        {description && <span className="mt-0.5 block text-xs text-muted-foreground">{description}</span>}
      </span>
    </label>
  );
}

// ---- Toggle ----
export function Toggle({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (value: boolean) => void;
  label?: React.ReactNode;
}) {
  const id = React.useId();

  return (
    <label htmlFor={id} className="flex cursor-pointer select-none items-center gap-2.5">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
        className="peer sr-only"
      />
      <span
        aria-hidden="true"
        className={cn(
          "relative h-6 w-11 shrink-0 rounded-full border shadow-inner transition-colors",
          "peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background",
          checked ? "border-primary bg-primary" : "border-border bg-muted-foreground/25",
        )}
      >
        <span
          className={cn(
            "absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform",
            checked ? "translate-x-6" : "translate-x-0.5",
          )}
        />
      </span>
      {label && <span className="text-sm text-muted-foreground">{label}</span>}
    </label>
  );
}
