<script setup lang="ts">
import { reactive, watch } from 'vue'
import { NInput, NModal, NSelect } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { ClashConfig, ClashLogLevel } from '@/types/clash'

const props = withDefaults(
  defineProps<{
    show: boolean
    config: ClashConfig
    saving?: boolean
  }>(),
  {
    saving: false
  }
)

const emit = defineEmits<{
  'update:show': [value: boolean]
  save: [payload: ClashConfig]
}>()

const { t } = useI18n()
const logLevelOptions: Array<{ label: string; value: ClashLogLevel }> = [
  { label: 'Debug', value: 'debug' },
  { label: 'Info', value: 'info' },
  { label: 'Warning', value: 'warning' },
  { label: 'Error', value: 'error' },
  { label: 'None', value: 'none' }
]

const form = reactive({
  socksListen: '127.0.0.1',
  socksPort: 10808,
  logLevel: 'warning' as ClashLogLevel,
  globalProxy: false,
  userYaml: ''
})

function syncFromProps(): void {
  form.socksListen = props.config?.socksListen || '127.0.0.1'
  form.socksPort = Number(props.config?.socksPort || 10808)
  form.logLevel = (props.config?.logLevel || 'warning') as ClashLogLevel
  form.globalProxy = !!props.config?.globalProxy
  form.userYaml = props.config?.userYaml || ''
}

watch(
  () => [props.show, props.config],
  ([show]) => {
    if (show) {
      syncFromProps()
    }
  },
  { deep: true, immediate: true }
)

function handleClose(): void {
  emit('update:show', false)
}

function handleSave(): void {
  emit('save', {
    ...props.config,
    socksListen: form.socksListen.trim() || '127.0.0.1',
    socksPort: Number(form.socksPort || 10808),
    logLevel: form.logLevel,
    globalProxy: !!form.globalProxy,
    userYaml: form.userYaml,
    subscriptions: Array.isArray(props.config?.subscriptions) ? props.config.subscriptions : []
  })
}
</script>

<template>
  <n-modal :show="show" :mask-closable="false" @update:show="emit('update:show', $event)">
    <div class="mx-auto mt-[6vh] w-[94vw] max-w-4xl rounded-xl border border-slate-200 bg-white p-5 shadow-xl">
      <div class="mb-4 flex items-center justify-between">
        <h3 class="text-base font-semibold text-slate-900">{{ t('clash.configTitle') }}</h3>
        <button type="button" class="rounded px-2 py-1 text-slate-500 hover:bg-slate-100" @click="handleClose">×</button>
      </div>

      <div class="max-h-[72vh] space-y-4 overflow-y-auto pr-1">
        <div class="grid gap-4 md:grid-cols-2">
          <label class="block text-sm">
            <span class="mb-1 block text-slate-700">{{ t('clash.socksListen') }}</span>
            <input
              v-model="form.socksListen"
              type="text"
              class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm outline-none focus:border-sky-500"
              placeholder="127.0.0.1"
            />
          </label>

          <label class="block text-sm">
            <span class="mb-1 block text-slate-700">{{ t('clash.socksPort') }}</span>
            <input
              v-model.number="form.socksPort"
              type="number"
              min="1"
              max="65535"
              class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm outline-none focus:border-sky-500"
              placeholder="10808"
            />
          </label>
        </div>

        <div class="grid gap-4 md:grid-cols-[minmax(0,1fr),auto] md:items-start">
          <label class="block text-sm">
            <span class="mb-1 block text-slate-700">{{ t('clash.logLevel') }}</span>
            <n-select
              v-model:value="form.logLevel"
              :options="logLevelOptions"
              class="w-full"
            />
          </label>

          <div class="space-y-2 rounded-md border border-slate-200 bg-slate-50 p-3">
            <label class="flex items-center justify-between gap-3 text-sm">
              <span class="font-medium text-slate-800">{{ t('clash.globalProxy') }}</span>
              <span class="relative inline-flex h-6 w-11 shrink-0 items-center">
                <input v-model="form.globalProxy" type="checkbox" class="peer sr-only" />
                <span
                  class="h-6 w-11 rounded-full bg-slate-300 transition-colors duration-200 peer-checked:bg-sky-600"
                ></span>
                <span
                  class="pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform duration-200 peer-checked:translate-x-5"
                ></span>
              </span>
            </label>
            <p class="text-xs leading-5 text-slate-600">
              {{ t('clash.globalProxyHelp') }}
            </p>
          </div>
        </div>

        <label class="block text-sm">
          <span class="mb-1 block text-slate-700">{{ t('clash.userYaml') }}</span>
          <n-input
            v-model:value="form.userYaml"
            type="textarea"
            class="font-mono text-xs"
            :autosize="{ minRows: 14, maxRows: 24 }"
            :spellcheck="false"
            :placeholder="t('clash.userYamlPlaceholder')"
          />
          <p class="mt-2 text-xs leading-5 text-slate-500">
            {{ t('clash.userYamlHelp') }}
          </p>
        </label>
      </div>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-100"
          @click="handleClose"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="rounded-md border border-sky-600 bg-sky-600 px-3 py-1.5 text-sm text-white hover:bg-sky-700 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="saving"
          @click="handleSave"
        >
          {{ saving ? '...' : t('settings.save') }}
        </button>
      </div>
    </div>
  </n-modal>
</template>
