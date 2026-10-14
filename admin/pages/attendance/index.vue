<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Attendance, AttendanceFilter } from '~/types/api'
import { confirmationStatus, dayStatus, formatClock, formatDate, formatMinutes, latestReview, monthStartISO, nextDay, todayISO } from '~/utils/format'
import { optionsFrom } from '~/utils/options'

const dayStatusOptions = optionsFrom(dayStatus, ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE'])
const manualStatusOptions = optionsFrom(dayStatus, ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE', 'HOLIDAY'])
const confirmationOptions = optionsFrom(confirmationStatus, ['PENDING', 'CONFIRMED', 'REJECTED'])

const auth = useAuthStore()
const lookups = useLookupsStore()
const route = useRoute()
const router = useRouter()
const tz = auth.company.timezone

// Filters live in the URL so a view can be bookmarked or shared.
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const filter = reactive({
  from: q('from') || monthStartISO(tz),
  to: q('to') || todayISO(tz),
  employee_id: q('employee_id'),
  department_id: q('department_id'),
  branch_id: q('branch_id'),
  status: q('status'),
  confirmation: q('confirmation'),
  page: Number(q('page')) || 1,
})
const query = computed<AttendanceFilter>(() => ({
  from: filter.from,
  to: filter.to,
  employee_id: Number(filter.employee_id) || undefined,
  department_id: Number(filter.department_id) || undefined,
  branch_id: Number(filter.branch_id) || undefined,
  status: filter.status || undefined,
  confirmation: filter.confirmation || undefined,
  page: filter.page,
  page_size: 50,
}))

const list = useResource(() => attendanceService.list(query.value))
watch(
  () => [filter.from, filter.to, filter.employee_id, filter.department_id, filter.branch_id, filter.status, filter.confirmation],
  () => (filter.page = 1),
)
watch(query, q => {
  router.replace({ query: Object.fromEntries(Object.entries(q).filter(([k, v]) => v !== undefined && k !== 'page_size').map(([k, v]) => [k, String(v)])) })
  list.load()
})

onMounted(() => {
  lookups.loadEmployees().catch(() => {})
  lookups.loadDepartments().catch(() => {})
})

// ---------- manual record ----------
const { busy, run } = useAction()
const addOpen = ref(false)
const manual = reactive({ employee_id: '', date: todayISO(tz), check_in: '', check_out: '', status: '', reason: '' })
watch(addOpen, v => v && Object.assign(manual, { employee_id: '', date: todayISO(tz), check_in: '', check_out: '', status: '', reason: '' }))
async function addRecord() {
  const ok = await run('add', () =>
    attendanceService.create({
      employee_id: Number(manual.employee_id),
      date: manual.date,
      check_in: manual.check_in || undefined,
      check_out: manual.check_out || undefined,
      status: manual.status || undefined,
      reason: manual.reason.trim(),
    }), 'Attendance recorded')
  if (ok) {
    addOpen.value = false
    list.load()
  }
}
// ---------- edit one record from the list ----------
const editing = ref<Attendance | null>(null)
const editOpen = computed({ get: () => !!editing.value, set: v => !v && (editing.value = null) })

// ---------- view one record ----------
const viewing = ref<number | null>(null)
function editFromView(id: number) {
  const a = list.data.value?.items.find(x => x.id === id)
  viewing.value = null
  if (a) editing.value = a
}
// Where a check-in/out was made, for the list's pin: inside / outside the office radius.
function placeOf(a: Attendance, type: 'CHECK_IN' | 'CHECK_OUT') {
  const r = latestReview(a.confirmations, type)
  const l = r?.location_id ? a.locations?.find(x => x.id === r.location_id) : undefined
  if (!l || l.inside_office == null) return null
  return { inside: l.inside_office, title: `${l.inside_office ? 'Inside' : 'Outside'} the office radius (${Math.round(l.distance_meters ?? 0)} m)` }
}

const activeEmployees = computed(() => (lookups.employees ?? []).filter(e => !e.deleted_at))
const activeEmployeeOptions = computed(() => activeEmployees.value.map(e => ({ value: String(e.id), label: e.full_name, sub: e.employee_code })))
</script>

<template>
  <div>
    <PageHeader title="Attendance" subtitle="Every check-in and check-out, each with its own status, location and review. View a day for the full record.">
      <AppButton v-if="auth.can('attendance.edit')" @click="addOpen = true">Add record</AppButton>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-3 lg:grid-cols-7">
      <FormField label="From" for="f-from"><input id="f-from" v-model="filter.from" type="date" class="input" /></FormField>
      <FormField label="To" for="f-to"><input id="f-to" v-model="filter.to" type="date" class="input" :min="filter.from" /></FormField>
      <FormField label="Employee" for="f-emp">
        <SearchSelect id="f-emp" v-model="filter.employee_id" :options="lookups.employeeOptions" empty-label="All" />
      </FormField>
      <BranchFilter id="f-branch" v-model="filter.branch_id" />
      <FormField label="Department" for="f-dept">
        <SearchSelect id="f-dept" v-model="filter.department_id" :options="lookups.departmentOptions" empty-label="All" />
      </FormField>
      <FormField label="Status" for="f-status">
        <SearchSelect id="f-status" v-model="filter.status" :options="dayStatusOptions" empty-label="All" />
      </FormField>
      <FormField label="Review" for="f-conf">
        <SearchSelect id="f-conf" v-model="filter.confirmation" :options="confirmationOptions" empty-label="All" />
      </FormField>
    </div>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.data.value?.items.length === 0" empty-text="No attendance matches these filters." @retry="list.load">
      <div v-if="list.data.value" class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Date</th><th>Employee</th><th>Check in</th><th>Check out</th><th>Worked</th><th>Status</th><th class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="a in list.data.value.items" :key="a.id" class="hover:bg-surface-2/60">
                <td class="whitespace-nowrap">{{ formatDate(a.work_date, true) }}</td>
                <td>
                  <span class="font-medium">{{ a.employee?.full_name }}</span>
                  <span class="block text-xs text-muted">{{ a.employee?.employee_code }}<template v-if="auth.can('all.view') && a.employee?.branch"> · {{ a.employee.branch.name }}</template></span>
                </td>
                <td v-for="t in (['CHECK_IN', 'CHECK_OUT'] as const)" :key="t" class="whitespace-nowrap">
                  <template v-for="at in [t === 'CHECK_IN' ? a.check_in_at : a.check_out_at]" :key="t">
                    <span v-if="!at" class="text-muted">—</span>
                    <template v-else>
                      <span class="inline-flex items-center gap-1 font-medium tabular-nums">
                        {{ formatClock(at, tz) }}<sup v-if="t === 'CHECK_OUT' && nextDay(a.work_date, at, tz)" class="text-[10px] text-muted" title="Next morning (night shift)">+1</sup>
                        <template v-for="p in [placeOf(a, t)]" :key="t">
                          <svg v-if="p" viewBox="0 0 24 24" class="size-3.5" :class="p.inside ? 'text-success' : 'text-warning'" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" role="img" :aria-label="p.title"><title>{{ p.title }}</title><path d="M12 21s-7-6.2-7-11.5A7 7 0 0 1 19 9.5C19 14.8 12 21 12 21zm0-9a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z" /></svg>
                        </template>
                      </span>
                      <span class="mt-0.5 flex items-center gap-1 text-xs">
                        <span v-if="t === 'CHECK_IN'" :class="a.late_minutes ? 'text-warning' : 'text-success'">{{ a.late_minutes ? `Late ${formatMinutes(a.late_minutes)}` : 'On time' }}</span>
                        <span v-else :class="a.early_leave_minutes ? 'text-warning' : 'text-success'">{{ a.early_leave_minutes ? `Early ${formatMinutes(a.early_leave_minutes)}` : 'On time' }}</span>
                        <template v-for="r in [latestReview(a.confirmations, t)]" :key="t">
                          <span v-if="r && r.status !== 'CONFIRMED'" class="text-muted">·</span>
                          <span v-if="r && r.status !== 'CONFIRMED'" :class="r.status === 'REJECTED' ? 'text-danger' : 'text-warning'">{{ confirmationStatus[r.status].label }}</span>
                        </template>
                      </span>
                    </template>
                  </template>
                </td>
                <td class="tabular-nums">{{ formatMinutes(a.working_minutes) }}</td>
                <td>
                  <StatusBadge v-bind="dayStatus[a.status]" />
                  <span v-if="a.check_in_at && a.confirmation_status !== 'CONFIRMED'" class="mt-0.5 block">
                    <StatusBadge v-bind="confirmationStatus[a.confirmation_status]" />
                  </span>
                </td>
                <td class="whitespace-nowrap text-right">
                  <div class="inline-flex items-center gap-1">
                    <AppButton v-if="auth.can('attendance.edit')" size="sm" variant="secondary" :aria-label="`Edit ${a.employee?.full_name} ${formatDate(a.work_date)}`" @click="editing = a">
                      <svg viewBox="0 0 24 24" class="size-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" /></svg>
                      Edit
                    </AppButton>
                    <AppButton size="sm" variant="ghost" :aria-label="`View ${a.employee?.full_name} ${formatDate(a.work_date)}`" @click="viewing = a.id">View</AppButton>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination :page="list.data.value.page" :page-size="list.data.value.page_size" :total="list.data.value.total" @go="p => (filter.page = p)" />
      </div>
    </StateView>

    <AttendanceEditModal v-model="editOpen" :record="editing" @saved="list.load()" />
    <AttendanceViewModal :id="viewing" @close="viewing = null" @changed="list.load()" @edit="editFromView" />

    <AppModal v-model="addOpen" title="Add attendance record">
      <form id="add-form" class="grid gap-3 sm:grid-cols-2" @submit.prevent="addRecord">
        <FormField label="Employee" for="m-emp" class="sm:col-span-2">
          <SearchSelect id="m-emp" v-model="manual.employee_id" :options="activeEmployeeOptions" placeholder="Choose an employee…" required />
        </FormField>
        <FormField label="Date" for="m-date"><input id="m-date" v-model="manual.date" type="date" class="input" required /></FormField>
        <FormField label="Status" for="m-status" hint="Leave on Automatic to derive it from the times.">
          <SearchSelect id="m-status" v-model="manual.status" :options="manualStatusOptions" empty-label="Automatic" />
        </FormField>
        <FormField label="Check in" for="m-in" :hint="`Company time (${tz})`"><input id="m-in" v-model="manual.check_in" type="time" class="input" /></FormField>
        <FormField label="Check out" for="m-out"><input id="m-out" v-model="manual.check_out" type="time" class="input" /></FormField>
        <FormField label="Reason (audited)" for="m-reason" class="sm:col-span-2">
          <textarea id="m-reason" v-model="manual.reason" class="input" rows="2" maxlength="500" required />
        </FormField>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="addOpen = false">Cancel</AppButton>
        <AppButton type="submit" form="add-form" :loading="busy === 'add'">Save record</AppButton>
      </template>
    </AppModal>
  </div>
</template>
