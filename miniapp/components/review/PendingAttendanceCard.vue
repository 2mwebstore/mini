<script setup lang="ts">
import type { PendingAttendance } from '~/types/api'
import { formatClock, formatDate, formatMinutes } from '~/utils/format'

const props = defineProps<{ item: PendingAttendance; timezone: string; busy?: boolean }>()
defineEmits<{ confirm: []; reject: [] }>()

const isIn = computed(() => props.item.type === 'CHECK_IN')
const at = computed(() => (isIn.value ? props.item.check_in_at : props.item.check_out_at))
</script>

<template>
  <article class="rounded-2xl bg-surface p-3.5">
    <div class="flex gap-3">
      <div class="w-24 shrink-0">
        <AuthPhoto zoomable :photo-id="item.photo_id" :alt="`${item.full_name} ${isIn ? 'check-in' : 'check-out'} photo`" />
      </div>
      <div class="flex min-w-0 flex-1 flex-col">
        <div class="flex items-start justify-between gap-2">
          <p class="truncate text-[15px] font-semibold">{{ item.full_name }}</p>
          <StatusBadge :tone="isIn ? 'success' : 'info'" :label="isIn ? 'Check-in' : 'Check-out'" />
        </div>
        <p class="truncate text-[13px] text-hint">{{ item.employee_code }} · {{ formatDate(item.work_date) }}</p>
        <p class="mt-auto pt-2 text-[28px] font-bold leading-none tabular-nums">{{ formatClock(at, timezone) }}</p>
        <p v-if="isIn && item.late_minutes" class="mt-1 text-[13px] font-medium text-warning">Late {{ formatMinutes(item.late_minutes) }}</p>
        <p v-else class="mt-1 text-[13px] text-hint">{{ isIn ? 'On time' : 'Checked out' }}</p>
      </div>
    </div>
    <div class="mt-3 grid grid-cols-2 gap-2">
      <AppButton variant="danger" :disabled="busy" @click="$emit('reject')">Reject</AppButton>
      <AppButton variant="success" :loading="busy" @click="$emit('confirm')">Confirm</AppButton>
    </div>
  </article>
</template>
