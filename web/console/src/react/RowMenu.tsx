import { ChevronDown } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "$lib/components/ui/dropdown-menu/react";
import { Button } from "$lib/components/ui/button/react";

export type RowMenuItem = {
  label: string;
  hint?: string;
  onSelect(): void;
  destructive?: boolean;
};

type RowMenuProps = { label: string; items: RowMenuItem[] };

export function RowMenu({ label, items }: RowMenuProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          aria-label={label}
          className="h-8 text-[0.8rem]"
        >
          More <ChevronDown className="text-muted-foreground size-3.5" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" aria-label={label} className="w-64">
        {items.map((item) => (
          <DropdownMenuItem
            key={item.label}
            onSelect={item.onSelect}
            className={item.destructive ? "text-destructive focus:text-destructive" : undefined}
          >
            <span className="block">
              <span className="block">{item.label}</span>
              {item.hint && <span className="text-muted-foreground block text-xs">{item.hint}</span>}
            </span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
