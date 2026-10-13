<script setup lang="ts">
import type { TodayStatus } from '~/types/api'
import { confirmationStatus, dayStatus, formatClock, formatDate, formatMinutes } from '~/utils/format'
import type { IconName } from '~/utils/icons'

// Today at a glance; the page puts the check-in/out action in the default slot.
const props = defineProps<{ today: TodayStatus; timezone: string }>()

const att = computed(() => props.today.attendance)
const recorded = computed(() => !!att.value?.check_in_at)

// Live clock in the company timezone (the one late/early is measured in).
const now = ref(new Date().toISOString())
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => (timer = setInterval(() => (now.value = new Date().toISOString()), 15_000)))
onBeforeUnmount(() => clearInterval(timer))

const figures = computed((): { label: string; value: string; icon: IconName; tone: string }[] => [
  { label: 'Check in', value: formatClock(att.value?.check_in_at, props.timezone), icon: 'in', tone: 'text-success' },
  { label: 'Check out', value: formatClock(att.value?.check_out_at, props.timezone), icon: 'out', tone: 'text-accent' },
  { label: 'Worked', value: att.value?.check_out_at ? formatMinutes(att.value.working_minutes) : '—', icon: 'timer', tone: 'text-hint' },
])

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
      <StatusBadge v-if="att && recorded" class="mt-0.5" v-bind="confirmationStatus[att.confirmation_status]" />
      <StatusBadge v-else-if="att" class="mt-0.5" v-bind="dayStatus[att.status]" />
    </div>

    <p class="mt-3 inline-flex max-w-full items-center gap-1.5 rounded-full bg-page px-3 py-1.5 text-[13px] text-hint">
      <AppIcon :name="today.holiday ? 'party' : today.work_day ? 'clock' : 'coffee'" class="size-4 shrink-0" />
      <span class="truncate">
        <template v-if="today.holiday">Holiday · {{ today.holiday }}</template>
        <template v-else-if="!today.work_day">Day off</template>
        <template v-else>Schedule {{ today.schedule_start }} – {{ today.schedule_end }}</template>
      </span>
    </p>

    <dl class="mt-4 grid grid-cols-3 divide-x divide-separator rounded-2xl bg-page py-3">
      <div v-for="f in figures" :key="f.label" class="flex min-w-0 flex-col items-center gap-1 px-1">
        <dt class="flex flex-col items-center gap-1 whitespace-nowrap text-xs text-hint">
          <AppIcon :name="f.icon" class="size-4 shrink-0" :class="f.tone" />{{ f.label }}
        </dt>
        <dd class="truncate text-lg font-semibold tabular-nums">{{ f.value }}</dd>
      </div>
    </dl>

    <div v-if="att && (att.late_minutes || att.early_leave_minutes)" class="mt-3 flex flex-wrap gap-2">
      <StatusBadge v-if="att.late_minutes" tone="warning" :label="`Late ${formatMinutes(att.late_minutes)}`" />
      <StatusBadge v-if="att.early_leave_minutes" tone="warning" :label="`Left ${formatMinutes(att.early_leave_minutes)} early`" />
    </div>

    <div v-if="rejection" class="mt-3 rounded-2xl bg-danger/10 p-3 text-sm text-danger" role="status">
      <p class="font-medium">Your {{ rejection.type === 'CHECK_IN' ? 'check-in' : 'check-out' }} was rejected.</p>
      <p v-if="rejection.reason" class="mt-0.5">Reason: {{ rejection.reason }}</p>
      <p class="mt-0.5">Please submit it again with a new photo.</p>
    </div>

    <div v-if="$slots.default" class="mt-4">
      <slot />
    </div>
  </section>
</template>
