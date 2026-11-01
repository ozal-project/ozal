import { useEffect, useMemo, useRef, useState } from "react";
import { Link, NavLink, Route, Routes, useLocation } from "react-router";
import { Button } from "$lib/components/ui/button/react";
import { Activity } from "./Activity";
import { Overview } from "./Overview";
import { Routes as RoutingPage } from "./Routes";
import { Domains } from "./Domains";
import { useSession } from "./session";

type Tool = "applications" | "people" | "administrators" | "sites" | "connectors" | "updates" | "updates.install" | "publishing";
type Item = { path: string; label: string; tool?: Tool; also?: string[] };
const groups: { name: string; items: Item[] }[] = [
  { name: "", items: [{ path: "/overview", label: "Overview" }] },
  { name: "Access", items: [
    { path: "/applications", label: "Applications", tool: "applications", also: ["/routes"] },
    { path: "/domains", label: "Domains", tool: "applications" },
    { path: "/people", label: "People", tool: "people", also: ["/groups", "/administrators"] },
  ] },
  { name: "Edge", items: [
    { path: "/sites", label: "Sites" }, { path: "/connectors", label: "Connectors", tool: "connectors" },
    { path: "/integrations", label: "Integrations" }, { path: "/activity", label: "Activity" },
  ] },
];
const purpose: Record<string, string> = {
  "/overview": "What needs you, and what changed", "/applications": "Destinations and who may enter", "/routes": "Destinations and who may enter",
  "/domains": "Domains and their certificates", "/people": "People, groups and administrators", "/administrators": "People, groups and administrators",
  "/sites": "Published versions and CI", "/connectors": "Trusted connections to private hosts", "/integrations": "GitHub, DNS and how sign-in works", "/activity": "Access decisions and their reasons",
};

export default function Console() {
  const { session, logout } = useSession();
  const location = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const drawer = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const drawerPath = useRef(location.pathname);
  const pageTitle = useMemo(() => [...groups.flatMap((group) => group.items), { path: "/routes", label: "Applications" }, { path: "/groups", label: "People" }, { path: "/administrators", label: "People" }].find((item) => item.path === location.pathname)?.label ?? "Overview", [location.pathname]);
  const can = (tool: Tool) => !!session?.tools.includes(tool);
  const visible = groups.map((group) => ({ ...group, items: group.items.filter((item) => !item.tool || can(item.tool) || item.path === "/applications" || item.path === "/people" && can("administrators")) })).filter((group) => group.items.length);
  const current = [...groups.flatMap((group) => group.items), { path: "/groups", label: "Groups", tool: "people" as const }, { path: "/administrators", label: "Administrators", tool: "administrators" as const }, { path: "/routes", label: "Applications", tool: "applications" as const }].find((item) => item.path === location.pathname) ?? groups[0].items[0];
  const target = (item: Item) => item.path === "/applications" && !can("applications") ? "/routes" : item.path === "/people" && !can("people") && can("administrators") ? "/administrators" : item.path;
  const blocked = current.path !== "/routes" && !!current.tool && !can(current.tool);

  useEffect(() => {
    document.title = `${pageTitle} · Ozal`;
    const frame = requestAnimationFrame(() => document.querySelector<HTMLElement>("#main-content h1")?.focus());
    return () => cancelAnimationFrame(frame);
  }, [pageTitle, location.pathname]);
  useEffect(() => {
    const dialog = drawer.current;
    if (!dialog) return;
    if (menuOpen && !dialog.open) {
      drawerPath.current = location.pathname;
      dialog.showModal();
      requestAnimationFrame(() => dialog.querySelector<HTMLElement>("[aria-current='page']")?.focus());
    } else if (!menuOpen && dialog.open) dialog.close();
  }, [menuOpen, location.pathname]);
  const closeDrawer = () => {
    setMenuOpen(false);
    window.setTimeout(() => (drawerPath.current === location.pathname ? trigger.current?.focus() : document.querySelector<HTMLElement>("#main-content h1")?.focus()), 0);
  };

  return <div className="console-workspace">
    <a href="#main-content" className="sr-only focus:not-sr-only focus:bg-background focus:text-foreground focus:ring-ring fixed top-2 left-2 z-50 rounded-md px-3 py-2 text-sm focus:ring-2">Skip to content</a>
    <header className="console-header"><div className="console-bar">
      <Link to="/overview" className="console-brand" aria-label="Ozal console home"><span className="text-lg font-medium tracking-tight">ozal</span><span className="text-muted-foreground ml-2 self-center text-xs">Private console</span></Link>
      <nav className="console-nav" aria-label="Console">{visible.flatMap((group) => group.items).map((item) => <NavLink key={item.path} to={target(item)} className="console-nav-link" aria-current={[item.path, ...(item.also ?? [])].includes(location.pathname) ? "page" : undefined}>{item.label}</NavLink>)}</nav>
      <details className="console-account relative"><summary className="flex cursor-pointer list-none items-center gap-2 rounded-md px-2 py-2 text-sm hover:bg-muted"><span className="grid size-7 place-items-center rounded-full bg-muted text-xs">{(session?.name || "O").slice(0, 1).toUpperCase()}</span><span className="console-account-name max-w-32 truncate">{session?.name || "Operator"}</span></summary><div className="bg-popover border-border absolute top-full right-0 z-40 mt-2 grid w-64 gap-3 rounded-lg border p-4 shadow-xl"><div><p className="text-sm font-medium">{session?.name || "Operator"}</p><p className="text-muted-foreground mt-1 text-xs">This session can use:</p><ul className="text-muted-foreground mt-2 grid gap-1 text-xs">{session?.tools.map((tool) => <li key={tool}>{tool.replaceAll(".", " ")}</li>)}</ul></div><Button variant="outline" size="sm" onClick={logout}>Sign out</Button></div></details>
      <span className="console-mobile-place" aria-hidden="true">{pageTitle}</span><button ref={trigger} type="button" className="console-menu-toggle" aria-expanded={menuOpen} aria-controls="mobile-menu" aria-label="Open menu" onClick={() => setMenuOpen(true)}><span aria-hidden="true" className="grid gap-1"><i className="bg-current h-0.5 w-4" /><i className="bg-current h-0.5 w-4" /><i className="bg-current h-0.5 w-4" /></span></button>
    </div></header>

    <dialog ref={drawer} id="mobile-menu" aria-label="Console navigation" className="console-drawer" onClose={closeDrawer} onCancel={() => setMenuOpen(false)}>
      {menuOpen && <><div className="console-drawer-header"><Link to="/overview" className="text-lg font-medium" onClick={() => setMenuOpen(false)}>ozal</Link><button type="button" className="console-menu-toggle" aria-label="Close menu" onClick={() => setMenuOpen(false)}><span aria-hidden="true" className="relative block size-4 before:absolute before:left-0 before:top-1/2 before:h-0.5 before:w-4 before:-rotate-45 before:bg-current after:absolute after:left-0 after:top-1/2 after:h-0.5 after:w-4 after:rotate-45 after:bg-current" /></button></div><nav aria-label="Console">{visible.flatMap((group) => group.items).map((item) => <NavLink key={item.path} to={target(item)} className="console-drawer-link" aria-current={[item.path, ...(item.also ?? [])].includes(location.pathname) ? "page" : undefined} onClick={() => setMenuOpen(false)}><span>{item.label}</span><span className="console-nav-purpose">{purpose[target(item)]}</span></NavLink>)}</nav><div className="console-drawer-account"><p className="mb-3 text-sm">{session?.name || "Operator"}</p><Button variant="outline" onClick={logout}>Sign out</Button></div></>}
    </dialog>

    <main id="main-content" tabIndex={-1} className="console-content outline-none"><div className="page-enter">
      {blocked ? <div className="panel mx-auto max-w-lg p-8 text-center"><p className="plate">Not included in this session</p><h1 tabIndex={-1} className="mt-2 text-xl font-semibold">This session cannot open {current.label}</h1><p className="text-muted-foreground mt-2 text-sm">You are signed in as {session?.name || "an operator"} without the {current.tool} tool. Ask an administrator to include it.</p></div> : <Routes>
        <Route path="/overview" element={<Overview />} />
        <Route path="/" element={<Overview />} />
        <Route path="/activity" element={<Activity />} />
        <Route path="/routes" element={<RoutingPage />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="*" element={<MigrationPlaceholder title={pageTitle} />} />
      </Routes>}
    </div></main>
  </div>;
}

function MigrationPlaceholder({ title }: { title: string }) {
  return <header><p className="plate">UI-012 · Migration in progress</p><h1 tabIndex={-1} className="mt-1 text-2xl font-semibold tracking-tight">{title}</h1><p className="text-muted-foreground mt-2 text-sm">This React view is being brought over from the current console.</p></header>;
}
