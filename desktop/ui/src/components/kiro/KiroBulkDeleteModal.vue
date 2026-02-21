<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('kiro.bulkDelete')"
    style="width: 720px"
  >
    <n-space vertical :size="16">
      <n-space>
        <n-button size="small" @click="selectAll">{{ t('kiro.selectAll') }}</n-button>
        <n-button size="small" @click="deselectAll">{{ t('kiro.deselectAll') }}</n-button>
        <n-button size="small" type="warning" @click="selectBanned">{{ t('kiro.selectBanned') }}</n-button>
      </n-space>

      <n-checkbox-group v-model:value="selectedTokens">
        <n-virtual-list
          :items="accounts"
          :item-size="44"
          :style="{ height: '320px' }"
        >
          <template #default="{ item }">
            <div class="bulk-row">
              <n-checkbox :value="item.refreshToken">
                <div class="bulk-item-text">
                  <span class="bulk-email">{{ item.email || item.refreshToken.slice(0, 12) + '...' }}</span>
                  <n-tag :type="statusTagType(item.status)" size="small">{{ t(`kiro.status.${item.status || 'unknown'}`) }}</n-tag>
                </div>
              </n-checkbox>
            </div>
          </template>
        </n-virtual-list>
      </n-checkbox-group>
    </n-space>

    <template #footer>
      <n-space justify="space-between" align="center">
        <n-text depth="3">{{ t('common.selected') }}: {{ selectedTokens.length }}</n-text>
        <n-space>
          <n-button @click="close">{{ t('common.cancel') }}</n-button>
          <n-button type="error" :loading="deleting" @click="confirmDelete">{{ t('common.delete') }}</n-button>
        </n-space>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NModal,
  NSpace,
  NButton,
  NCheckbox,
  NCheckboxGroup,
  NVirtualList,
  NTag,
  NText,
  useMessage
} from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { KiroAccount } from '@/types/kiro'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(
  defineProps<{
    show: boolean
    accounts: KiroAccount[]
  }>(),
  {
    show: false,
    accounts: () => []
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  execute: [refreshTokens: string[]]
}>()

const visible = ref(false)
const deleting = ref(false)
const selectedTokens = ref<string[]>([])

const accounts = computed(() => props.accounts || [])

watch(
  () => props.show,
  (show) => {
    visible.value = show
    if (show) selectedTokens.value = []
  },
  { immediate: true }
)

watch(visible, (show) => {
  if (!show) emit('update:show', false)
})

function close(): void {
  visible.value = false
}

function selectAll(): void {
  selectedTokens.value = accounts.value.map((account) => account.refreshToken)
}

function deselectAll(): void {
  selectedTokens.value = []
}

function selectBanned(): void {
  selectedTokens.value = accounts.value
    .filter((account) => String(account.status || '').toLowerCase() === 'banned')
    .map((account) => account.refreshToken)
}

function statusTagType(status: string | undefined): 'success' | 'error' | 'warning' | 'default' {
  const normalized = String(status || '').toLowerCase()
  if (normalized === 'active') return 'success'
  if (normalized === 'banned') return 'error'
  if (normalized === 'warning') return 'warning'
  return 'default'
}

async function confirmDelete(): Promise<void> {
  if (selectedTokens.value.length === 0) {
    message.error(t('kiro.noAccountSelected'))
    return
  }

  deleting.value = true
  try {
    emit('execute', [...selectedTokens.value])
    close()
  } finally {
    deleting.value = false
  }
}
</script>

<style scoped>
.bulk-row {
  padding: 8px 0;
  border-bottom: 1px solid var(--border-light);
}

.bulk-item-text {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.bulk-email {
  font-weight: 500;
}
</style>
