import { h } from 'vue'
import { NButton, NSpace, useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'

export interface ConfirmDialogOptions {
  title?: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

export interface ConfirmOption<TValue extends string = string> {
  value: TValue
  text: string
  danger?: boolean
  primary?: boolean
}

export interface ConfirmWithOptionsDialogOptions<TValue extends string = string> {
  title?: string
  buttons: ConfirmOption<TValue>[]
}

export type FeedbackApi = {
  success: (content: string) => void
  error: (content: string) => void
  confirm: (content: string, options?: ConfirmDialogOptions) => Promise<boolean>
  confirmWithOptions: <TValue extends string = string>(
    content: string,
    options: ConfirmWithOptionsDialogOptions<TValue>
  ) => Promise<TValue | null>
}

export function useFeedback(): FeedbackApi {
  const message = useMessage()
  const dialog = useDialog()
  const { t } = useI18n()

  function success(content: string): void {
    message.success(content)
  }

  function error(content: string): void {
    message.error(content)
  }

  function confirm(content: string, options: ConfirmDialogOptions = {}): Promise<boolean> {
    return new Promise((resolve) => {
      let settled = false
      const finalize = (value: boolean) => {
        if (settled) return
        settled = true
        resolve(value)
      }

      const handler = options.danger ? dialog.error : dialog.warning
      handler({
        title: options.title || t('common.confirm') || 'Confirm',
        content,
        positiveText: options.confirmText || t('common.ok') || 'OK',
        negativeText: options.cancelText || t('common.cancel') || 'Cancel',
        onPositiveClick: () => finalize(true),
        onNegativeClick: () => finalize(false),
        onClose: () => finalize(false)
      })
    })
  }

  function confirmWithOptions<TValue extends string = string>(
    content: string,
    options: ConfirmWithOptionsDialogOptions<TValue>
  ): Promise<TValue | null> {
    const buttons = options.buttons || []
    if (buttons.length === 0) {
      return Promise.resolve(null)
    }

    return new Promise((resolve) => {
      let settled = false
      let dialogReactive: { destroy: () => void } | null = null
      const finalize = (value: TValue | null) => {
        if (settled) return
        settled = true
        resolve(value)
      }

      dialogReactive = dialog.warning({
        title: options.title || t('common.confirm') || 'Confirm',
        content,
        action: () =>
          h(
            NSpace,
            { justify: 'end', wrapItem: false },
            () =>
              buttons.map((button) =>
                h(
                  NButton,
                  {
                    type: button.danger ? 'error' : button.primary ? 'primary' : 'default',
                    onClick: () => {
                      finalize(button.value)
                      dialogReactive?.destroy()
                    }
                  },
                  { default: () => button.text }
                )
              )
          ),
        onClose: () => finalize(null)
      })
    })
  }

  return {
    success,
    error,
    confirm,
    confirmWithOptions
  }
}
