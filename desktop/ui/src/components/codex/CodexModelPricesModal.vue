<template>
  <n-modal v-model:show="visible" preset="card" title="模型单价" style="width: min(1080px, calc(100vw - 32px))">
    <p class="price-help">单位为 USD / 1M Token。预估成本仅基于本地统计，不代表 OpenAI 实际账单、订阅余额或额度。</p>

    <n-spin :show="loading">
      <div class="price-table-wrap">
        <table class="price-table">
          <thead>
            <tr>
              <th>模型</th><th>输入 / 1M</th><th>缓存读取 / 1M</th><th>缓存写入 / 1M</th><th>输出 / 1M</th><th aria-label="操作"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(price, index) in rows" :key="`${index}-${price.model}`">
              <td><n-input v-model:value="price.model" placeholder="例如 gpt-5.6-sol" /></td>
              <td><n-input-number v-model:value="price.inputPer1M" :min="0" :precision="6" /></td>
              <td><n-input-number v-model:value="price.cachedInputPer1M" :min="0" :precision="6" /></td>
              <td><n-input-number v-model:value="price.cacheWritePer1M" :min="0" :precision="6" /></td>
              <td><n-input-number v-model:value="price.outputPer1M" :min="0" :precision="6" /></td>
              <td><n-button type="error" secondary size="small" :disabled="saving" @click="removeRow(index)">删除</n-button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </n-spin>

    <template #footer>
      <n-space justify="space-between">
        <n-button :disabled="loading || saving" @click="addRow">添加模型</n-button>
        <n-space>
          <n-button :disabled="saving" @click="close">取消</n-button>
          <n-button type="primary" :loading="saving" :disabled="loading" @click="save">保存</n-button>
        </n-space>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NButton, NInput, NInputNumber, NModal, NSpace, NSpin, useMessage } from 'naive-ui'
import { useCodexModelPricesStore } from '@/stores/codexModelPricesStore'
import type { CodexModelPrice } from '@/types/codex'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [show: boolean]; saved: [] }>()
const message = useMessage()
const priceStore = useCodexModelPricesStore()
const visible = ref(false)
const loading = ref(false)
const saving = ref(false)
const rows = ref<CodexModelPrice[]>([])

function copyPrices(prices: CodexModelPrice[]): CodexModelPrice[] {
  return prices.map((price) => ({
    model: price.model,
    inputPer1M: price.inputPer1M,
    cachedInputPer1M: price.cachedInputPer1M,
    cacheWritePer1M: price.cacheWritePer1M,
    outputPer1M: price.outputPer1M
  }))
}

function toErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

watch(() => props.show, async (show) => {
  visible.value = show
  if (!show) return
  loading.value = true
  try {
    rows.value = copyPrices(await priceStore.loadPrices())
  } catch (error) {
    message.error(`加载模型单价失败：${toErrorMessage(error)}`)
    visible.value = false
  } finally {
    loading.value = false
  }
}, { immediate: true })

watch(visible, (show) => {
  if (!show) emit('update:show', false)
})

function addRow(): void {
  rows.value.push({ model: '', inputPer1M: 0, cachedInputPer1M: 0, cacheWritePer1M: 0, outputPer1M: 0 })
}

function removeRow(index: number): void {
  rows.value.splice(index, 1)
}

function validateRows(): CodexModelPrice[] | null {
  const seen = new Set<string>()
  const normalized = copyPrices(rows.value)
  for (const price of normalized) {
    price.model = price.model.trim()
    if (!price.model) {
      message.error('模型名不能为空')
      return null
    }
    if (seen.has(price.model)) {
      message.error(`模型名重复：${price.model}`)
      return null
    }
    seen.add(price.model)
    for (const amount of [price.inputPer1M, price.cachedInputPer1M, price.cacheWritePer1M, price.outputPer1M]) {
      if (!Number.isFinite(amount) || amount < 0) {
        message.error(`模型 ${price.model} 的价格必须是非负有限数`)
        return null
      }
    }
  }
  return normalized
}

function close(): void {
  visible.value = false
}

async function save(): Promise<void> {
  const prices = validateRows()
  if (!prices) return
  saving.value = true
  try {
    await priceStore.savePrices(prices)
    message.success('模型单价已保存')
    emit('saved')
    close()
  } catch (error) {
    message.error(`保存模型单价失败：${toErrorMessage(error)}`)
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.price-help { margin: 0 0 14px; color: var(--text-color-3); line-height: 1.5; }
.price-table-wrap { overflow-x: auto; }
.price-table { width: 100%; min-width: 860px; border-collapse: collapse; }
.price-table th, .price-table td { padding: 8px; text-align: left; border-bottom: 1px solid var(--border-color); }
.price-table th { color: var(--text-color-2); font-weight: 600; white-space: nowrap; }
</style>
