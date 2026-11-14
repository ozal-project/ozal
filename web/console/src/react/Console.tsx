import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, Navigate, NavLink, Route, Routes, useLocation } from "react-router";
import { Button } from "$lib/components/ui/button/react";
import { fetchUpdateStatus } from "$lib/api";
import { availableUpdateNotice, type AvailableUpdateNotice } from "$lib/updateStatus";
import { Activity } from "./Activity";
import { Overview } from "./Overview";
import { Routes as RoutingPage } from "./Routes";
import { Domains } from "./Domains";
import { Integrations } from "./Integrations";
import { Applications } from "./Applications";
import { Sites } from "./Sites";
import { Administrators } from "./Administrators";
import { Groups } from "./Groups";
import { People } from "./People";
import { Connectors } from "./Connectors";
import { UpdateDialog } from "./UpdateDialog";
import { useSession } from "./session";
import { UserMenu } from "./UserMenu";
import { Wordmark } from "./Wordmark";

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
  const { session } = useSession();
  const location = useLocation();
  const [updateNotice, setUpdateNotice] = useState<AvailableUpdateNotice | null>(null);
  const [updateDialogOpen, setUpdateDialogOpen] = useState(false);
  const dismissedVersion = useRef("");
  const updateNoticeRef = useRef(updateNotice);
  updateNoticeRef.current = updateNotice;
  const updateRequest = useRef<AbortController | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const drawer = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const drawerPath = useRef(location.pathname);
  const pageTitle = useMemo(() => [...groups.flatMap((group) => group.items), { path: "/routes", label: "Applications" }, { path: "/groups", label: "Groups" }, { path: "/administrators", label: "Administrators" }].find((item) => item.path === location.pathname)?.label ?? "Overview", [location.pathname]);
  const can = (tool: Tool) => !!session?.tools.includes(tool);
  const visible = groups.map((group) => ({ ...group, items: group.items.filter((item) => !item.tool || can(item.tool) || item.path === "/applications" || item.path === "/people" && can("administrators")) })).filter((group) => group.items.length);
  const current = [...groups.flatMap((group) => group.items), { path: "/groups", label: "Groups", tool: "people" as const }, { path: "/administrators", label: "Administrators", tool: "administrators" as const }, { path: "/routes", label: "Applications", tool: "applications" as const }].find((item) => item.path === location.pathname) ?? groups[0].items[0];
  const target = (item: Item) => item.path === "/applications" && !can("applications") ? "/routes" : item.path === "/people" && !can("people") && can("administrators") ? "/administrators" : item.path;
  const blocked = current.path !== "/routes" && !!current.tool && !can(current.tool);

  const checkUpdateNotice = useCallback(async (signal: AbortSignal) => {
    if (!can("updates")) { setUpdateNotice(null); return; }
    try {
      const status = await fetchUpdateStatus(signal);
      if (signal.aborted) return;
      const notice = availableUpdateNotice(status);
      setUpdateNotice(notice && notice.version !== dismissedVersion.current ? notice : null);
    } catch {
      // Release status is optional and must not interrupt console access.
    }
  }, [session?.token, session?.tools]);
  const completeUpdate = useCallback(() => {
    dismissedVersion.current = updateNoticeRef.current?.version ?? "";
    setUpdateNotice(null);
  }, []);

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
  useEffect(() => {
    const controller = new AbortController();
    void checkUpdateNotice(controller.signal);
    const poll = window.setInterval(() => {
      updateRequest.current?.abort();
      const next = new AbortController();
      void checkUpdateNotice(next.signal);
      updateRequest.current = next;
    }, 5 * 60_000);
    updateRequest.current = controller;
    return () => {
      window.clearInterval(poll);
      updateRequest.current?.abort();
      updateRequest.current = null;
    };
  }, [checkUpdateNotice]);
  const closeDrawer = () => {
    setMenuOpen(false);
    window.setTimeout(() => (drawerPath.current === location.pathname ? trigger.current?.focus() : document.querySelector<HTMLElement>("#main-content h1")?.focus()), 0);
  };
  const containDrawerTab = (event: React.KeyboardEvent<HTMLDialogElement>) => {
    if (event.key !== "Tab") return;
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), input:not(:disabled), [tabindex]:not([tabindex="-1"])')].filter((element) => element.getClientRects().length > 0);
    if (!controls.length) return;
    const first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };

  return <>
  <div className="console-workspace">
    <a href="#main-content" className="sr-only focus:not-sr-only focus:bg-background focus:text-foreground focus:ring-ring fixed top-2 left-2 z-50 rounded-md px-3 py-2 text-sm focus:ring-2">Skip to content</a>
    <header className="console-header"><div className="console-bar">
      <Link to="/overview" className="console-brand" aria-label="Ozal console home"><Wordmark sub="Private console" animated /></Link>
      <nav className="console-nav" aria-label="Console">{visible.flatMap((group) => group.items).map((item) => <NavLink key={item.path} to={target(item)} className="console-nav-link" aria-current={[item.path, ...(item.also ?? [])].includes(location.pathname) ? "page" : undefined}>{item.label}</NavLink>)}</nav>
      <UserMenu />
      <span className="console-mobile-place" aria-hidden="true">{pageTitle}</span><button ref={trigger} type="button" className="console-menu-toggle" aria-expanded={menuOpen} aria-controls="mobile-menu" aria-label="Open menu" onClick={() => setMenuOpen(true)}><span aria-hidden="true" className="grid gap-1"><i className="bg-current h-0.5 w-4" /><i className="bg-current h-0.5 w-4" /><i className="bg-current h-0.5 w-4" /></span></button>
    </div></header>

    <dialog ref={drawer} id="mobile-menu" aria-label="Console navigation" className="console-drawer" onClose={closeDrawer} onCancel={() => setMenuOpen(false)} onKeyDown={containDrawerTab}>
      {menuOpen && <><div className="console-drawer-header"><Link to="/overview" onClick={() => setMenuOpen(false)}><Wordmark sub="Private console" /></Link><button type="button" className="console-menu-toggle" aria-label="Close menu" onClick={() => setMenuOpen(false)}><span aria-hidden="true" className="relative block size-4 before:absolute before:left-0 before:top-1/2 before:h-0.5 before:w-4 before:-rotate-45 before:bg-current after:absolute after:left-0 after:top-1/2 after:h-0.5 after:w-4 after:rotate-45 after:bg-current" /></button></div><nav aria-label="Console">{visible.flatMap((group) => group.items).map((item) => <NavLink key={item.path} to={target(item)} className="console-drawer-link" aria-current={[item.path, ...(item.also ?? [])].includes(location.pathname) ? "page" : undefined} onClick={() => setMenuOpen(false)}><span>{item.label}</span><span className="console-nav-purpose">{purpose[target(item)]}</span></NavLink>)}</nav><div className="console-drawer-account"><UserMenu placement="up" /></div></>}
    </dialog>

    <main id="main-content" tabIndex={-1} className="console-content outline-none"><div className="page-enter">
      {blocked ? <div className="panel mx-auto max-w-lg p-8 text-center"><p className="plate">Not included in this session</p><h1 tabIndex={-1} className="mt-2 text-xl font-semibold">This session cannot open {current.label}</h1><p className="text-muted-foreground mt-2 text-sm">You are signed in as {session?.name || "an operator"} without the {current.tool} tool. Ask an administrator to include it.</p></div> : <Routes>
        <Route path="/overview" element={<Overview />} />
        <Route path="/" element={<Overview />} />
        <Route path="/activity" element={<Activity />} />
        <Route path="/routes" element={<RoutingPage />} />
        <Route path="/applications" element={<Applications />} />
        <Route path="/sites" element={<Sites />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="/integrations" element={<Integrations />} />
        <Route path="/administrators" element={<Administrators />} />
        <Route path="/groups" element={<Groups />} />
        <Route path="/people" element={<People />} />
        <Route path="/connectors" element={<Connectors />} />
        <Route path="*" element={<Navigate to={{ pathname: "/overview", search: location.search, hash: location.hash }} replace />} />
      </Routes>}
    </div></main>
  </div>
  {updateNotice && <aside className="border-signal/30 bg-popover fixed top-20 right-4 z-50 w-[calc(100vw-2rem)] max-w-sm rounded-xl border p-4 shadow-2xl" role="status" aria-live="polite">
    <div className="flex items-start justify-between gap-4"><p className="min-w-0 text-sm font-medium">Ozal {updateNotice.version} is available</p><button type="button" className="link-quiet shrink-0 text-xs" onClick={() => { dismissedVersion.current = updateNotice.version; setUpdateNotice(null); }}>Dismiss</button></div>
    <div className="mt-3 flex justify-end"><Button onClick={() => setUpdateDialogOpen(true)}>Review update</Button></div>
  </aside>}
  <UpdateDialog open={updateDialogOpen} token={session?.token ?? ""} canInstall={can("updates.install")} onOpenChange={setUpdateDialogOpen} onDone={completeUpdate} />
  </>;
}
