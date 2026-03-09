<script setup lang="ts">
import { ref, watch } from 'vue'
import { NModal } from 'naive-ui'
import { useI18n } from 'vue-i18n'

const props = withDefaults(
  defineProps<{
    show: boolean
    saving?: boolean
  }>(),
  {
    saving: false
  }
)

const emit = defineEmits<{
  'update:show': [value: boolean]
  submit: [content: string]
}>()

const { t } = useI18n()
const content = ref('')

watch(
  () => props.show,
  (show) => {
    if (show) {
      content.value = ''
    }
  }
)

function close(): void {
  emit('update:show', false)
}

function submit(): void {
  emit('submit', content.value)
}
</script>

<template>
  <n-modal :show="show" :mask-closable="false" @update:show="emit('update:show', $event)">
    <div class="mx-auto mt-[10vh] w-[92vw] max-w-2xl rounded-xl border border-slate-200 bg-white p-5 shadow-xl">
      <div class="mb-4 flex items-center justify-between">
        <h3 class="text-base font-semibold text-slate-900">{{ t('clash.addNodeTitle') }}</h3>
        <button type="button" class="rounded px-2 py-1 text-slate-500 hover:bg-slate-100" @click="close">×</button>
      </div>

      <textarea
        v-model="content"
        rows="12"
        class="w-full rounded-md border border-slate-300 px-3 py-2 font-mono text-sm outline-none focus:border-sky-500"
        :placeholder="t('clash.addNodePlaceholder')"
      ></textarea>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-slate-300 px-3 py-1.5 text-sm text-slate-700 hover:bg-slate-100"
          @click="close"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="rounded-md border border-sky-600 bg-sky-600 px-3 py-1.5 text-sm text-white hover:bg-sky-700 disabled:cursor-not-allowed disabled:opacity-60"
          :disabled="saving"
          @click="submit"
        >
          {{ saving ? '...' : t('clash.addNode') }}
        </button>
      </div>
    </div>
  </n-modal>
</template>
