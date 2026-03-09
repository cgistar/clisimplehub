<script setup lang="ts">
import { reactive, watch } from 'vue'
import { NModal } from 'naive-ui'
import { useI18n } from 'vue-i18n'

const props = withDefaults(
  defineProps<{
    show: boolean
    title: string
    submitText?: string
    initialName?: string
    initialUrl?: string
    saving?: boolean
  }>(),
  {
    submitText: '',
    initialName: '',
    initialUrl: '',
    saving: false
  }
)

const emit = defineEmits<{
  'update:show': [value: boolean]
  submit: [payload: { name: string; url: string }]
}>()

const { t } = useI18n()

const form = reactive({
  name: '',
  url: ''
})

watch(
  () => [props.show, props.initialName, props.initialUrl],
  ([show]) => {
    if (!show) return
    form.name = props.initialName || ''
    form.url = props.initialUrl || ''
  },
  { immediate: true }
)

function handleClose(): void {
  emit('update:show', false)
}

function handleSubmit(): void {
  emit('submit', {
    name: form.name.trim(),
    url: form.url.trim()
  })
}
</script>

<template>
  <n-modal :show="show" :mask-closable="false" @update:show="emit('update:show', $event)">
    <div class="mx-auto mt-[10vh] w-[92vw] max-w-lg rounded-xl border border-slate-200 bg-white p-5 shadow-xl">
      <div class="mb-4 flex items-center justify-between">
        <h3 class="text-base font-semibold text-slate-900">{{ title }}</h3>
        <button type="button" class="rounded px-2 py-1 text-slate-500 hover:bg-slate-100" @click="handleClose">×</button>
      </div>

      <div class="space-y-4">
        <label class="block text-sm">
          <span class="mb-1 block text-slate-700">{{ t('clash.subName') }}</span>
          <input
            v-model="form.name"
            type="text"
            class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm outline-none focus:border-sky-500"
            :placeholder="t('clash.subName')"
          />
        </label>

        <label class="block text-sm">
          <span class="mb-1 block text-slate-700">{{ t('clash.subUrl') }}</span>
          <input
            v-model="form.url"
            type="text"
            class="w-full rounded-md border border-slate-300 px-3 py-2 text-sm outline-none focus:border-sky-500"
            :placeholder="t('clash.subUrl')"
          />
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
          @click="handleSubmit"
        >
          {{ saving ? '...' : (submitText || t('common.save')) }}
        </button>
      </div>
    </div>
  </n-modal>
</template>
