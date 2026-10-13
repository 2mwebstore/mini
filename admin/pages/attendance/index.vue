<script setup lang="ts">
import { attendanceService } from '~/services/attendance.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { AttendanceFilter } from '~/types/api'
import { confirmationStatus, dayStatus, formatClock, formatDate, formatMinutes, monthStartISO, todayISO } from '~/utils/format'

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
const activeEmployees = computed(() => (lookups.employees ?? []).filter(e => !e.deleted_at))
</script>

<template>
  <div>
    <PageHeader title="Attendance" subtitle="Every check-in and check-out, with photos, location and review history.">
      <AppButton v-if="auth.can('attendance.edit')" @click="addOpen = true">Add record</AppButton>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-3 lg:grid-cols-7">
      <FormField label="From" for="f-from"><input id="f-from" v-model="filter.from" type="date" class="input" /></FormField>
      <FormField label="To" for="f-to"><input id="f-to" v-model="filter.to" type="date" class="input" :min="filter.from" /></FormField>
      <FormField label="Employee" for="f-emp">
        <select id="f-emp" v-model="filter.employee_id" class="input">
          <option value="">All</option>
          <option v-for="e in lookups.employees ?? []" :key="e.id" :value="String(e.id)">{{ e.employee_code }} · {{ e.full_name }}</option>
        </select>
      </FormField>
      <BranchFilter id="f-branch" v-model="filter.branch_id" />
      <FormField label="Department" for="f-dept">
        <select id="f-dept" v-model="filter.department_id" class="input">
          <option value="">All</option>
          <option v-for="d in lookups.departments ?? []" :key="d.id" :value="String(d.id)">{{ d.name }}</option>
        </select>
      </FormField>
      <FormField label="Status" for="f-status">
        <select id="f-status" v-model="filter.status" class="input">
          <option value="">All</option>
          <option v-for="s in ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE']" :key="s" :value="s">{{ dayStatus[s as keyof typeof dayStatus].label }}</option>
        </select>
      </FormField>
      <FormField label="Review" for="f-conf">
        <select id="f-conf" v-model="filter.confirmation" class="input">
          <option value="">All</option>
          <option value="PENDING">Pending</option>
          <option value="CONFIRMED">Confirmed</option>
          <option value="REJECTED">Rejected</option>
        </select>
      </FormField>
    </div>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.data.value?.items.length === 0" empty-text="No attendance matches these filters." @retry="list.load">
      <div v-if="list.data.value" class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Date</th><th>Employee</th><th>In</th><th>Out</th><th>Worked</th><th>Late</th><th>Status</th><th>Review</th><th /></tr></thead>
            <tbody>
              <tr v-for="a in list.data.value.items" :key="a.id" class="hover:bg-surface-2/60">
                <td class="whitespace-nowrap">{{ formatDate(a.work_date, true) }}</td>
                <td>
                  <span class="font-medium">{{ a.employee?.full_name }}</span>
                  <span class="block text-xs text-muted">{{ a.employee?.employee_code }}<template v-if="auth.can('all.view') && a.employee?.branch"> · {{ a.employee.branch.name }}</template></span>
                </td>
                <td class="tabular-nums">{{ formatClock(a.check_in_at, tz) }}</td>
                <td class="tabular-nums">{{ formatClock(a.check_out_at, tz) }}</td>
                <td class="tabular-nums">{{ formatMinutes(a.working_minutes) }}</td>
                <td class="tabular-nums" :class="{ 'text-warning': a.late_minutes }">{{ formatMinutes(a.late_minutes) }}</td>
                <td><StatusBadge v-bind="dayStatus[a.status]" /></td>
                <td><StatusBadge v-if="a.check_in_at" v-bind="confirmationStatus[a.confirmation_status]" /></td>
                <td class="text-right"><NuxtLink :to="`/attendance/${a.id}`" class="link text-sm">View</NuxtLink></td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination :page="list.data.value.page" :page-size="list.data.value.page_size" :total="list.data.value.total" @go="p => (filter.page = p)" />
      </div>
    </StateView>

    <AppModal v-model="addOpen" title="Add attendance record">
      <form id="add-form" class="grid gap-3 sm:grid-cols-2" @submit.prevent="addRecord">
        <FormField label="Employee" for="m-emp" class="sm:col-span-2">
          <select id="m-emp" v-model="manual.employee_id" class="input" required>
            <option value="" disabled>Choose…</option>
            <option v-for="e in activeEmployees" :key="e.id" :value="String(e.id)">{{ e.employee_code }} · {{ e.full_name }}</option>
          </select>
        </FormField>
        <FormField label="Date" for="m-date"><input id="m-date" v-model="manual.date" type="date" class="input" required /></FormField>
        <FormField label="Status" for="m-status" hint="Leave on Automatic to derive it from the times.">
          <select id="m-status" v-model="manual.status" class="input">
            <option value="">Automatic</option>
            <option v-for="s in ['PRESENT', 'LATE', 'EARLY_LEAVE', 'ABSENT', 'LEAVE', 'HOLIDAY']" :key="s" :value="s">{{ dayStatus[s as keyof typeof dayStatus].label }}</option>
          </select>
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
