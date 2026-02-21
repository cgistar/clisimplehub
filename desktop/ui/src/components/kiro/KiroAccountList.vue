<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { NEmpty, NSpin, useMessage, useDialog } from 'naive-ui';
import { useKiroAccountsStore } from '@/stores/kiroAccountsStore';
import type { KiroAccount } from '@/types/kiro';
import KiroAccountCard from './KiroAccountCard.vue';

const { t } = useI18n();
const message = useMessage();
const dialog = useDialog();
const kiroStore = useKiroAccountsStore();

const emit = defineEmits<{
  'edit-account': [account: KiroAccount]
}>();

const accounts = computed(() => kiroStore.filteredAccounts);
const loading = computed(() => kiroStore.loading);
const isEmpty = computed(() => accounts.value.length === 0 && !loading.value);

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return String(error);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

async function handleActivate(account: KiroAccount): Promise<void> {
  try {
    await kiroStore.setActiveAccount(account.refreshToken);
    message.success(t('kiro.accountSwitched'));
  } catch (error) {
    message.error(t('kiro.switchAccountFailed') + ': ' + toErrorMessage(error));
  }
}

async function handleTest(account: KiroAccount): Promise<void> {
  try {
    message.loading(t('kiro.testing'), { duration: 0 });
    const result = await kiroStore.testAccount(account.refreshToken);
    const asRecord = isRecord(result) ? result : {};
    const success = asRecord.success !== false;
    const resultMessage = typeof asRecord.message === 'string' ? asRecord.message : '';
    message.destroyAll();
    if (success) {
      message.success(t('kiro.testSuccess'));
    } else {
      message.error(t('kiro.testFailed') + ': ' + resultMessage);
    }
  } catch (error) {
    message.destroyAll();
    message.error(t('kiro.testFailed') + ': ' + toErrorMessage(error));
  }
}

async function handleUsage(account: KiroAccount): Promise<void> {
  try {
    message.loading(t('kiro.fetchingUsage'), { duration: 0 });
    const result = await kiroStore.fetchUsage(account.refreshToken);
    message.destroyAll();
    if (result) {
      message.success(t('kiro.usageFetched'));
      await kiroStore.loadAccounts(); // Refresh to show updated usage
    }
  } catch (error) {
    message.destroyAll();
    message.error(t('kiro.fetchUsageFailed') + ': ' + toErrorMessage(error));
  }
}

function handleCopy(account: KiroAccount): void {
  if (navigator.clipboard) {
    navigator.clipboard.writeText(account.refreshToken);
    message.success(t('kiro.copiedToClipboard'));
  } else {
    message.error(t('kiro.copyFailed'));
  }
}

function handleEdit(account: KiroAccount): void {
  emit('edit-account', account);
}

function handleDelete(account: KiroAccount): void {
  dialog.warning({
    title: t('kiro.deleteConfirm'),
    content: t('kiro.deleteConfirmMessage', { email: account.email }),
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await kiroStore.deleteAccount(account.refreshToken);
        message.success(t('kiro.deleteSuccess'));
      } catch (error) {
        message.error(t('kiro.deleteFailed') + ': ' + toErrorMessage(error));
      }
    }
  });
}

function isActive(account: KiroAccount): boolean {
  return account.refreshToken === kiroStore.activeRefreshToken;
}

onMounted(() => {
  if (accounts.value.length === 0) {
    kiroStore.loadAccounts();
  }
});
</script>

<template>
  <div class="kiro-account-list-container">
    <n-spin :show="loading">
      <n-empty
        v-if="isEmpty"
        :description="t('kiro.noAccounts')"
        style="padding: 40px 0"
      />

      <div v-else class="kiro-account-grid">
        <KiroAccountCard
          v-for="item in accounts"
          :key="item.refreshToken"
          :account="item"
          :is-active="isActive(item)"
          @activate="handleActivate"
          @test="handleTest"
          @usage="handleUsage"
          @copy="handleCopy"
          @edit="handleEdit"
          @delete="handleDelete"
        />
      </div>
    </n-spin>
  </div>
</template>

<style scoped>
.kiro-account-list-container {
  flex: 1;
  height: 100%;
  min-height: 0;
  overflow-y: auto;
  padding-right: 4px;
}

.kiro-account-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, 340px);
  gap: 16px;
  padding: 4px 0 8px;
  justify-content: start;
}

@media (max-width: 760px) {
  .kiro-account-grid {
    grid-template-columns: 1fr;
  }
}
</style>
