<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'
import type { OvertimeRequest } from '~/types/api'
import { formatClock, formatDate, formatMinutes, leaveStatus, todayISOAt } from '~/utils/format'

// One overtime request in the employee's list.
const props = defineProps<{ item: OvertimeRequest }>()
const auth = useAuthStore()
const nextDay = computed(() => todayISOAt(props.item.end_at, auth.timezone) > props.item.work_date.slice(0, 10))
</script>

<template>
  <div class="py-3">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-[15px] font-medium">{{ formatDate(item.work_date) }}</p>
        <p class="mt-0.5 text-[13px] tabular-nums text-hint">
          {{ formatClock(item.start_at, auth.timezone) }} – {{ formatClock(item.end_at, auth.timezone) }}<template v-if="nextDay"> (+1)</template>
          · {{ formatMinutes(item.minutes) }}
        </p>
      </div>
      <StatusBadge v-bind="leaveStatus[item.status]" />
    </div>
    <p class="mt-1.5 line-clamp-2 text-sm">{{ item.reason }}</p>
    <p v-if="item.review_note" class="mt-1 text-[13px] text-hint">
      {{ item.reviewer?.full_name ? `${item.reviewer.full_name}: ` : '' }}{{ item.review_note }}
    </p>
    <slot />
  </div>
</template>
