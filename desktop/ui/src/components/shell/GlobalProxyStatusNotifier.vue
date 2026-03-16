<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { endpointApi } from '@/api/endpoint'
import { useMainTabs } from '@/composables/useMainTabs'
import type { ProxyStatusPayload } from '@/types/endpoint'

type RuntimeWithEvents = Window & {
  runtime?: {
    EventsOn?: (event: string, callback: () => void) => (() => void) | void
  }
}

const message = useMessage()
const dialog = useDialog()
const { t } = useI18n()
const { switchMainTab } = useMainTabs()

const proxyStatus = ref<ProxyStatusPayload>({
  running: false,
  port: 0,
  listenAddr: '',
  lastError: ''
})

let offProxyStatusEvent: (() => void) | null = null
let dialogReactive: { destroy: () => void } | null = null
let lastShownError = ''

function closeDialog(): void {
  dialogReactive?.destroy()
  dialogReactive = null
}

function buildDialogContent(errorMessage: string): string {
  return `${t('settings.proxyStartFailedHelp')}\n${errorMessage}`
}

function showProxyError(errorMessage: string): void {
  if (!errorMessage || errorMessage === lastShownError) return

  lastShownError = errorMessage
  message.error(`${t('settings.proxyStartFailedTitle')}: ${errorMessage}`, {
    duration: 5000
  })

  closeDialog()
  dialogReactive = dialog.error({
    title: t('settings.proxyStartFailedTitle'),
    content: buildDialogContent(errorMessage),
    positiveText: t('common.retry'),
    negativeText: t('settings.openSettingsAction'),
    onPositiveClick: async () => {
      try {
        await endpointApi.startProxy()
        await refreshProxyStatus()
      } catch (error) {
        const errorText = error instanceof Error ? error.message : String(error)
        message.error(`${t('settings.retryProxyStartFailed')}: ${errorText}`)
        await refreshProxyStatus()
        return false
      }
      return true
    },
    onNegativeClick: () => {
      void switchMainTab('settings')
      closeDialog()
    },
    onClose: () => {
      dialogReactive = null
    }
  })
}

function syncNotifications(): void {
  const errorMessage = String(proxyStatus.value.lastError || '').trim()
  if (proxyStatus.value.running || !errorMessage) {
    lastShownError = ''
    closeDialog()
    return
  }

  showProxyError(errorMessage)
}

async function refreshProxyStatus(): Promise<void> {
  try {
    proxyStatus.value = await endpointApi.getProxyStatus()
    syncNotifications()
  } catch (error) {
    console.error('Failed to load proxy status:', error)
  }
}

onMounted(() => {
  void refreshProxyStatus()

  try {
    const runtime = (window as RuntimeWithEvents).runtime
    if (runtime?.EventsOn) {
      const off = runtime.EventsOn('proxy:status-changed', () => {
        void refreshProxyStatus()
      })
      if (typeof off === 'function') {
        offProxyStatusEvent = off
      }
    }
  } catch {
    // ignore
  }
})

onBeforeUnmount(() => {
  offProxyStatusEvent?.()
  offProxyStatusEvent = null
  closeDialog()
})
</script>

<template></template>
