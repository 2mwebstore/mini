<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { reviewService } from '~/services/review.service'
import { useAuthStore } from '~/stores/auth'
import type { LeaveRequest, OvertimeRequest, PendingAttendance } from '~/types/api'

definePageMeta({ middleware: 'reviewer' })

const auth = useAuthStore()
const tab = ref<'review' | 'today'>('review')

// ---------- review queue ----------
const queue = useResource(async () => {
  const [attendance, leave, overtime] = await Promise.all([reviewService.pendingAttendance(), reviewService.pendingLeave(), reviewService.pendingOvertime()])
  return { attendance, leave, overtime }
})
const pendingCount = computed(() => (queue.data.value ? queue.data.value.attendance.length + queue.data.value.leave.length + queue.data.value.overtime.length : 0))
const tabs = computed(() => [
  { value: 'review', label: queue.data.value ? `Review (${pendingCount.value})` : 'Review' },
  { value: 'today', label: 'Today' },
])

const busy = ref<string | null>(null)

type Kind = 'attendance' | 'leave' | 'overtime'
function drop(kind: Kind, id: number) {
  const d = queue.data.value
  if (!d) return
  queue.data.value =
    kind === 'attendance' ? { ...d, attendance: d.attendance.filter(a => a.confirmation_id !== id) }
      : kind === 'leave' ? { ...d, leave: d.leave.filter(l => l.id !== id) }
        : { ...d, overtime: d.overtime.filter(o => o.id !== id) }
}

async function act(key: string, kind: Kind, id: number, call: () => Promise<unknown>) {
  busy.value = key
  try {
    await call()
    haptic('success')
    drop(kind, id)
  } catch (e) {
    haptic('error')
    const err = e as ApiError
    await alertDialog(err.message)
    // Someone else already handled it (or the employee re-submitted): take it off the list.
    if (err.status === 409 || err.status === 404) drop(kind, id)
  } finally {
    busy.value = null
  }
}

const confirmAttendance = (a: PendingAttendance) =>
  act(`a${a.confirmation_id}`, 'attendance', a.confirmation_id, () => reviewService.confirmAttendance(a.confirmation_id))
const approveLeave = (l: LeaveRequest) => act(`l${l.id}`, 'leave', l.id, () => reviewService.approveLeave(l.id))
const approveOvertime = (o: OvertimeRequest) => act(`o${o.id}`, 'overtime', o.id, () => reviewService.approveOvertime(o.id))

const rejecting = ref<{ kind: 'attendance'; item: PendingAttendance } | { kind: 'leave'; item: LeaveRequest } | { kind: 'overtime'; item: OvertimeRequest } | null>(null)
const sheetOpen = computed({
  get: () => !!rejecting.value,
  set: v => {
    if (!v) rejecting.value = null
  },
})
const attendancePresets = ['Not at the workplace.', 'Location does not match the workplace.', 'Wrong time or duplicate submission.']
const overtimePresets = ['Not needed this time.', 'Please plan it with me first.', 'Too many hours this week.']

async function reject(reason: string) {
  const r = rejecting.value
  if (!r) return
  if (r.kind === 'attendance') {
    await act(`a${r.item.confirmation_id}`, 'attendance', r.item.confirmation_id, () => reviewService.rejectAttendance(r.item.confirmation_id, reason))
  } else if (r.kind === 'leave') {
    await act(`l${r.item.id}`, 'leave', r.item.id, () => reviewService.rejectLeave(r.item.id, reason))
  } else {
    await act(`o${r.item.id}`, 'overtime', r.item.id, () => reviewService.rejectOvertime(r.item.id, reason))
  }
  rejecting.value = null
}

// ---------- today ----------
const daily = useResource(() => reviewService.daily(), { immediate: false })
watch(tab, t => {
  if (t === 'today' && !daily.data.value) daily.load()
})
const tiles = computed(() => {
  const c = daily.data.value?.counts
  if (!c) return []
  return [
    { label: 'Confirmed', value: c.Confirmed, tone: 'text-success' },
    { label: 'Pending', value: c.Pending, tone: 'text-warning' },
    { label: 'Not in yet', value: c.NotYet, tone: 'text-hint' },
    { label: 'Absent', value: c.Absent, tone: 'text-danger' },
    { label: 'On leave', value: c.Leave, tone: 'text-info' },
    { label: 'Late', value: c.Late, tone: 'text-warning' },
  ]
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <header>
      <h1 class="text-2xl font-bold">Team</h1>
      <p v-if="auth.me?.role === 'branch_admin' && auth.me.branch_name" class="text-[13px] text-hint">{{ auth.me.branch_name }}</p>
    </header>
    <Segmented v-model="tab" :options="tabs" />

    <template v-if="tab === 'review'">
      <StateView
        :loading="queue.loading.value && !queue.data.value"
        :error="queue.error.value"
        :empty="pendingCount === 0"
        empty-text="All caught up — nothing is waiting for review."
        @retry="queue.load"
      >
        <section v-if="queue.data.value?.attendance.length" aria-labelledby="att-h">
          <h2 id="att-h" class="mb-1.5 px-4 text-[13px] font-medium uppercase tracking-wide text-hint">Attendance</h2>
          <div class="flex flex-col gap-3">
            <PendingAttendanceCard
              v-for="a in queue.data.value.attendance"
              :key="a.confirmation_id"
              :item="a"
              :timezone="auth.timezone"
              :busy="busy === `a${a.confirmation_id}`"
              @confirm="confirmAttendance(a)"
              @reject="rejecting = { kind: 'attendance', item: a }"
            />
          </div>
        </section>
        <section v-if="queue.data.value?.leave.length" class="mt-4" aria-labelledby="leave-h">
          <h2 id="leave-h" class="mb-1.5 px-4 text-[13px] font-medium uppercase tracking-wide text-hint">Leave requests</h2>
          <div class="flex flex-col gap-3">
            <PendingLeaveCard
              v-for="l in queue.data.value.leave"
              :key="l.id"
              :leave="l"
              :busy="busy === `l${l.id}`"
              @approve="approveLeave(l)"
              @reject="rejecting = { kind: 'leave', item: l }"
            />
          </div>
        </section>
        <section v-if="queue.data.value?.overtime.length" class="mt-4" aria-labelledby="ot-h">
          <h2 id="ot-h" class="mb-1.5 px-4 text-[13px] font-medium uppercase tracking-wide text-hint">Overtime requests</h2>
          <div class="flex flex-col gap-3">
            <PendingOvertimeCard
              v-for="o in queue.data.value.overtime"
              :key="o.id"
              :item="o"
              :timezone="auth.timezone"
              :busy="busy === `o${o.id}`"
              @approve="approveOvertime(o)"
              @reject="rejecting = { kind: 'overtime', item: o }"
            />
          </div>
        </section>
      </StateView>
    </template>

    <template v-else>
      <StateView :loading="daily.loading.value" :error="daily.error.value" :empty="daily.data.value?.rows.length === 0" empty-text="No staff in your scope today." @retry="daily.load">
        <template v-if="daily.data.value">
          <div class="grid grid-cols-3 gap-2">
            <div v-for="t in tiles" :key="t.label" class="rounded-2xl bg-surface px-3 py-3 text-center">
              <p class="text-xl font-semibold tabular-nums" :class="t.tone">{{ t.value }}</p>
              <p class="text-xs text-hint">{{ t.label }}</p>
            </div>
          </div>
          <AppCard :title="`${daily.data.value.counts.Staff} staff`" class="mt-4">
            <div class="-my-3 divide-y divide-separator">
              <DayRowItem v-for="r in daily.data.value.rows" :key="r.employee_id" :row="r" :timezone="auth.timezone" show-name />
            </div>
          </AppCard>
        </template>
      </StateView>
    </template>

    <RejectSheet
      v-model="sheetOpen"
      :title="rejecting?.kind === 'leave' ? 'Reject leave request' : rejecting?.kind === 'overtime' ? 'Reject overtime' : 'Reject attendance'"
      :presets="rejecting?.kind === 'leave' ? [] : rejecting?.kind === 'overtime' ? overtimePresets : attendancePresets"
      :required="rejecting?.kind === 'attendance'"
      :loading="!!busy"
      @submit="reject"
    />
  </div>
</template>
