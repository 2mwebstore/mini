<script setup lang="ts">
import type { TodayStatus } from '~/types/api'
import { formatClock, formatMinutes } from '~/utils/format'

// One half of the day on the home screen: the check-in or the check-out, each with
// its own time, on-time/late status, review state and button.
const props = defineProps<{ kind: 'in' | 'out'; today: TodayStatus; timezone: string }>()
defineEmits<{ go: [] }>()

const isIn = computed(() => props.kind === 'in')
const att = computed(() => props.today.attendance)
const at = computed(() => (isIn.value ? att.value?.check_in_at : att.value?.check_out_at) ?? null)
const pre = computed(() => (isIn.value ? props.today.check_in : props.today.check_out))
const review = computed(() => {
  const type = isIn.value ? 'CHECK_IN' : 'CHECK_OUT'
  return (att.value?.confirmations ?? []).filter(c => c.type === type && c.status !== 'SUPERSEDED').sort((a, b) => b.id - a.id)[0] ?? null
})

const status = computed((): { text: string; tone: string } => {
  const a = att.value
  const t = props.today
  if (at.value && a) {
    if (isIn.value) return a.late_minutes ? { text: `Late ${formatMinutes(a.late_minutes)}`, tone: 'text-warning' } : { text: 'On time', tone: 'text-success' }
    return a.early_leave_minutes
      ? { text: `Left ${formatMinutes(a.early_leave_minutes)} early`, tone: 'text-warning' }
      : { text: `Worked ${formatMinutes(a.working_minutes)}`, tone: 'text-success' }
  }
  if (t.holiday || !t.work_day) return { text: t.holiday ? 'Holiday' : 'Day off', tone: 'text-hint' }
  return isIn.value
    ? { text: `Starts ${t.schedule_start}`, tone: 'text-hint' }
    : { text: `Ends ${t.schedule_end}${t.cross_day ? ' (+1)' : ''}`, tone: 'text-hint' }
})
const reviewBadge = computed(() => {
  switch (review.value?.status) {
    case 'PENDING': return { label: 'Waiting review', tone: 'warning' as const }
    case 'CONFIRMED': return { label: 'Confirmed', tone: 'success' as const }
    case 'REJECTED': return { label: 'Rejected', tone: 'danger' as const }
    default: return null
  }
})
const again = computed(() => review.value?.status === 'REJECTED')
const reason = computed(() => (isIn.value ? att.value?.check_in_reason : att.value?.check_out_reason) || '') // why late / early
const label = computed(() => (isIn.value ? 'Check in' : 'Check out'))
</script>

<template>
  <div class="flex min-w-0 flex-col rounded-2xl bg-page p-3" :aria-label="label" role="group">
    <p class="flex items-center gap-1.5 text-[13px] font-medium text-hint">
      <AppIcon :name="kind" class="size-4 shrink-0" :class="isIn ? 'text-success' : 'text-accent'" />{{ label }}
    </p>
    <p class="mt-1 text-[26px] font-bold leading-tight tabular-nums" :class="!at && 'text-hint/60'">{{ at ? formatClock(at, timezone) : '--:--' }}</p>
    <!-- Narrow phones: these wrap rather than cut off (“Worked 8h 23…”). -->
    <p class="text-[13px] font-medium leading-snug" :class="status.tone">{{ status.text }}</p>
    <p v-if="reason" class="line-clamp-2 break-words text-[12px] leading-snug text-hint" :title="reason">“{{ reason }}”</p>
    <div class="mb-2 mt-1 min-h-5">
      <StatusBadge v-if="reviewBadge" v-bind="reviewBadge" />
    </div>
    <AppButton
      v-if="pre.allowed"
      class="mt-auto whitespace-nowrap !gap-1.5 !px-2 py-2.5"
      block
      :variant="isIn ? 'success' : 'primary'"
      @click="$emit('go')"
    >
      <AppIcon :name="kind" class="size-4 shrink-0" />{{ again ? 'Again' : label }}
    </AppButton>
    <span v-else-if="!at" class="mt-auto rounded-xl bg-hint/10 px-1 py-2.5 text-center text-[13px] text-hint">
      {{ isIn ? 'Not available' : 'After check-in' }}
    </span>
  </div>
</template>
