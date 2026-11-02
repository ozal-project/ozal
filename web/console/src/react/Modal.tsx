import { createPortal } from "react-dom";
import { useId, useLayoutEffect, useRef, type ReactNode, type RefObject } from "react";
import { X } from "lucide-react";
import { Button } from "$lib/components/ui/button/react";
import { cn } from "$lib/utils";

type ModalProps = {
  open: boolean;
  onOpenChange(open: boolean): void;
  title: string;
  intro?: string;
  wide?: boolean;
  portalContainer?: HTMLElement;
  initialFocusRef?: RefObject<HTMLElement | null>;
  children: ReactNode;
};

export function Modal({ open, onOpenChange, title, intro = "", wide = false, portalContainer, initialFocusRef, children }: ModalProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const titleID = useId();
  const introID = useId();

  useLayoutEffect(() => {
    const element = dialog.current;
    if (!element) return;
    if (open && !element.open) {
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      element.showModal();
      if (initialFocusRef?.current?.isConnected) initialFocusRef.current.focus();
    } else if (!open && element.open) {
      element.close();
    }
  }, [open, initialFocusRef]);

  const content = (
    <dialog
      ref={dialog}
      aria-modal="true"
      aria-labelledby={titleID}
      aria-describedby={intro ? introID : undefined}
      className={cn(
        "ozal-modal bg-card text-card-foreground border-border fixed top-1/2 left-1/2 z-50 m-0 max-h-[calc(100dvh-2rem)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border p-5 outline-none sm:p-6",
        wide ? "w-[min(44rem,calc(100vw-2rem))]" : "w-[min(30rem,calc(100vw-2rem))]",
      )}
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onOpenChange(false);
      }}
      onClose={(event) => {
        event.stopPropagation();
        const target = returnFocus.current;
        returnFocus.current = null;
        onOpenChange(false);
        requestAnimationFrame(() => {
          if (target?.isConnected) target.focus();
        });
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onOpenChange(false);
      }}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const dialog = event.currentTarget;
        const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        )).filter((element) => element.getClientRects().length > 0);
        const first = focusable[0];
        const last = focusable.at(-1);
        if (!first || !last) return;
        if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog)) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && (document.activeElement === last || document.activeElement === dialog)) {
          event.preventDefault();
          first.focus();
        }
      }}
    >
      <div className="mb-5 flex items-start justify-between gap-4">
        <div>
          <h2 id={titleID} className="text-lg font-semibold">{title}</h2>
          {intro && <p id={introID} className="text-muted-foreground mt-1 text-sm">{intro}</p>}
        </div>
        <Button type="button" variant="ghost" size="icon-sm" aria-label="Close" onClick={() => onOpenChange(false)}>
          <X className="size-4" />
        </Button>
      </div>
      {children}
    </dialog>
  );

  return createPortal(content, portalContainer ?? document.body);
}
