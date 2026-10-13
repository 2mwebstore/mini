<script setup lang="ts">
import type { TodayStatus } from '~/types/api'
import { confirmationStatus, dayStatus, formatClock, formatDate, formatMinutes } from '~/utils/format'

const props = defineProps<{ today: TodayStatus; timezone: string }>()

const att = computed(() => props.today.attendance)
const recorded = computed(() => !!att.value?.check_in_at)

// The latest rejection the employee still has to act on.
const rejection = computed(() => {
  const confs = (att.value?.confirmations ?? []).filter(c => c.status !== 'SUPERSEDED')
  const latest = confs.sort((a, b) => b.id - a.id)[0]
  return latest?.status === 'REJECTED' ? latest : null
})
</script>

<template>
  <div class="rounded-2xl bg-surface p-4">
    <div class="flex items-start justify-between gap-3">
      <div>
        <p class="text-[13px] text-hint">{{ formatDate(today.date, 'long') }}</p>
        <p class="mt-0.5 text-[15px]">
          <template v-if="today.holiday">🎉 {{ today.holiday }}</template>
          <template v-else-if="!today.work_day">Day off</template>
          <template v-else>Schedule {{ today.schedule_start }} – {{ today.schedule_end }}</template>
        </p>
      </div>
      <StatusBadge
        v-if="att && recorded"
        v-bind="confirmationStatus[att.confirmation_status]"
      />
      <StatusBadge v-else-if="att" v-bind="dayStatus[att.status]" />
    </div>

    <div class="mt-4 grid grid-cols-3 gap-2 text-center">
      <div class="rounded-xl bg-page px-2 py-3">
        <p class="text-xs text-hint">Check in</p>
        <p class="mt-1 text-lg font-semibold tabular-nums">{{ formatClock(att?.check_in_at, timezone) }}</p>
      </div>
      <div class="rounded-xl bg-page px-2 py-3">
        <p class="text-xs text-hint">Check out</p>
        <p class="mt-1 text-lg font-semibold tabular-nums">{{ formatClock(att?.check_out_at, timezone) }}</p>
      </div>
      <div class="rounded-xl bg-page px-2 py-3">
        <p class="text-xs text-hint">Worked</p>
        <p class="mt-1 text-lg font-semibold tabular-nums">{{ att?.check_out_at ? formatMinutes(att.working_minutes) : '—' }}</p>
      </div>
    </div>

    <div v-if="att && (att.late_minutes || att.early_leave_minutes)" class="mt-3 flex flex-wrap gap-2">
      <StatusBadge v-if="att.late_minutes" tone="warning" :label="`Late ${formatMinutes(att.late_minutes)}`" />
      <StatusBadge v-if="att.early_leave_minutes" tone="warning" :label="`Left ${formatMinutes(att.early_leave_minutes)} early`" />
    </div>

    <div v-if="rejection" class="mt-3 rounded-xl bg-danger/10 p-3 text-sm text-danger" role="status">
      <p class="font-medium">Your {{ rejection.type === 'CHECK_IN' ? 'check-in' : 'check-out' }} was rejected.</p>
      <p v-if="rejection.reason" class="mt-0.5">Reason: {{ rejection.reason }}</p>
      <p class="mt-0.5">Please submit it again with a new photo.</p>
    </div>
  </div>
</template>
