<script setup lang="ts">
import type { PendingAttendance } from '~/types/api'
import { formatClock, formatDate, formatMinutes } from '~/utils/format'

const props = defineProps<{ item: PendingAttendance; timezone: string; busy?: boolean }>()
defineEmits<{ confirm: []; reject: [] }>()

const isIn = computed(() => props.item.type === 'CHECK_IN')
const at = computed(() => (isIn.value ? props.item.check_in_at : props.item.check_out_at))
</script>

<template>
  <article class="rounded-2xl bg-surface p-4">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="truncate text-[15px] font-semibold">{{ item.full_name }}</p>
        <p class="text-[13px] text-hint">{{ item.employee_code }} · {{ formatDate(item.work_date) }}</p>
      </div>
      <StatusBadge :tone="isIn ? 'success' : 'info'" :label="isIn ? 'Check-in' : 'Check-out'" />
    </div>
    <div class="mt-3 grid grid-cols-[1fr_1.2fr] gap-3">
      <AuthPhoto :photo-id="item.photo_id" :alt="`${item.full_name} ${isIn ? 'check-in' : 'check-out'} photo`" />
      <dl class="flex flex-col gap-2 text-sm">
        <div>
          <dt class="text-hint">Time</dt>
          <dd class="text-lg font-semibold tabular-nums">{{ formatClock(at, timezone) }}</dd>
        </div>
        <div v-if="isIn && item.late_minutes">
          <dt class="text-hint">Late</dt>
          <dd class="font-medium text-warning">{{ formatMinutes(item.late_minutes) }}</dd>
        </div>
      </dl>
    </div>
    <div class="mt-3 grid grid-cols-2 gap-2">
      <AppButton variant="danger" :disabled="busy" @click="$emit('reject')">Reject</AppButton>
      <AppButton variant="success" :loading="busy" @click="$emit('confirm')">Confirm</AppButton>
    </div>
  </article>
</template>
