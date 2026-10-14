<script setup lang="ts">
import type { TodayStatus } from '~/types/api'
import { dayStatus, formatClock, formatDate, halfLabels, todayISO } from '~/utils/format'

// Today at a glance; the page puts the check-in and check-out tiles in the default slot.
const props = defineProps<{ today: TodayStatus; timezone: string }>()

const att = computed(() => props.today.attendance)
// While a night shift that started yesterday runs, "today" is that shift's date.
const isCalendarToday = computed(() => props.today.date.slice(0, 10) === todayISO(props.timezone))

// Live clock in the company timezone (the one late/early is measured in).
const now = ref(new Date().toISOString())
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => (timer = setInterval(() => (now.value = new Date().toISOString()), 15_000)))
onBeforeUnmount(() => clearInterval(timer))

// An approved half day: which half is off (the schedule shown is the other half).
const halfOff = computed(() => {
  const h = props.today.half_leave
  if (!h) return ''
  const names = halfLabels({ work_type: h.work_type, first_half: [h.shift_start, ''] })
  const part = h.day_type === 'FIRST_HALF' ? names[0].label : names[1].label
  return `${part} off (${h.from}–${h.to})${h.leave_type ? ` · ${h.leave_type}` : ''}`
})

// The latest rejection the employee still has to act on.
const rejection = computed(() => {
  const confs = (att.value?.confirmations ?? []).filter(c => c.status !== 'SUPERSEDED')
  const latest = confs.sort((a, b) => b.id - a.id)[0]
  return latest?.status === 'REJECTED' ? latest : null
})
</script>

<template>
  <section class="rounded-3xl bg-surface p-4 shadow-sm min-[360px]:p-5" aria-label="Today">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-[13px] text-hint">{{ formatDate(today.date, 'long') }}</p>
        <p class="mt-1 text-[40px] font-bold leading-none tracking-tight tabular-nums">{{ formatClock(now, timezone) }}</p>
      </div>
      <StatusBadge v-if="att" class="mt-0.5" v-bind="dayStatus[att.status]" />
    </div>

    <p class="mt-3 inline-flex max-w-full items-center gap-1.5 rounded-full bg-page px-3 py-1.5 text-[13px] text-hint">
      <AppIcon :name="today.holiday ? 'party' : !today.work_day ? 'coffee' : today.cross_day ? 'moon' : 'clock'" class="size-4 shrink-0" />
      <span class="truncate">
        <template v-if="today.holiday">Holiday · {{ today.holiday }}</template>
        <template v-else-if="!today.work_day">Day off</template>
        <template v-else>
          {{ today.shift_name || 'Schedule' }} {{ today.schedule_start }} – {{ today.schedule_end }}<template v-if="today.cross_day"> (+1 day)</template>
        </template>
      </span>
    </p>

    <p v-if="today.special" class="mt-2 flex items-start gap-1.5 rounded-xl bg-warning/10 px-3 py-2 text-[13px] text-warning" role="note">
      <AppIcon name="star" class="mt-px size-4 shrink-0" />
      <span>Special schedule: {{ today.special }}</span>
    </p>
    <p v-if="halfOff" class="mt-2 flex items-start gap-1.5 rounded-xl bg-info/10 px-3 py-2 text-[13px] text-info" role="note">
      <AppIcon name="leave" class="mt-px size-4 shrink-0" />
      <span>Half-day leave: {{ halfOff }}</span>
    </p>
    <p v-if="!isCalendarToday" class="mt-2 text-[13px] text-hint">Night shift that started {{ formatDate(today.date, 'long') }}.</p>

    <div v-if="rejection" class="mt-3 rounded-2xl bg-danger/10 p-3 text-sm text-danger" role="status">
      <p class="font-medium">Your {{ rejection.type === 'CHECK_IN' ? 'check-in' : 'check-out' }} was rejected.</p>
      <p v-if="rejection.reason" class="mt-0.5">Reason: {{ rejection.reason }}</p>
      <p class="mt-0.5">Please submit it again.</p>
    </div>

    <div v-if="$slots.default" class="mt-4">
      <slot />
    </div>
  </section>
</template>
