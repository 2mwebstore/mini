<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { attendanceService } from '~/services/attendance.service'
import { leaveService } from '~/services/leave.service'
import { useAuthStore } from '~/stores/auth'
import type { LeaveRequest, PendingAttendance } from '~/types/api'
import { formatDate, formatDateRange } from '~/utils/format'

definePageMeta({ permission: ['team.review', 'all.review'] })

const auth = useAuthStore()
const queue = useResource(async () => {
  const [attendance, leave] = await Promise.all([attendanceService.pending(), leaveService.pending()])
  return { attendance, leave }
})
const { busy, error, run } = useAction()

function drop(kind: 'attendance' | 'leave', id: number) {
  const d = queue.data.value
  if (!d) return
  queue.data.value =
    kind === 'attendance' ? { ...d, attendance: d.attendance.filter(a => a.confirmation_id !== id) } : { ...d, leave: d.leave.filter(l => l.id !== id) }
}

async function act(kind: 'attendance' | 'leave', id: number, fn: () => Promise<unknown>, success: string) {
  const ok = await run(`${kind}${id}`, fn, success)
  // Done, or already handled by someone else / re-submitted: remove from the queue.
  const st = (error.value as ApiError | null)?.status
  if (ok || st === 409 || st === 404) drop(kind, id)
}

const confirm = (a: PendingAttendance) => act('attendance', a.confirmation_id, () => attendanceService.confirm(a.confirmation_id), `Confirmed ${a.full_name}`)
const approve = (l: LeaveRequest) => act('leave', l.id, () => leaveService.approve(l.id), `Approved leave for ${l.employee?.full_name}`)

const rejecting = ref<{ kind: 'attendance'; item: PendingAttendance } | { kind: 'leave'; item: LeaveRequest } | null>(null)
const rejectOpen = computed({ get: () => !!rejecting.value, set: v => !v && (rejecting.value = null) })
const presets = ['Please provide a valid attendance photo.', 'Not at the workplace.', 'Wrong time or duplicate submission.']

async function reject(reason: string) {
  const r = rejecting.value
  if (!r) return
  if (r.kind === 'attendance') {
    await act('attendance', r.item.confirmation_id, () => attendanceService.reject(r.item.confirmation_id, reason), 'Attendance rejected')
  } else {
    await act('leave', r.item.id, () => leaveService.reject(r.item.id, reason), 'Leave rejected')
  }
  rejecting.value = null
}
</script>

<template>
  <div>
    <PageHeader title="Reviews" subtitle="Check-ins, check-outs and leave requests waiting for you.">
      <AppButton variant="secondary" @click="queue.load">Refresh</AppButton>
    </PageHeader>

    <StateView
      :loading="queue.loading.value && !queue.data.value"
      :error="queue.error.value"
      :empty="queue.data.value?.attendance.length === 0 && queue.data.value?.leave.length === 0"
      empty-text="All caught up — nothing is waiting for review."
      @retry="queue.load"
    >
      <template v-if="queue.data.value">
        <section v-if="queue.data.value.attendance.length" class="mb-8">
          <h2 class="mb-3 text-sm font-semibold">Attendance ({{ queue.data.value.attendance.length }})</h2>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <PendingAttendanceCard
              v-for="a in queue.data.value.attendance"
              :key="a.confirmation_id"
              :item="a"
              :timezone="auth.company.timezone"
              :busy="busy === `attendance${a.confirmation_id}`"
              @confirm="confirm(a)"
              @reject="rejecting = { kind: 'attendance', item: a }"
            />
          </div>
        </section>

        <section v-if="queue.data.value.leave.length">
          <h2 class="mb-3 text-sm font-semibold">Leave requests ({{ queue.data.value.leave.length }})</h2>
          <div class="card overflow-x-auto">
            <table class="table">
              <thead><tr><th>Employee</th><th>Type</th><th>Dates</th><th>Days</th><th>Reason</th><th>Requested</th><th /></tr></thead>
              <tbody>
                <tr v-for="l in queue.data.value.leave" :key="l.id">
                  <td><span class="font-medium">{{ l.employee?.full_name }}</span><span class="block text-xs text-muted">{{ l.employee?.employee_code }}</span></td>
                  <td>{{ l.leave_type?.name }}</td>
                  <td class="whitespace-nowrap">{{ formatDateRange(l.start_date, l.end_date) }}</td>
                  <td class="tabular-nums">{{ l.days }}</td>
                  <td class="max-w-xs"><p class="line-clamp-2">{{ l.reason }}</p></td>
                  <td class="whitespace-nowrap text-muted">{{ formatDate(l.created_at) }}</td>
                  <td>
                    <div class="flex justify-end gap-2">
                      <AppButton size="sm" variant="danger" :disabled="busy === `leave${l.id}`" @click="rejecting = { kind: 'leave', item: l }">Reject</AppButton>
                      <AppButton size="sm" variant="success" :loading="busy === `leave${l.id}`" @click="approve(l)">Approve</AppButton>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </StateView>

    <RejectModal
      v-model="rejectOpen"
      :title="rejecting?.kind === 'leave' ? `Reject leave — ${rejecting.item.employee?.full_name}` : `Reject attendance — ${rejecting?.item.full_name ?? ''}`"
      :presets="rejecting?.kind === 'attendance' ? presets : []"
      :required="rejecting?.kind === 'attendance'"
      :loading="!!busy"
      @submit="reject"
    />
  </div>
</template>
