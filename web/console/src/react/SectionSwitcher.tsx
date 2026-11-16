import { ChevronDown } from "lucide-react";
import { Link } from "react-router";
import * as Menu from "@radix-ui/react-dropdown-menu";
import { target, type Group, type Section } from "$lib/navigation";

// The header says where you are, "Access › Applications", and opens every
// place of the console, grouped and explained. It replaces a row of eight
// links: the current place is always named, and the rest is one step away.
export function SectionSwitcher({ groups, place, tools }: { groups: Group[]; place: { group: string; section: Section }; tools: readonly string[] }) {
  return <Menu.Root modal={false}>
    <Menu.Trigger className="console-switcher" aria-label={`Current place: ${place.group ? `${place.group}, ` : ""}${place.section.label}. Open the console's sections`}>
      {place.group && <><span className="console-switcher-group">{place.group}</span><span className="console-switcher-sep" aria-hidden="true">›</span></>}
      <span className="console-switcher-here">{place.section.label}</span>
      <ChevronDown className="console-switcher-chevron size-4" aria-hidden="true" />
    </Menu.Trigger>
    <Menu.Portal>
      <Menu.Content className="console-sections" align="start" sideOffset={10} collisionPadding={16}>
        {groups.map((group) => <Menu.Group key={group.name || "home"} className="console-sections-group">
          {group.name && <Menu.Label className="console-sections-heading">{group.name}</Menu.Label>}
          {group.sections.map((section) => {
            const current = section.path === place.section.path;
            return <Menu.Item key={section.path} asChild>
              <Link to={target(section, tools)} className="console-sections-item" aria-current={current ? "page" : undefined}>
                <span className="console-sections-label">{section.label}</span>
                <span className="console-sections-purpose">{section.purpose}</span>
              </Link>
            </Menu.Item>;
          })}
        </Menu.Group>)}
      </Menu.Content>
    </Menu.Portal>
  </Menu.Root>;
}
