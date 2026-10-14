<script setup lang="ts">
import type { DayRow } from '~/types/api'
import { confirmationStatus, dayStatus, formatClock, formatDate, formatMinutes, todayISOAt } from '~/utils/format'

defineProps<{ row: DayRow; timezone: string; showName?: boolean }>()
</script>

<template>
  <div class="flex items-center gap-3 py-3">
    <div class="min-w-0 flex-1">
      <p class="truncate text-[15px] font-medium">{{ showName ? row.full_name : formatDate(row.date) }}</p>
      <p class="mt-0.5 text-[13px] tabular-nums text-hint">
        <template v-if="row.check_in_at">
          <template v-if="row.check_out_at">
            {{ formatClock(row.check_in_at, timezone) }} – {{ formatClock(row.check_out_at, timezone) }}<template v-if="todayISOAt(row.check_out_at, timezone) > row.date.slice(0, 10)"> (+1)</template> · {{ formatMinutes(row.working_minutes) }}
          </template>
          <template v-else>In {{ formatClock(row.check_in_at, timezone) }}</template>
          <template v-if="row.late_minutes"> · late {{ formatMinutes(row.late_minutes) }}</template>
          <template v-if="row.leave_half"> · {{ row.leave_type }}</template>
        </template>
        <template v-else-if="row.leave_type">{{ row.leave_type }}</template>
        <template v-else-if="showName">{{ row.employee_code }}</template>
      </p>
      <p v-if="row.check_in_reason || row.check_out_reason" class="mt-0.5 truncate text-[12px] text-hint">
        “{{ [row.check_in_reason, row.check_out_reason].filter(Boolean).join(' · ') }}”
      </p>
    </div>
    <div class="flex flex-col items-end gap-1">
      <StatusBadge v-bind="dayStatus[row.status]" />
      <StatusBadge
        v-if="row.check_in_at && row.confirmation_status && row.confirmation_status !== 'CONFIRMED'"
        v-bind="confirmationStatus[row.confirmation_status]"
      />
    </div>
  </div>
</template>
