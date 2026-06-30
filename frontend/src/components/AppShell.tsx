import React, { useState } from "react";
import { Link, useLocation } from "react-router-dom";
import {
  LayoutDashboard, Link2, BarChart2, QrCode, Globe,
  Settings, Bell, LogOut, ChevronDown, Menu, X,
} from "lucide-react";
import { useAuth } from "../lib/auth";
import { cn } from "../lib/utils";

const NAV = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard },
  { to: "/links", label: "Links", icon: Link2 },
  { to: "/analytics", label: "Analytics", icon: BarChart2 },
  { to: "/qr", label: "QR Codes", icon: QrCode },
  { to: "/domains", label: "Domains", icon: Globe },
  { to: "/settings", label: "Settings", icon: Settings },
];

interface AppShellProps {
  children: React.ReactNode;
  search?: string;
  onSearch?: (q: string) => void;
}

export function AppShell({ children, search, onSearch }: AppShellProps) {
  const { pathname } = useLocation();
  const { user, logout } = useAuth();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);

  const isActive = (to: string) => to === "/" ? pathname === "/" : pathname.startsWith(to);

  return (
    <div className="flex h-[100dvh] bg-background overflow-hidden">
      {/* Mobile sidebar overlay */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/50 lg:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside className={cn(
        "fixed inset-y-0 left-0 z-50 w-56 flex flex-col border-r border-border bg-card transition-transform duration-200 lg:static lg:translate-x-0 lg:z-auto",
        sidebarOpen ? "translate-x-0" : "-translate-x-full"
      )}>
        {/* Logo */}
        <div className="flex items-center justify-between px-4 h-14 border-b border-border shrink-0">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-lg bg-primary flex items-center justify-center">
              <svg className="w-4 h-4 text-primary-foreground" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                <path d="M13 5l7 7-7 7M5 12h15" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </div>
            <span className="font-bold text-base tracking-tight">VECTOR</span>
          </div>
          <button className="lg:hidden p-1 text-muted-foreground" onClick={() => setSidebarOpen(false)}>
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Nav */}
        <nav className="flex-1 py-3 px-2 space-y-0.5 overflow-y-auto">
          {NAV.map(({ to, label, icon: Icon }) => (
            <Link
              key={to}
              to={to}
              onClick={() => setSidebarOpen(false)}
              className={cn(
                "flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all",
                isActive(to)
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:text-foreground hover:bg-accent"
              )}
            >
              <Icon className="w-4 h-4 shrink-0" />
              {label}
            </Link>
          ))}
        </nav>

        <div className="px-4 py-4 border-t border-border shrink-0">
          <p className="text-xs text-muted-foreground mb-1.5">USAGE</p>
          <div className="h-1 bg-muted rounded-full overflow-hidden mb-1">
            <div className="h-full bg-primary rounded-full w-1/2" />
          </div>
          <p className="text-xs text-muted-foreground">Self-hosted · Unlimited</p>
        </div>
      </aside>

      {/* Main */}
      <div className="flex-1 flex flex-col overflow-hidden min-w-0">
        {/* Topbar */}
        <header className="h-14 border-b border-border bg-card flex items-center gap-3 px-4 shrink-0">
          <button
            className="lg:hidden p-2 text-muted-foreground hover:text-foreground hover:bg-accent rounded-lg transition-colors"
            onClick={() => setSidebarOpen(true)}
          >
            <Menu className="w-4 h-4" />
          </button>

          {/* Search */}
          <div className="flex-1 relative">
            <svg className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground pointer-events-none"
              fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <input
              type="text"
              placeholder="Search links..."
              value={search ?? ""}
              onChange={(e) => onSearch?.(e.target.value)}
              className="w-full pl-8 pr-4 h-9 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring placeholder:text-muted-foreground"
            />
          </div>

          <button className="p-2 text-muted-foreground hover:text-foreground hover:bg-accent rounded-lg transition-colors hidden sm:flex">
            <Bell className="w-4 h-4" />
          </button>

          {/* User menu */}
          <div className="relative shrink-0">
            <button
              onClick={() => setUserMenuOpen((o) => !o)}
              className="flex items-center gap-2 rounded-lg hover:bg-accent px-2 py-1.5 transition-colors"
            >
              <div className="w-7 h-7 rounded-full bg-primary flex items-center justify-center text-primary-foreground text-xs font-semibold">
                {user?.email?.[0]?.toUpperCase() ?? "?"}
              </div>
              <span className="text-sm font-medium hidden sm:block max-w-[100px] truncate">{user?.email?.split("@")[0]}</span>
              <ChevronDown className="w-3.5 h-3.5 text-muted-foreground hidden sm:block" />
            </button>
            {userMenuOpen && (
              <>
                <div className="fixed inset-0 z-10" onClick={() => setUserMenuOpen(false)} />
                <div className="absolute right-0 top-full mt-1 z-20 w-52 rounded-xl border border-border bg-card shadow-lg py-1">
                  <div className="px-3 py-2 border-b border-border mb-1">
                    <p className="text-xs font-medium truncate">{user?.email}</p>
                    <p className="text-xs text-muted-foreground">Administrator</p>
                  </div>
                  <Link to="/settings" onClick={() => setUserMenuOpen(false)}
                    className="flex items-center gap-2 w-full text-left px-3 py-2 text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
                    <Settings className="w-3.5 h-3.5" /> Settings
                  </Link>
                  <button
                    onClick={() => { setUserMenuOpen(false); logout(); }}
                    className="flex items-center gap-2 w-full text-left px-3 py-2 text-sm text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                  >
                    <LogOut className="w-3.5 h-3.5" /> Log out
                  </button>
                </div>
              </>
            )}
          </div>
        </header>

        {/* Page */}
        <main className="flex-1 overflow-y-auto">{children}</main>

        {/* Mobile bottom nav */}
        <nav className="lg:hidden border-t border-border bg-card flex items-center justify-around px-2 py-1 shrink-0">
          {NAV.slice(0, 5).map(({ to, label, icon: Icon }) => (
            <Link
              key={to}
              to={to}
              className={cn(
                "flex flex-col items-center gap-0.5 px-2 py-1.5 rounded-lg text-xs font-medium transition-all min-w-0",
                isActive(to) ? "text-primary" : "text-muted-foreground"
              )}
            >
              <Icon className="w-5 h-5" />
              <span className="text-[10px] truncate">{label}</span>
            </Link>
          ))}
        </nav>
      </div>
    </div>
  );
}
