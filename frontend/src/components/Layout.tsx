import { useState } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { FileText, LayoutDashboard, LogOut, Megaphone, Menu, Settings, Shield, UserCircle, Users, UsersRound } from "lucide-react";
import { useAuth } from "../auth";
import { cx } from "./ui";

type NavItem = { to: string; label: string; icon: typeof LayoutDashboard; end?: boolean };

const nav: NavItem[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/campaigns", label: "Campaigns", icon: Megaphone },
  { to: "/contacts", label: "Contacts", icon: Users },
  { to: "/groups", label: "Groups", icon: UsersRound },
  { to: "/templates", label: "Templates", icon: FileText },
];
const adminNav: NavItem[] = [
  { to: "/users", label: "Users", icon: Shield },
  { to: "/settings", label: "Settings", icon: Settings },
];

export default function Layout() {
  const { user, logout } = useAuth();
  const [open, setOpen] = useState(false);
  const items = user?.role === "admin" ? [...nav, ...adminNav] : nav;

  const sidebar = (
    <nav className="flex h-full flex-col">
      <div className="flex items-center gap-2 px-5 py-5">
        <img src="/favicon.svg" alt="" className="size-8" />
        <div>
          <p className="text-sm font-semibold text-white">WhatsApp Campaigns</p>
          <p className="text-xs text-emerald-100/70">powered by Bird</p>
        </div>
      </div>
      <div className="flex-1 space-y-0.5 px-3">
        {items.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            onClick={() => setOpen(false)}
            className={({ isActive }) =>
              cx(
                "flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
                isActive ? "bg-white/15 text-white" : "text-emerald-50/80 hover:bg-white/10 hover:text-white",
              )
            }
          >
            <Icon className="size-4" />
            {label}
          </NavLink>
        ))}
      </div>
      <div className="border-t border-white/10 p-3">
        <NavLink to="/account" onClick={() => setOpen(false)} className="flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-emerald-50/80 hover:bg-white/10 hover:text-white">
          <UserCircle className="size-4" />
          <span className="min-w-0 flex-1 truncate">{user?.name || user?.email}</span>
          <span className="rounded bg-white/15 px-1.5 text-[10px] uppercase">{user?.role}</span>
        </NavLink>
        <button onClick={logout} className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-emerald-50/80 hover:bg-white/10 hover:text-white">
          <LogOut className="size-4" />
          Log out
        </button>
      </div>
    </nav>
  );

  return (
    <div className="min-h-screen lg:pl-60">
      <aside className="fixed inset-y-0 left-0 hidden w-60 bg-brand-700 lg:block">{sidebar}</aside>
      {open && (
        <div className="fixed inset-0 z-40 lg:hidden" onClick={() => setOpen(false)}>
          <div className="absolute inset-0 bg-slate-900/40" />
          <aside className="absolute inset-y-0 left-0 w-64 bg-brand-700" onClick={(e) => e.stopPropagation()}>
            {sidebar}
          </aside>
        </div>
      )}
      <header className="sticky top-0 z-30 flex items-center gap-3 border-b border-slate-200 bg-white px-4 py-3 lg:hidden">
        <button onClick={() => setOpen(true)} aria-label="Open menu" className="rounded p-1 text-slate-600 hover:bg-slate-100">
          <Menu className="size-5" />
        </button>
        <span className="font-semibold">WhatsApp Campaigns</span>
      </header>
      <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
        <Outlet />
      </main>
    </div>
  );
}
