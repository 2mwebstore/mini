<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import type { PendingAttendance } from '~/types/api'
import { formatClock, formatDate, formatMinutes, typeLabel } from '~/utils/format'

const props = defineProps<{ item: PendingAttendance; timezone: string; busy?: boolean }>()
defineEmits<{ confirm: []; reject: [] }>()
const at = computed(() => (props.item.type === 'CHECK_IN' ? props.item.check_in_at : props.item.check_out_at))
</script>

<template>
  <article class="card flex flex-col overflow-hidden">
    <a
      v-if="item.photo_id"
      :href="attendanceService.photoUrl(item.photo_id)"
      target="_blank"
      rel="noopener"
      class="block aspect-[4/3] bg-surface-2"
      :aria-label="`Open ${item.full_name}'s photo`"
    >
      <img :src="attendanceService.photoUrl(item.photo_id)" :alt="`${item.full_name} ${typeLabel(item.type)} photo`" class="size-full object-cover" loading="lazy" />
    </a>
    <div class="flex flex-1 flex-col gap-3 p-4">
      <div class="flex items-start justify-between gap-2">
        <div class="min-w-0">
          <NuxtLink :to="`/attendance/${item.attendance_id}`" class="block truncate font-medium hover:underline">{{ item.full_name }}</NuxtLink>
          <p class="text-xs text-muted">{{ item.employee_code }} · {{ formatDate(item.work_date, true) }}</p>
        </div>
        <StatusBadge :tone="item.type === 'CHECK_IN' ? 'success' : 'info'" :label="typeLabel(item.type)" />
      </div>
      <p class="text-sm">
        <span class="text-lg font-semibold tabular-nums">{{ formatClock(at, timezone) }}</span>
        <span v-if="item.type === 'CHECK_IN' && item.late_minutes" class="ml-2 text-warning">late {{ formatMinutes(item.late_minutes) }}</span>
      </p>
      <div class="mt-auto grid grid-cols-2 gap-2">
        <AppButton variant="danger" :disabled="busy" @click="$emit('reject')">Reject</AppButton>
        <AppButton variant="success" :loading="busy" @click="$emit('confirm')">Confirm</AppButton>
      </div>
    </div>
  </article>
</template>
