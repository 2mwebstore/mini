<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Attendance, AttendanceFilter } from '~/types/api'
import { dayStatus, formatClock, formatDate, formatMinutes, latestReview, monthStartISO, nextDay, todayISO } from '~/utils/format'
import { optionsFrom } from '~/utils/options'

const dayStatusOptions = optionsFrom(dayStatus, ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE'])
const manualStatusOptions = optionsFrom(dayStatus, ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE', 'HOLIDAY'])

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
  confirmation: q('confirmation'), // only from links (the dashboard's "waiting for review"): shown as a chip
  page: Number(q('page')) || 1,
  size: Number(q('page_size')) || 10, // rows per page
})
// Departments belong to one branch: drop a choice the new branch doesn't have.
watch(() => filter.branch_id, (b) => {
  if (!lookups.fits('departments', filter.department_id, b)) filter.department_id = ''
}, { flush: 'sync' })
const query = computed<AttendanceFilter>(() => ({
  from: filter.from,
  to: filter.to,
  employee_id: Number(filter.employee_id) || undefined,
  department_id: Number(filter.department_id) || undefined,
  branch_id: Number(filter.branch_id) || undefined,
  status: filter.status || undefined,
  confirmation: filter.confirmation || undefined,
  page: filter.page,
  page_size: filter.size,
}))

const list = useResource(() => attendanceService.list(query.value))
watch(
  () => [filter.from, filter.to, filter.employee_id, filter.department_id, filter.branch_id, filter.status, filter.confirmation, filter.size],
  () => (filter.page = 1),
)
watch(query, q => {
  router.replace({ query: Object.fromEntries(Object.entries(q).filter(([k, v]) => v !== undefined && !(k === 'page_size' && v === 10)).map(([k, v]) => [k, String(v)])) })
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

// ---------- delete ----------
const deleting = useDeleteAttendance()
async function removeRecord(a: Attendance) {
  if (await deleting.remove(a)) list.load()
}

// ---------- view one record ----------
const viewing = ref<number | null>(null)
function editFromView(id: number) {
  const a = list.data.value?.items.find(x => x.id === id)
  viewing.value = null
  if (a) editing.value = a
}
// Where a check-in/out was made, for the list's pin (opens Google Maps).
function placeOf(a: Attendance, type: 'CHECK_IN' | 'CHECK_OUT') {
  const r = latestReview(a.confirmations, type)
  return (r?.location_id ? a.locations?.find(x => x.id === r.location_id) : undefined)
    ?? [...(a.locations ?? [])].filter(l => l.type === type).sort((x, y) => y.id - x.id)[0] ?? null
}

const activeEmployees = computed(() => (lookups.employees ?? []).filter(e => !e.deleted_at))
const activeEmployeeOptions = computed(() => activeEmployees.value.map(e => ({ value: String(e.id), label: e.full_name, sub: e.employee_code })))
</script>

<template>
  <div>
    <PageHeader title="Attendance" subtitle="Every check-in and check-out with its time, on time / late / early, location and the reason given. View a day for the full record and its review.">
      <AppButton v-if="auth.can('attendance.edit')" @click="addOpen = true">Add record</AppButton>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-3 lg:grid-cols-6">
      <FormField label="From" for="f-from"><input id="f-from" v-model="filter.from" type="date" class="input" /></FormField>
      <FormField label="To" for="f-to"><input id="f-to" v-model="filter.to" type="date" class="input" :min="filter.from" /></FormField>
      <FormField label="Employee" for="f-emp">
        <SearchSelect id="f-emp" v-model="filter.employee_id" :options="lookups.employeeOptions" empty-label="All" />
      </FormField>
      <BranchFilter id="f-branch" v-model="filter.branch_id" />
      <FormField label="Department" for="f-dept">
        <SearchSelect id="f-dept" v-model="filter.department_id" :options="lookups.departmentOptions(filter.branch_id)" empty-label="All" />
      </FormField>
      <FormField label="Status" for="f-status">
        <SearchSelect id="f-status" v-model="filter.status" :options="dayStatusOptions" empty-label="All" />
      </FormField>
    </div>

    <p v-if="filter.confirmation" class="mb-3 flex items-center gap-2 text-sm">
      <span class="inline-flex items-center gap-1 rounded-full bg-warning/10 py-1 pl-3 pr-1 text-warning">
        Only records waiting for review
        <button type="button" class="rounded-full px-1.5 hover:bg-warning/20" aria-label="Show all records" @click="filter.confirmation = ''">×</button>
      </span>
    </p>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.data.value?.items.length === 0" empty-text="No attendance matches these filters." @retry="list.load">
      <div v-if="list.data.value" class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Date</th><th>Employee</th><th>Check in</th><th>Check out</th><th>Worked</th><th>Reason</th><th class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="a in list.data.value.items" :key="a.id" class="hover:bg-surface-2/60">
                <td class="whitespace-nowrap">{{ formatDate(a.work_date, true) }}</td>
                <td>
                  <span class="font-medium">{{ a.employee?.full_name }}</span>
                  <span class="block text-xs text-muted">{{ a.employee?.employee_code }}<template v-if="auth.multiBranch && a.employee?.branch"> · {{ a.employee.branch.name }}</template></span>
                </td>
                <td v-for="t in (['CHECK_IN', 'CHECK_OUT'] as const)" :key="t" class="whitespace-nowrap">
                  <template v-for="at in [t === 'CHECK_IN' ? a.check_in_at : a.check_out_at]" :key="t">
                    <span v-if="!at" class="text-muted">—</span>
                    <template v-else>
                      <span class="inline-flex items-center gap-1 font-medium tabular-nums">
                        {{ formatClock(at, tz) }}<sup v-if="t === 'CHECK_OUT' && nextDay(a.work_date, at, tz)" class="text-[10px] text-muted" title="Next morning (night shift)">+1</sup>
                        <template v-for="p in [placeOf(a, t)]" :key="t">
                          <MapPin
                            v-if="p" :latitude="p.latitude" :longitude="p.longitude" :inside="p.inside_office" :distance="p.distance_meters"
                            :what="`${a.employee?.full_name} ${t === 'CHECK_IN' ? 'check-in' : 'check-out'}`"
                          />
                        </template>
                      </span>
                      <span class="mt-0.5 block text-xs">
                        <span v-if="t === 'CHECK_IN'" :class="a.late_minutes ? 'text-warning' : 'text-success'">{{ a.late_minutes ? `Late ${formatMinutes(a.late_minutes)}` : 'On time' }}</span>
                        <span v-else :class="a.early_leave_minutes ? 'text-warning' : 'text-success'">{{ a.early_leave_minutes ? `Early ${formatMinutes(a.early_leave_minutes)}` : 'On time' }}</span>
                      </span>
                    </template>
                  </template>
                </td>
                <td class="tabular-nums">{{ formatMinutes(a.working_minutes) }}</td>
                <!-- Why they were late / left early, in their words (asked in the Mini App). -->
                <td class="min-w-48 max-w-xs text-sm">
                  <template v-if="a.check_in_reason || a.check_out_reason">
                    <p v-if="a.check_in_reason" class="line-clamp-2" :title="a.check_in_reason"><span class="text-xs font-medium text-muted">In:</span> {{ a.check_in_reason }}</p>
                    <p v-if="a.check_out_reason" class="line-clamp-2" :title="a.check_out_reason"><span class="text-xs font-medium text-muted">Out:</span> {{ a.check_out_reason }}</p>
                  </template>
                  <span v-else-if="!a.check_in_at" class="text-muted">{{ dayStatus[a.status].label }}</span>
                  <span v-else class="text-muted">—</span>
                </td>
                <td class="whitespace-nowrap text-right">
                  <div class="inline-flex items-center gap-1.5">
                    <IconAction kind="view" :label="`View ${a.employee?.full_name} ${formatDate(a.work_date)}`" @click="viewing = a.id" />
                    <IconAction v-if="auth.can('attendance.edit')" kind="edit" :label="`Edit ${a.employee?.full_name} ${formatDate(a.work_date)}`" @click="editing = a" />
                    <IconAction
                      v-if="auth.can('attendance.edit')" kind="delete" :label="`Delete ${a.employee?.full_name} ${formatDate(a.work_date)}`"
                      :loading="deleting.busy.value === `del${a.id}`" @click="removeRecord(a)"
                    />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination :page="list.data.value.page" :page-size="list.data.value.page_size" :total="list.data.value.total" @go="p => (filter.page = p)" @size="n => (filter.size = n)" />
      </div>
    </StateView>

    <AttendanceEditModal v-model="editOpen" :record="editing" @saved="list.load()" />
    <AttendanceViewModal :id="viewing" @close="viewing = null" @changed="list.load()" @edit="editFromView" @deleted="viewing = null; list.load()" />

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
