<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import { formatMinutes, formatMonth, shiftMonth, todayISO } from '~/utils/format'

const auth = useAuthStore()
const thisMonth = todayISO(auth.timezone).slice(0, 7)
const month = ref(thisMonth)

const { data, error, loading, load } = useResource(() => attendanceService.history(month.value))
watch(month, load)

const rows = computed(() => [...(data.value?.rows ?? [])].reverse())
const stats = computed(() => {
  const s = data.value?.summary
  if (!s) return []
  return [
    { label: 'Present', value: s.present_days, of: s.working_days },
    { label: 'Late', value: s.late_days },
    { label: 'Absent', value: s.absent_days },
    { label: 'Leave', value: s.leave_days },
    { label: 'Worked', value: formatMinutes(s.working_minutes) },
    { label: 'Avg / day', value: s.avg_minutes_per_day ? formatMinutes(s.avg_minutes_per_day) : '—' },
  ]
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <header class="flex items-center justify-between">
      <button class="rounded-full px-3 py-1 text-2xl text-link" aria-label="Previous month" @click="month = shiftMonth(month, -1)">‹</button>
      <h1 class="text-lg font-semibold">{{ formatMonth(month) }}</h1>
      <button
        class="rounded-full px-3 py-1 text-2xl text-link disabled:opacity-30"
        aria-label="Next month"
        :disabled="month >= thisMonth"
        @click="month = shiftMonth(month, 1)"
      >›</button>
    </header>

    <StateView :loading="loading" :error="error" :empty="!rows.length" empty-text="No attendance recorded this month." @retry="load">
      <div class="grid grid-cols-3 gap-2">
        <div v-for="s in stats" :key="s.label" class="rounded-2xl bg-surface px-3 py-3 text-center">
          <p class="text-lg font-semibold tabular-nums">
            {{ s.value }}<span v-if="s.of !== undefined" class="text-sm font-normal text-hint">/{{ s.of }}</span>
          </p>
          <p class="text-xs text-hint">{{ s.label }}</p>
        </div>
      </div>
      <AppCard title="Days" class="mt-4">
        <div class="-my-3 divide-y divide-separator">
          <DayRowItem v-for="r in rows" :key="r.date" :row="r" :timezone="auth.timezone" />
        </div>
      </AppCard>
    </StateView>
  </div>
</template>
