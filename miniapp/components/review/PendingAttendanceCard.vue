<script setup lang="ts">
import type { PendingAttendance } from '~/types/api'
import { formatClock, formatDate, formatMinutes } from '~/utils/format'

const props = defineProps<{ item: PendingAttendance; timezone: string; busy?: boolean }>()
defineEmits<{ confirm: []; reject: [] }>()

const isIn = computed(() => props.item.type === 'CHECK_IN')
const at = computed(() => (isIn.value ? props.item.check_in_at : props.item.check_out_at))
const initial = computed(() => props.item.full_name.trim().charAt(0).toUpperCase())
// Check-ins have no photo: where the person was is what the reviewer goes on.
const place = computed(() => {
  const i = props.item
  if (i.distance_meters == null) return { text: i.accuracy != null ? 'Location shared' : 'No location shared', tone: 'text-hint' }
  const m = `${Math.round(i.distance_meters)} m from the office`
  return i.inside_office ? { text: `Inside · ${m}`, tone: 'text-success' } : { text: `Outside · ${m}`, tone: 'text-warning' }
})
const timing = computed(() => {
  const i = props.item
  if (isIn.value) return i.late_minutes ? { text: `Late ${formatMinutes(i.late_minutes)}`, tone: 'text-warning' } : { text: 'On time', tone: 'text-hint' }
  return i.early_leave_minutes ? { text: `Left ${formatMinutes(i.early_leave_minutes)} early`, tone: 'text-warning' } : { text: 'Checked out', tone: 'text-hint' }
})
</script>

<template>
  <article class="rounded-2xl bg-surface p-3.5">
    <div class="flex gap-3">
      <div v-if="item.photo_id" class="w-24 shrink-0">
        <AuthPhoto zoomable :photo-id="item.photo_id" :alt="`${item.full_name} ${isIn ? 'check-in' : 'check-out'} photo`" />
      </div>
      <span v-else class="flex size-12 shrink-0 items-center justify-center rounded-full bg-primary text-lg font-semibold text-primary-fg" aria-hidden="true">{{ initial }}</span>
      <div class="flex min-w-0 flex-1 flex-col">
        <div class="flex items-start justify-between gap-2">
          <p class="truncate text-[15px] font-semibold">{{ item.full_name }}</p>
          <StatusBadge :tone="isIn ? 'success' : 'info'" :label="isIn ? 'Check-in' : 'Check-out'" />
        </div>
        <p class="truncate text-[13px] text-hint">{{ item.employee_code }} · {{ formatDate(item.work_date) }}</p>
        <p class="mt-auto pt-2 text-[28px] font-bold leading-none tabular-nums">{{ formatClock(at, timezone) }}</p>
        <p class="mt-1 text-[13px] font-medium" :class="timing.tone">{{ timing.text }}</p>
        <p class="mt-0.5 flex items-center gap-1 text-[13px]" :class="place.tone">
          <AppIcon name="pin" class="size-3.5 shrink-0" /><span class="truncate">{{ place.text }}</span>
        </p>
      </div>
    </div>
    <div class="mt-3 grid grid-cols-2 gap-2">
      <AppButton variant="danger" :disabled="busy" @click="$emit('reject')">Reject</AppButton>
      <AppButton variant="success" :loading="busy" @click="$emit('confirm')">Confirm</AppButton>
    </div>
  </article>
</template>
