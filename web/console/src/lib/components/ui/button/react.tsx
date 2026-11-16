import type { ComponentProps } from "react";
import { tv, type VariantProps } from "tailwind-variants";
import { cn } from "$lib/utils";

export const buttonVariants = tv({
  base: "focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:ring-destructive/20 aria-invalid:border-destructive rounded-lg border border-transparent bg-clip-padding text-sm font-medium focus-visible:ring-3 active:translate-y-px [&_svg:not([class*='size-'])]:size-4 group/button inline-flex shrink-0 items-center justify-center whitespace-nowrap transition-all outline-none select-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0",
  variants: {
    variant: {
      // The next action: filled indigo.
      default: "bg-primary text-primary-foreground hover:bg-primary/85",
      // Other actions: a see-through surface that sits on the page or on a
      // card alike (never a black hole), lit in indigo on hover.
      outline: "border-border bg-white/[0.03] text-foreground hover:border-signal/45 hover:bg-signal/10",
      secondary: "bg-secondary text-secondary-foreground hover:bg-accent",
      ghost: "text-foreground hover:bg-signal/10",
      destructive: "border-destructive/40 bg-destructive/10 text-destructive hover:bg-destructive/20",
      link: "text-signal underline decoration-signal/45 underline-offset-4 hover:decoration-current",
    },
    size: {
      default: "h-9 gap-1.5 px-3",
      xs: "h-6 gap-1 rounded-md px-2 text-xs [&_svg:not([class*='size-'])]:size-3",
      sm: "h-8 gap-1 rounded-md px-2.5 text-[0.8rem] [&_svg:not([class*='size-'])]:size-3.5",
      lg: "h-9 gap-1.5 px-2.5",
      icon: "size-8",
      "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3",
      "icon-sm": "size-7 rounded-md",
      "icon-lg": "size-9",
    },
  },
  defaultVariants: { variant: "default", size: "default" },
});

export type ButtonVariant = VariantProps<typeof buttonVariants>["variant"];
export type ButtonSize = VariantProps<typeof buttonVariants>["size"];
export type ButtonProps = ComponentProps<"button"> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
};

export function Button({ className, variant, size, type = "button", ...props }: ButtonProps) {
  return (
    <button
      data-slot="button"
      className={cn(buttonVariants({ variant, size }), className)}
      type={type}
      {...props}
    />
  );
}
