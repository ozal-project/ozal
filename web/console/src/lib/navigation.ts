// Where everything in the console lives. One list feeds the section switcher
// in the header, the phone menu and the Overview hub, so a page is named and
// grouped the same way everywhere. Groups follow the work: who may enter
// (Access), how traffic reaches the edge (Network), what is published
// (Publishing), what happened (Insight) and how the edge itself is set up
// (Settings).

export type Tool = "applications" | "people" | "administrators" | "sites" | "connectors" | "updates" | "updates.install" | "publishing";

export type Section = {
  path: string;
  label: string;
  purpose: string;
  // The tool a session needs to open it; none means every session.
  tool?: Tool;
  // Other paths that belong to this section (its tabs).
  also?: string[];
};

export type Group = { name: string; sections: Section[] };

export const groups: Group[] = [
  { name: "", sections: [{ path: "/overview", label: "Overview", purpose: "What needs you, and what changed", also: ["/"] }] },
  { name: "Access", sections: [
    { path: "/applications", label: "Applications", purpose: "Addresses, what is behind them and who may enter", tool: "applications", also: ["/routes"] },
    { path: "/people", label: "People", purpose: "Who can sign in, and their groups", tool: "people", also: ["/groups"] },
  ] },
  { name: "Network", sections: [
    { path: "/domains", label: "Domains", purpose: "Domains, DNS and certificates", tool: "applications" },
    { path: "/connectors", label: "Connectors", purpose: "Private hosts reached without opening a port", tool: "connectors" },
  ] },
  { name: "Publishing", sections: [
    { path: "/sites", label: "Sites", purpose: "Published versions and GitHub builds" },
  ] },
  { name: "Insight", sections: [
    { path: "/activity", label: "Activity", purpose: "Access decisions and their reasons" },
  ] },
  { name: "Settings", sections: [
    { path: "/administrators", label: "Administrators", purpose: "Who runs this console, and their tools", tool: "administrators" },
    { path: "/integrations", label: "Integrations", purpose: "GitHub, DNS providers and how sign-in works" },
  ] },
];

const all = groups.flatMap((group) => group.sections.map((section) => ({ group: group.name, section })));

// placeOf finds the section a path belongs to, with its group; an unknown
// path is the Overview, where the router sends it.
export function placeOf(pathname: string): { group: string; section: Section } {
  return all.find(({ section }) => section.path === pathname || section.also?.includes(pathname)) ?? all[0];
}

// allowed says whether a session with these tools may open a section.
export function allowed(section: Section, tools: readonly string[]): boolean {
  return !section.tool || tools.includes(section.tool);
}

// visibleGroups are the groups and sections this session may open. Applications
// stays visible without its tool: the read-only routing view is open to all.
export function visibleGroups(tools: readonly string[]): Group[] {
  return groups
    .map((group) => ({ ...group, sections: group.sections.filter((s) => allowed(s, tools) || s.path === "/applications") }))
    .filter((group) => group.sections.length > 0);
}

// target is where a section's link goes for this session: Applications falls
// back to its read-only routing view without the applications tool.
export function target(section: Section, tools: readonly string[]): string {
  return section.path === "/applications" && !tools.includes("applications") ? "/routes" : section.path;
}
