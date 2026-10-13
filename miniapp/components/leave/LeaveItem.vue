<script setup lang="ts">
import type { LeaveRequest } from '~/types/api'
import { formatDateRange, leaveStatus } from '~/utils/format'

defineProps<{ leave: LeaveRequest }>()
</script>

<template>
  <div class="py-3">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-[15px] font-medium">{{ leave.leave_type?.name ?? 'Leave' }}</p>
        <p class="mt-0.5 text-[13px] text-hint">
          {{ formatDateRange(leave.start_date, leave.end_date) }} · {{ leave.days }} working day{{ leave.days === 1 ? '' : 's' }}
        </p>
      </div>
      <StatusBadge v-bind="leaveStatus[leave.status]" />
    </div>
    <p class="mt-1.5 line-clamp-2 text-sm">{{ leave.reason }}</p>
    <p v-if="leave.review_note" class="mt-1 text-[13px] text-hint">
      {{ leave.reviewer?.full_name ? `${leave.reviewer.full_name}: ` : '' }}{{ leave.review_note }}
    </p>
    <slot />
  </div>
</template>
