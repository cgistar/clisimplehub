import * as React from "react"
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog"

import { CloseIcon } from "@/components/icons"
import { cn } from "@/lib/utils"

const Dialog = DialogPrimitive.Root

function DialogContent({
  className,
  children,
  showCloseButton = true,
  closeDisabled = false,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Popup> & {
  showCloseButton?: boolean
  closeDisabled?: boolean
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Backdrop className="dialog-backdrop" />
      <DialogPrimitive.Popup className={cn("dialog-card", className)} {...props}>
        {children}
        {showCloseButton ? (
          <DialogPrimitive.Close
            className="btn dialog-close-btn dialog-close-btn-floating"
            aria-label="关闭"
            title="关闭"
            disabled={closeDisabled}
          >
            <CloseIcon />
          </DialogPrimitive.Close>
        ) : null}
      </DialogPrimitive.Popup>
    </DialogPrimitive.Portal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("card-header dialog-header", className)} {...props} />
}

function DialogBody({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("dialog-body", className)} {...props} />
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("actions mt-18 dialog-actions", className)} {...props} />
}

function DialogTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn("card-title", className)} {...props} />
}

function DialogDescription({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn("card-subtitle", className)} {...props} />
}

export {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
}
