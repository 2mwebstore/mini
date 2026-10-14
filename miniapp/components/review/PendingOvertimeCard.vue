<script setup lang="ts">
import type { OvertimeRequest } from '~/types/api'
import { formatClock, formatDate, formatMinutes, todayISOAt } from '~/utils/format'

// An overtime request waiting for the reviewer (Team tab).
const props = defineProps<{ item: OvertimeRequest; timezone: string; busy?: boolean }>()
defineEmits<{ approve: []; reject: [] }>()
const nextDay = computed(() => todayISOAt(props.item.end_at, props.timezone) > props.item.work_date.slice(0, 10))
</script>

<template>
  <article class="rounded-2xl bg-surface p-4">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="truncate text-[15px] font-semibold">{{ item.employee?.full_name }}</p>
        <p class="text-[13px] text-hint">{{ item.employee?.employee_code }} · {{ formatDate(item.work_date) }}</p>
      </div>
      <StatusBadge tone="info" :label="formatMinutes(item.minutes)" />
    </div>
    <p class="mt-2 text-[22px] font-bold leading-tight tabular-nums">
      {{ formatClock(item.start_at, timezone) }} – {{ formatClock(item.end_at, timezone) }}<span v-if="nextDay" class="ml-1 text-[13px] font-medium text-hint">next day</span>
    </p>
    <p class="mt-1 whitespace-pre-line text-sm">{{ item.reason }}</p>
    <div class="mt-3 grid grid-cols-2 gap-2">
      <AppButton variant="danger" :disabled="busy" @click="$emit('reject')">Reject</AppButton>
      <AppButton variant="success" :loading="busy" @click="$emit('approve')">Approve</AppButton>
    </div>
  </article>
</template>
