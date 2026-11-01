import type { ComponentProps } from "react";
import { cn } from "$lib/utils";

export function Alert({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      role="alert"
      data-slot="alert"
      className={cn("border-border bg-card relative w-full rounded-lg border p-4", className)}
      {...props}
    />
  );
}

export function AlertTitle({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="alert-title" className={cn("mb-1 font-medium leading-none", className)} {...props} />;
}

export function AlertDescription({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="alert-description" className={cn("text-muted-foreground text-sm [&_p]:leading-relaxed", className)} {...props} />;
}
