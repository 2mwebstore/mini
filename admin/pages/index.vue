<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { leaveService } from '~/services/leave.service'
import { reportService } from '~/services/report.service'
import { useAuthStore } from '~/stores/auth'
import type { DayRow } from '~/types/api'
import { addDays, confirmationStatus, dayStatus, formatClock, formatDate, formatMinutes, nextDay, shiftHours, todayISO } from '~/utils/format'

// Pending check-ins can be a few weeks old: look back 90 days.
const pendingAttendanceLink = computed(() => `/attendance?confirmation=PENDING&from=${addDays(todayISO(useAuthStore().company.timezone), -90)}`)

const auth = useAuthStore()
const branchId = ref('')
const daily = useResource(() => reportService.daily(undefined, Number(branchId.value) || undefined))
watch(branchId, () => daily.load())
const pending = useResource(async () => {
  if (!auth.isReviewer) return null
  const [a, l] = await Promise.all([attendanceService.pending(), leaveService.pending()])
  return { attendance: a.length, leave: l.length }
})

const tiles = computed(() => {
  const c = daily.data.value?.counts
  if (!c) return []
  return [
    { label: 'Staff', value: c.Staff },
    { label: 'Confirmed', value: c.Confirmed, tone: 'success' as const },
    { label: 'Pending review', value: c.Pending, tone: 'warning' as const },
    { label: 'Not checked in', value: c.NotYet },
    { label: 'Absent', value: c.Absent, tone: 'danger' as const },
    { label: 'On leave', value: c.Leave, tone: 'info' as const },
    { label: 'Late', value: c.Late, tone: 'warning' as const },
    { label: 'No check-out', value: c.MissingOut },
  ]
})

// People who need a look today first, then everyone else.
const weight = (r: DayRow) =>
  r.status === 'NOT_CHECKED_IN' || r.status === 'ABSENT' ? 0 : r.confirmation_status === 'PENDING' || r.confirmation_status === 'REJECTED' ? 1 : r.late_minutes ? 2 : 3
const rows = computed(() => [...(daily.data.value?.rows ?? [])].sort((a, b) => weight(a) - weight(b) || a.full_name.localeCompare(b.full_name)))
const pager = usePager(rows) // who needs attention first
</script>

<template>
  <div>
    <PageHeader
      title="Today"
      :subtitle="[daily.data.value ? formatDate(daily.data.value.date, true) : '', auth.multiBranch ? '' : auth.employee?.branch?.name].filter(Boolean).join(' · ')"
    >
      <div class="w-48"><BranchFilter id="d-branch" v-model="branchId" /></div>
      <AppButton variant="secondary" @click="daily.load(); pending.load()">Refresh</AppButton>
    </PageHeader>

    <div
      v-if="pending.data.value && pending.data.value.attendance + pending.data.value.leave > 0"
      class="card mb-6 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-warning/40 bg-warning/5 px-4 py-3"
    >
      <span class="text-sm"><b>{{ pending.data.value.attendance + pending.data.value.leave }}</b> waiting for review</span>
      <span class="flex gap-4 text-sm font-medium text-warning">
        <NuxtLink v-if="pending.data.value.attendance" :to="pendingAttendanceLink" class="hover:underline">{{ pending.data.value.attendance }} attendance →</NuxtLink>
        <NuxtLink v-if="pending.data.value.leave" to="/leave?status=PENDING" class="hover:underline">{{ pending.data.value.leave }} leave →</NuxtLink>
      </span>
    </div>

    <StateView :loading="daily.loading.value && !daily.data.value" :error="daily.error.value" @retry="daily.load">
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatTile v-for="t in tiles" :key="t.label" v-bind="t" />
      </div>

      <section class="card mt-6 overflow-hidden">
        <h2 class="border-b border-border px-4 py-3 text-sm font-semibold">Staff today</h2>
        <p v-if="!rows.length" class="px-4 py-10 text-center text-sm text-muted">No staff scheduled today.</p>
        <div v-else class="overflow-x-auto">
          <table class="table">
            <thead>
              <tr><th>Employee</th><th>{{ auth.multiBranch ? 'Branch' : 'Department' }}</th><th>Shift</th><th>In</th><th>Out</th><th>Worked</th><th>Status</th><th>Review</th></tr>
            </thead>
            <tbody>
              <tr v-for="r in pager.rows.value" :key="r.employee_id">
                <td>
                  <NuxtLink v-if="r.attendance_id" :to="`/attendance/${r.attendance_id}`" class="font-medium hover:underline">{{ r.full_name }}</NuxtLink>
                  <span v-else class="font-medium">{{ r.full_name }}</span>
                  <span class="block text-xs text-muted">{{ r.employee_code }}</span>
                </td>
                <td class="text-muted">{{ (auth.multiBranch ? r.branch : r.department) || '—' }}</td>
                <td class="whitespace-nowrap text-xs">
                  <template v-if="r.shift_start">
                    <span class="block">{{ r.shift }}</span>
                    <span class="tabular-nums text-muted">{{ shiftHours(r.shift_start, r.shift_end, r.cross_day) }}</span>
                  </template>
                  <span v-else class="text-muted">—</span>
                </td>
                <td class="tabular-nums">{{ formatClock(r.check_in_at, auth.company.timezone) }}</td>
                <td class="tabular-nums">
                  {{ formatClock(r.check_out_at, auth.company.timezone) }}<sup v-if="nextDay(r.date, r.check_out_at, auth.company.timezone)" class="ml-0.5 text-[10px] text-muted">+1</sup>
                </td>
                <td class="tabular-nums">{{ formatMinutes(r.working_minutes) }}</td>
                <td>
                  <StatusBadge v-bind="dayStatus[r.status]" />
                  <span v-if="r.late_minutes" class="ml-1 text-xs text-warning">{{ formatMinutes(r.late_minutes) }}</span>
                </td>
                <td><StatusBadge v-if="r.check_in_at && r.confirmation_status" v-bind="confirmationStatus[r.confirmation_status]" /></td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination v-if="rows.length" :page="pager.page.value" :page-size="pager.pageSize.value" :total="pager.total.value" noun="person" @go="pager.go" @size="pager.resize" />
      </section>
    </StateView>
  </div>
</template>
