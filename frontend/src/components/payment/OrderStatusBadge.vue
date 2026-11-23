<template>
  <span
    class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
    :class="statusClass"
  >
    {{ statusLabel }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  status: string
}>()

const { t } = useI18n()

const SUCCESS_CLASS = 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400'
const UNKNOWN_CLASS = 'bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400'

/** 大小写与首尾空白不影响匹配（如客服手动充值直接落库的 SUCCESS） */
function normalizeStatus(status: string): string {
  return String(status ?? '').trim().toUpperCase()
}

/** 与已完成等价的原始状态值，统一按成功展示 */
const statusAliases: Record<string, string> = {
  SUCCESS: 'COMPLETED',
}

const statusMap: Record<string, { key: string; class: string }> = {
  PENDING: { key: 'payment.status.pending', class: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400' },
  PAID: { key: 'payment.status.paid', class: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400' },
  RECHARGING: { key: 'payment.status.recharging', class: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400' },
  COMPLETED: { key: 'payment.status.completed', class: SUCCESS_CLASS },
  EXPIRED: { key: 'payment.status.expired', class: 'bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400' },
  CANCELLED: { key: 'payment.status.cancelled', class: 'bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400' },
  FAILED: { key: 'payment.status.failed', class: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400' },
  REFUND_REQUESTED: { key: 'payment.status.refund_requested', class: 'bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-400' },
  REFUNDING: { key: 'payment.status.refunding', class: 'bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-400' },
  REFUND_PENDING: { key: 'payment.status.refund_pending', class: 'bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-400' },
  REFUNDED: { key: 'payment.status.refunded', class: 'bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400' },
  PARTIALLY_REFUNDED: { key: 'payment.status.partially_refunded', class: 'bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400' },
  REFUND_FAILED: { key: 'payment.status.refund_failed', class: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400' },
}

const statusEntry = computed(() => {
  const normalized = normalizeStatus(props.status)
  return statusMap[statusAliases[normalized] ?? normalized]
})

const statusLabel = computed(() => {
  const entry = statusEntry.value
  return entry ? t(entry.key) : props.status
})

const statusClass = computed(() => statusEntry.value?.class ?? UNKNOWN_CLASS)
</script>
