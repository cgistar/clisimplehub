<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NButton, NEmpty, NForm, NFormItem, NInput, NModal, NSpace, NSpin } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useFeedback } from '@/composables/useFeedback'
import type { Vendor, VendorInput } from '@/types/endpoint'
import { useVendorsStore } from '@/stores/vendorsStore'
import { useHomeEndpointsStore } from '@/stores/homeEndpointsStore'

const { t } = useI18n()
const feedback = useFeedback()
const vendorsStore = useVendorsStore()
const endpointStore = useHomeEndpointsStore()

const { vendors, loading } = storeToRefs(vendorsStore)

const props = withDefaults(defineProps<{
  show: boolean
}>(), {
  show: false
})

const emit = defineEmits<{
  'update:show': [show: boolean]
}>()

const saving = ref(false)
const deleting = ref(false)
const selectedVendorId = ref<number | null>(null)
const form = ref<VendorInput>(createDefaultForm())

const visible = computed({
  get: () => props.show,
  set: (value: boolean) => emit('update:show', value)
})
const isEditing = computed(() => (form.value.id || 0) > 0)
const formTitle = computed(() => (isEditing.value ? t('manage.editVendor') : t('manage.addVendor')))

function createDefaultForm(): VendorInput {
  return {
    id: 0,
    name: '',
    homeUrl: '',
    apiUrl: '',
    remark: ''
  }
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function fillForm(vendor: Vendor): void {
  selectedVendorId.value = vendor.id
  form.value = {
    id: vendor.id,
    name: vendor.name || '',
    homeUrl: vendor.homeUrl || '',
    apiUrl: vendor.apiUrl || '',
    remark: vendor.remark || ''
  }
}

function startCreate(): void {
  selectedVendorId.value = null
  form.value = createDefaultForm()
}

function close(): void {
  visible.value = false
}

async function prepareOpen(): Promise<void> {
  try {
    await vendorsStore.loadVendors()
    const selected = vendors.value.find((item) => item.id === selectedVendorId.value)
    if (selected) {
      fillForm(selected)
      return
    }

    if (vendors.value.length > 0) {
      fillForm(vendors.value[0])
      return
    }

    startCreate()
  } catch (error) {
    feedback.error(t('manage.saveFailed') + ': ' + toErrorMessage(error))
  }
}

async function save(): Promise<void> {
  const payload: VendorInput = {
    id: form.value.id || 0,
    name: form.value.name?.trim() || '',
    homeUrl: form.value.homeUrl?.trim() || '',
    apiUrl: form.value.apiUrl?.trim() || '',
    remark: form.value.remark?.trim() || ''
  }

  if (!payload.name || !payload.homeUrl || !payload.apiUrl) {
    feedback.error('Please fill in all required fields')
    return
  }

  try {
    saving.value = true
    const saved = await vendorsStore.saveVendor(payload)
    await endpointStore.refreshCurrent()

    const latest = vendors.value.find((item) => item.id === saved.id)
    if (latest) {
      fillForm(latest)
    } else {
      startCreate()
    }

    feedback.success('Vendor saved successfully')
  } catch (error) {
    feedback.error(t('manage.saveFailed') + ': ' + toErrorMessage(error))
  } finally {
    saving.value = false
  }
}

async function runDelete(vendorId: number): Promise<void> {
  if (!vendorId) return

  const confirmed = await feedback.confirm(t('manage.confirmDeleteVendor') || 'Confirm delete vendor?', {
    danger: true
  })
  if (!confirmed) return

  try {
    deleting.value = true
    await vendorsStore.deleteVendorById(vendorId)
    await endpointStore.refreshCurrent()

    if (vendors.value.length > 0) {
      fillForm(vendors.value[0])
    } else {
      startCreate()
    }

    feedback.success('Vendor deleted successfully')
  } catch (error) {
    feedback.error(t('manage.deleteFailed') + ': ' + toErrorMessage(error))
  } finally {
    deleting.value = false
  }
}

async function deleteCurrent(): Promise<void> {
  await runDelete(form.value.id || 0)
}

async function deleteById(vendorId: number): Promise<void> {
  if (deleting.value) return
  await runDelete(vendorId)
}

watch(() => props.show, (newVal) => {
  if (newVal) {
    void prepareOpen()
  }
})
</script>

<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('manage.vendors')"
    :style="{ width: 'min(1000px, 96vw)', maxHeight: '90vh' }"
    :closable="true"
    :mask-closable="false"
    :close-on-esc="true"
    :block-scroll="true"
    @close="close"
  >
    <div class="vendor-manage-body">
      <section class="vendor-panel vendor-list-panel">
        <div class="section-header">
          <h3>{{ t('manage.vendors') }}</h3>
          <n-button size="small" type="primary" @click="startCreate">
            {{ t('manage.addVendor') }}
          </n-button>
        </div>

        <n-spin :show="loading">
          <div v-if="vendors.length === 0" class="vendor-empty">
            <n-empty :description="t('manage.noVendors')" />
          </div>
          <div v-else class="vendor-list">
            <div
              v-for="vendor in vendors"
              :key="vendor.id"
              class="vendor-item"
              :class="{ selected: selectedVendorId === vendor.id }"
              @click="fillForm(vendor)"
            >
              <div class="vendor-main">
                <div class="vendor-name">{{ vendor.name }}</div>
                <div class="vendor-url">{{ vendor.apiUrl }}</div>
              </div>
              <n-space>
                <n-button
                  size="tiny"
                  quaternary
                  :title="t('manage.editVendor')"
                  @click.stop="fillForm(vendor)"
                >
                  {{ t('manage.editVendor') }}
                </n-button>
                <n-button
                  size="tiny"
                  quaternary
                  type="error"
                  :title="t('manage.delete')"
                  :disabled="deleting"
                  @click.stop="deleteById(vendor.id)"
                >
                  {{ t('manage.delete') }}
                </n-button>
              </n-space>
            </div>
          </div>
        </n-spin>
      </section>

      <section class="vendor-panel vendor-form-panel">
        <div class="section-header">
          <h3>{{ formTitle }}</h3>
        </div>
        <n-form
          :model="form"
          label-placement="top"
          class="vendor-form"
        >
          <n-form-item :label="`${t('manage.vendorName')} *`">
            <n-input v-model:value="form.name" :placeholder="t('manage.vendorNamePlaceholder')" />
          </n-form-item>
          <n-form-item :label="`${t('manage.homeUrl')} *`">
            <n-input v-model:value="form.homeUrl" :placeholder="t('manage.homeUrlPlaceholder')" />
          </n-form-item>
          <n-form-item :label="`${t('manage.apiUrl')} *`">
            <n-input v-model:value="form.apiUrl" :placeholder="t('manage.apiUrlPlaceholder')" />
          </n-form-item>
          <n-form-item :label="t('manage.remark')">
            <n-input v-model:value="form.remark" :placeholder="t('manage.remarkPlaceholder')" />
          </n-form-item>
        </n-form>
      </section>
    </div>

    <template #footer>
      <div class="vendor-footer">
        <n-button
          v-if="isEditing"
          type="error"
          secondary
          :disabled="saving || deleting"
          @click="deleteCurrent"
        >
          {{ t('manage.delete') }}
        </n-button>
        <n-space justify="end">
          <n-button :disabled="saving || deleting" @click="close">{{ t('manage.cancel') }}</n-button>
          <n-button type="primary" :loading="saving" :disabled="deleting" @click="save">
            {{ t('manage.save') }}
          </n-button>
        </n-space>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.vendor-manage-body {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(380px, 1.3fr);
  gap: 14px;
  min-height: 430px;
  max-height: min(68vh, 760px);
}

.vendor-panel {
  border: 1px solid var(--border-light);
  border-radius: var(--radius-md);
  background: var(--bg-primary);
  padding: 12px;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}

.section-header h3 {
  margin: 0;
  font-size: 14px;
}

.vendor-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow-y: auto;
  min-height: 0;
}

.vendor-item {
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
  background: var(--bg-primary);
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 8px;
  align-items: center;
  padding: 10px;
  cursor: pointer;
  transition: border-color 0.2s ease, background-color 0.2s ease;
}

.vendor-item:hover {
  border-color: color-mix(in srgb, var(--accent) 32%, white);
}

.vendor-item.selected {
  border-color: color-mix(in srgb, var(--accent) 48%, white);
  background: color-mix(in srgb, var(--accent) 8%, white);
}

.vendor-main {
  min-width: 0;
}

.vendor-name {
  font-weight: 600;
  font-size: 13px;
}

.vendor-url {
  margin-top: 4px;
  color: var(--text-tertiary);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.vendor-form {
  overflow-y: auto;
  min-height: 0;
  padding-right: 2px;
}

.vendor-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 220px;
}

.vendor-footer {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

@media (max-width: 900px) {
  .vendor-manage-body {
    grid-template-columns: 1fr;
    max-height: none;
    min-height: 0;
  }

  .vendor-list-panel {
    max-height: 40vh;
  }
}
</style>
