<script setup lang="ts">
import type { LeaveRequest } from '~/types/api'
import { formatDateRange } from '~/utils/format'

defineProps<{ leave: LeaveRequest; busy?: boolean }>()
defineEmits<{ approve: []; reject: [] }>()
</script>

<template>
  <article class="rounded-2xl bg-surface p-4">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="truncate text-[15px] font-semibold">{{ leave.employee?.full_name }}</p>
        <p class="text-[13px] text-hint">{{ leave.employee?.employee_code }} · {{ leave.leave_type?.name }}</p>
      </div>
      <StatusBadge tone="info" :label="`${leave.days} day${leave.days === 1 ? '' : 's'}`" />
    </div>
    <p class="mt-2 text-[15px] font-medium">{{ formatDateRange(leave.start_date, leave.end_date) }}</p>
    <p class="mt-1 whitespace-pre-line text-sm">{{ leave.reason }}</p>
    <div class="mt-3 grid grid-cols-2 gap-2">
      <AppButton variant="danger" :disabled="busy" @click="$emit('reject')">Reject</AppButton>
      <AppButton variant="success" :loading="busy" @click="$emit('approve')">Approve</AppButton>
    </div>
  </article>
</template>
