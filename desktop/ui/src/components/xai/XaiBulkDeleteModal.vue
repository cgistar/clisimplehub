<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.bulkDeleteTitle')"
    style="width: 640px"
  >
    <n-space vertical>
      <n-space>
        <n-button size="small" @click="selectAll">{{ t('xai.selectAll') }}</n-button>
        <n-button size="small" @click="deselectAll">{{ t('xai.deselectAll') }}</n-button>
        <n-button size="small" @click="selectBanned">{{ t('xai.selectBanned') }}</n-button>
      </n-space>
      <n-checkbox-group v-model:value="selectedIds">
        <n-space vertical>
          <n-checkbox
            v-for="account in accounts"
            :key="account.id"
            :value="account.id || ''"
            :label="account.email || account.subject || account.id || ''"
          />
        </n-space>
      </n-checkbox-group>
      <n-empty v-if="accounts.length === 0" :description="t('xai.noAccountsAvailable')" />
    </n-space>
    <template #footer>
      <n-space justify="end">
        <n-button @click="visible = false">{{ t('common.cancel') }}</n-button>
        <n-button type="error" :loading="deleting" :disabled="selectedIds.length === 0" @click="handleDelete">
          {{ t('xai.bulkDelete') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NModal, NSpace, NButton, NCheckboxGroup, NCheckbox, NEmpty, useMessage, useDialog } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useXaiAccountsStore } from '../../stores/xaiAccountsStore'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const xaiStore = useXaiAccountsStore()
const { accounts } = storeToRefs(xaiStore)

const props = withDefaults(defineProps<{ show: boolean }>(), { show: false })
const emit = defineEmits<{
  'update:show': [show: boolean]
  success: []
}>()

const visible = ref(false)
const selectedIds = ref<string[]>([])
const deleting = ref(false)

watch(() => props.show, (v) => {
  visible.value = v
  if (v) selectedIds.value = []
})
watch(visible, (v) => {
  if (!v) emit('update:show', false)
})

function selectAll() {
  selectedIds.value = accounts.value.map((a) => a.id || '').filter(Boolean)
}
function deselectAll() {
  selectedIds.value = []
}
function selectBanned() {
  selectedIds.value = accounts.value
    .filter((a) => a.status === 'banned')
    .map((a) => a.id || '')
    .filter(Boolean)
}

function handleDelete() {
  if (selectedIds.value.length === 0) {
    message.warning(t('xai.selectAtLeastOne'))
    return
  }
  dialog.warning({
    title: t('common.confirm'),
    content: t('xai.bulkDeleteConfirm'),
    positiveText: t('common.ok'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      deleting.value = true
      try {
        await xaiStore.deleteAccounts(selectedIds.value)
        message.success(t('xai.bulkDeleteSuccess'))
        emit('success')
        visible.value = false
      } catch (error) {
        message.error(t('xai.bulkDeleteFailed') + (error instanceof Error ? error.message : String(error)))
      } finally {
        deleting.value = false
      }
    }
  })
}
</script>
