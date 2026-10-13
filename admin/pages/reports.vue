<script setup lang="ts">
import { reportService, type ReportFilter } from '~/services/report.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import { formatDate, formatMinutes, monthStartISO, todayISO } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const tz = auth.company.timezone
const filter = reactive({ from: monthStartISO(tz), to: todayISO(tz), branch_id: '', department_id: '', employee_id: '' })
const query = computed<ReportFilter>(() => ({
  from: filter.from,
  to: filter.to,
  department_id: Number(filter.department_id) || undefined,
  branch_id: Number(filter.branch_id) || undefined,
  employee_id: Number(filter.employee_id) || undefined,
}))

const report = useResource(() => reportService.summary(query.value))
watch(query, () => report.load())
onMounted(() => {
  lookups.loadEmployees().catch(() => {})
  lookups.loadDepartments().catch(() => {})
})

const presets = [
  { label: 'This month', from: () => monthStartISO(tz), to: () => todayISO(tz) },
  {
    label: 'Last month',
    from: () => {
      const d = new Date(`${monthStartISO(tz)}T00:00:00Z`)
      d.setUTCMonth(d.getUTCMonth() - 1)
      return d.toISOString().slice(0, 10)
    },
    to: () => {
      const d = new Date(`${monthStartISO(tz)}T00:00:00Z`)
      d.setUTCDate(0)
      return d.toISOString().slice(0, 10)
    },
  },
  { label: 'Today', from: () => todayISO(tz), to: () => todayISO(tz) },
]
const totals = computed(() => {
  const t = report.data.value?.totals
  if (!t) return []
  return [
    { label: 'Staff', value: report.data.value!.staff_count },
    { label: 'Present days', value: t.present_days, hint: `of ${t.working_days} working days`, tone: 'success' as const },
    { label: 'Late days', value: t.late_days, hint: formatMinutes(t.late_minutes) + ' late in total', tone: 'warning' as const },
    { label: 'Absent days', value: t.absent_days, tone: 'danger' as const },
    { label: 'Leave days', value: t.leave_days, tone: 'info' as const },
    { label: 'Worked', value: formatMinutes(t.working_minutes) },
  ]
})
</script>

<template>
  <div>
    <PageHeader title="Reports" :subtitle="report.data.value ? `${formatDate(report.data.value.from)} – ${formatDate(report.data.value.to)}` : undefined">
      <template v-if="auth.can('report.export')">
        <a :href="reportService.exportUrl(query, 'xlsx')" class="inline-flex items-center rounded-lg bg-primary px-3.5 py-2 text-sm font-medium text-primary-fg hover:brightness-110" download>Export Excel</a>
        <a :href="reportService.exportUrl(query, 'csv')" class="inline-flex items-center rounded-lg border border-border bg-surface px-3.5 py-2 text-sm font-medium hover:bg-surface-2" download>CSV</a>
      </template>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-5">
      <FormField label="From" for="r-from"><input id="r-from" v-model="filter.from" type="date" class="input" /></FormField>
      <FormField label="To" for="r-to"><input id="r-to" v-model="filter.to" type="date" class="input" :min="filter.from" /></FormField>
      <BranchFilter id="r-branch" v-model="filter.branch_id" />
      <FormField label="Department" for="r-dept">
        <SearchSelect id="r-dept" v-model="filter.department_id" :options="lookups.departmentOptions" empty-label="All" />
      </FormField>
      <FormField label="Employee" for="r-emp">
        <SearchSelect id="r-emp" v-model="filter.employee_id" :options="lookups.employeeOptions" empty-label="All" />
      </FormField>
      <div class="flex flex-wrap gap-2 sm:col-span-5">
        <AppButton v-for="p in presets" :key="p.label" size="sm" variant="secondary" @click="filter.from = p.from(); filter.to = p.to()">{{ p.label }}</AppButton>
      </div>
    </div>

    <StateView :loading="report.loading.value && !report.data.value" :error="report.error.value" @retry="report.load">
      <template v-if="report.data.value">
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6" :class="{ 'opacity-60': report.loading.value }">
          <StatTile v-for="t in totals" :key="t.label" v-bind="t" />
        </div>

        <div class="card mt-6 overflow-x-auto" :class="{ 'opacity-60': report.loading.value }">
          <p v-if="!report.data.value.summaries.length" class="px-4 py-10 text-center text-sm text-muted">No staff in this selection.</p>
          <table v-else class="table">
            <thead>
              <tr>
                <th>Employee</th><th>Branch</th><th>Department</th><th class="text-right">Present</th><th class="text-right">Late</th><th class="text-right">Early</th>
                <th class="text-right">Absent</th><th class="text-right">Leave</th><th class="text-right">No out</th><th class="text-right">Worked</th><th class="text-right">Avg/day</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in report.data.value.summaries" :key="s.employee_id">
                <td><NuxtLink :to="{ path: '/attendance', query: { employee_id: String(s.employee_id), from: filter.from, to: filter.to } }" class="font-medium hover:underline">{{ s.full_name }}</NuxtLink><span class="block text-xs text-muted">{{ s.employee_code }}</span></td>
                <td class="text-muted">{{ s.branch || '—' }}</td>
                <td class="text-muted">{{ s.department || '—' }}</td>
                <td class="text-right tabular-nums">{{ s.present_days }}<span class="text-muted">/{{ s.working_days }}</span></td>
                <td class="text-right tabular-nums" :class="{ 'text-warning': s.late_days }">{{ s.late_days }}</td>
                <td class="text-right tabular-nums">{{ s.early_leave_days }}</td>
                <td class="text-right tabular-nums" :class="{ 'text-danger': s.absent_days }">{{ s.absent_days }}</td>
                <td class="text-right tabular-nums">{{ s.leave_days }}</td>
                <td class="text-right tabular-nums">{{ s.missing_checkout }}</td>
                <td class="text-right tabular-nums">{{ formatMinutes(s.working_minutes) }}</td>
                <td class="text-right tabular-nums">{{ formatMinutes(s.avg_minutes_per_day) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </StateView>
  </div>
</template>
