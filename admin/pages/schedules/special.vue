<script setup lang="ts">
import { scheduleService } from '~/services/schedule.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { OverrideInput, OverrideKind, ScheduleOverride, WorkType } from '~/types/api'
import { formatDate, formatDayMonth, parseWorkDays, shiftHours, todayISO, workDaysLabel } from '~/utils/format'
import type { SelectOption } from '~/utils/options'

const auth = useAuthStore()
const lookups = useLookupsStore()
const tz = auth.company.timezone
const canEdit = computed(() => auth.can('staff.manage'))

const tabs = [
  { value: 'active', label: 'Active now' },
  { value: 'upcoming', label: 'Upcoming' },
  { value: 'past', label: 'Past' },
  { value: '', label: 'All' },
]
const filter = reactive({ when: 'active', branch_id: '', q: '' })
const list = usePaged(p => scheduleService.overrides({ when: filter.when || undefined, branch_id: filter.branch_id || undefined, q: filter.q.trim() || undefined }, p))
watch(() => [filter.when, filter.branch_id], () => list.reset())
let typing: ReturnType<typeof setTimeout> | undefined
watch(() => filter.q, () => {
  clearTimeout(typing)
  typing = setTimeout(() => list.reset(), 300)
})
onMounted(() => {
  lookups.loadEmployees().catch(() => {})
  lookups.loadSchedules().catch(() => {})
  lookups.loadDepartments().catch(() => {})
  lookups.loadBranches().catch(() => {})
})

const days = (o: { start_date: string; end_date: string }) =>
  Math.round((Date.parse(o.end_date.slice(0, 10)) - Date.parse(o.start_date.slice(0, 10))) / 86400000) + 1
function describe(o: ScheduleOverride) {
  if (o.kind === 'OFF') return 'Day off'
  if (o.kind === 'SHIFT') return `${o.work_schedule?.name ?? 'Shift'} ${shiftHours(o.work_schedule?.start_time, o.work_schedule?.end_time, o.work_schedule?.work_type === 'CROSS_DAY')}`
  return `Custom ${shiftHours(o.start_time, o.end_time, o.work_type === 'CROSS_DAY')}`
}
const kindTone = { SHIFT: 'info', CUSTOM: 'warning', OFF: 'neutral' } as const
const kindLabel = { SHIFT: 'Another shift', CUSTOM: 'Custom hours', OFF: 'Day off' } as const

// ---------- create / edit ----------
const kindOptions = [
  { value: 'SHIFT', label: 'Another shift' },
  { value: 'CUSTOM', label: 'Custom hours' },
  { value: 'OFF', label: 'Day off' },
]
const typeOptions = [
  { value: 'NORMAL', label: 'Same day' },
  { value: 'CROSS_DAY', label: 'Ends next day' },
]
const blank = () => ({
  id: 0, employee_id: '', employee_ids: [] as string[], start_date: todayISO(tz), end_date: todayISO(tz), kind: 'SHIFT' as OverrideKind, work_schedule_id: '',
  start_time: '08:00', end_time: '17:00', work_type: 'NORMAL' as WorkType, grace_minutes: 10, work_days: [1, 2, 3, 4, 5] as number[], reason: '',
})
const form = reactive(blank())
const open = ref(false)
function edit(o?: ScheduleOverride) {
  Object.assign(form, blank(), o
    ? { id: o.id, employee_id: String(o.employee_id), start_date: o.start_date.slice(0, 10), end_date: o.end_date.slice(0, 10), kind: o.kind,
        work_schedule_id: o.work_schedule_id ? String(o.work_schedule_id) : '', start_time: o.start_time ?? '08:00', end_time: o.end_time ?? '17:00',
        work_type: o.work_type ?? 'NORMAL', grace_minutes: o.grace_minutes ?? 0, work_days: parseWorkDays(o.work_days), reason: o.reason }
    : {})
  open.value = true
}
// The second line names the department and branch, so searching "Sales" and
// ticking all matches gives the whole department the schedule.
const employeeOptions = computed((): SelectOption[] => {
  const dept = new Map((lookups.departments ?? []).map(d => [d.id, d.name]))
  const br = new Map((lookups.branches ?? []).map(b => [b.id, b.name]))
  return (lookups.employees ?? []).filter(e => !e.deleted_at && e.status !== 'INACTIVE').map(e => ({
    value: String(e.id),
    label: e.full_name,
    sub: [e.employee_code, e.department_id ? dept.get(e.department_id) : '', e.branch_id ? br.get(e.branch_id) : ''].filter(Boolean).join(' · '),
  }))
})
const people = computed(() => (form.id ? (form.employee_id ? [form.employee_id] : []) : form.employee_ids))
const chosenShift = computed(() => lookups.schedules?.find(s => String(s.id) === form.work_schedule_id))
// A shift belongs to one branch: "Another shift" offers the shifts of the people's branch.
const peopleBranches = computed(() => [...new Set(people.value.map(id => lookups.employees?.find(e => String(e.id) === id)?.branch_id ?? 0))])
const shiftBranch = computed(() => (peopleBranches.value.length === 1 ? peopleBranches.value[0]! : 0))
const shiftOptions = computed(() => (shiftBranch.value ? lookups.shiftOptions(shiftBranch.value) : []))
watch(shiftBranch, (b) => {
  if (!lookups.fits('schedules', form.work_schedule_id, b) || (!b && form.work_schedule_id)) form.work_schedule_id = ''
})
// Picking a shift starts from its usual days.
watch(() => form.work_schedule_id, () => {
  if (chosenShift.value && !form.id) form.work_days = parseWorkDays(chosenShift.value.work_days)
})
watch(() => [form.start_time, form.end_time], ([s, e]) => {
  if (s && e) form.work_type = e <= s ? 'CROSS_DAY' : 'NORMAL'
})

const problem = computed(() => {
  if (!people.value.length) return form.id ? 'Choose an employee.' : 'Choose at least one employee.'
  if (form.end_date < form.start_date) return 'The end date is before the start date.'
  if (form.kind === 'SHIFT' && peopleBranches.value.length > 1) return 'Shifts belong to one branch: choose people of one branch, or use custom hours.'
  if (form.kind === 'SHIFT' && !form.work_schedule_id) return 'Choose the shift to work.'
  if (form.kind !== 'OFF' && !form.work_days.length) return 'Choose at least one work day.'
  return ''
})
const preview = computed(() => {
  if (problem.value) return ''
  const names = people.value.map(id => lookups.employees?.find(e => String(e.id) === id)?.full_name ?? '')
  const who = names.length === 1 ? names[0] : names.length === 2 ? `${names[0]} and ${names[1]}` : `${names[0]} and ${names.length - 1} others`
  const range = form.start_date === form.end_date ? formatDate(form.start_date) : `${formatDayMonth(form.start_date)} – ${formatDate(form.end_date)}`
  const n = days(form)
  const span = `${range} (${n} day${n === 1 ? '' : 's'})`
  if (form.kind === 'OFF') return `${who} ${names.length === 1 ? 'does' : 'do'} not work ${span}.`
  const what = form.kind === 'SHIFT' && chosenShift.value
    ? `${chosenShift.value.name} ${shiftHours(chosenShift.value.start_time, chosenShift.value.end_time, chosenShift.value.work_type === 'CROSS_DAY')}`
    : shiftHours(form.start_time, form.end_time, form.work_type === 'CROSS_DAY')
  return `${who} ${names.length === 1 ? 'works' : 'work'} ${what} on ${workDaysLabel(form.work_days)}, ${span}; other days off.`
})

const { busy, run } = useAction()
const { confirm } = useConfirm()
async function save() {
  if (problem.value) return
  const who = form.id ? { employee_id: Number(form.employee_id) } : { employee_ids: form.employee_ids.map(Number) }
  const input: OverrideInput = { ...who, start_date: form.start_date, end_date: form.end_date, kind: form.kind, reason: form.reason.trim() }
  if (form.kind === 'SHIFT') Object.assign(input, { work_schedule_id: Number(form.work_schedule_id), work_days: form.work_days })
  if (form.kind === 'CUSTOM') {
    Object.assign(input, { start_time: form.start_time, end_time: form.end_time, work_type: form.work_type, grace_minutes: Number(form.grace_minutes), work_days: form.work_days })
  }
  const n = people.value.length
  const ok = await run('save', () => (form.id ? scheduleService.updateOverride(form.id, input) : scheduleService.createOverride(input)),
    form.id ? 'Special schedule saved' : n === 1 ? 'Special schedule added' : `Special schedule added for ${n} employees`)
  if (ok) {
    open.value = false
    list.load()
  }
}
async function remove(o: ScheduleOverride) {
  const ok = await confirm({ title: 'Delete this special schedule?', message: 'Their usual shift and the weekly roster apply again on these dates.',
    details: [{ label: 'Employee', value: o.employee?.full_name ?? '' }, { label: 'Dates', value: `${formatDate(o.start_date)} – ${formatDate(o.end_date)}` },
      { label: 'Schedule', value: kindLabel[o.kind] }] })
  if (!ok) return
  if (await run(`d${o.id}`, () => scheduleService.deleteOverride(o.id), 'Special schedule deleted')) list.refresh()
}
</script>

<template>
  <div>
    <PageHeader title="Special schedules" subtitle="Temporary changes for a date range — another shift, custom hours, or days off. They take priority over the weekly roster.">
      <AppButton v-if="canEdit" @click="edit()">New special schedule</AppButton>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)] sm:items-end">
      <FormField label="Show"><Segmented v-model="filter.when" :options="tabs" label="Show" /></FormField>
      <BranchFilter id="sp-branch" v-model="filter.branch_id" />
      <FormField label="Search" for="sp-q"><input id="sp-q" v-model="filter.q" type="search" class="input" placeholder="Name or employee ID" /></FormField>
    </div>

    <StateView
      :loading="list.loading.value && !list.data.value"
      :error="list.error.value"
      :empty="list.total.value === 0"
      :empty-text="filter.when === 'active' ? 'No special schedules in force today.' : 'No special schedules.'"
      @retry="list.load"
    >
      <div class="card overflow-hidden" :class="{ 'opacity-60': list.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Employee</th><th>Dates</th><th>Schedule</th><th>Days</th><th>Reason</th><th v-if="canEdit" class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="o in list.rows.value" :key="o.id">
                <td>
                  <span class="font-medium">{{ o.employee?.full_name }}</span>
                  <span class="block text-xs text-muted">{{ o.employee?.employee_code }}<template v-if="o.employee?.branch && auth.multiBranch"> · {{ o.employee.branch.name }}</template></span>
                </td>
                <td class="whitespace-nowrap">
                  {{ o.start_date === o.end_date ? formatDate(o.start_date) : `${formatDayMonth(o.start_date)} – ${formatDate(o.end_date)}` }}
                  <span class="block text-xs text-muted">{{ days(o) }} day{{ days(o) === 1 ? '' : 's' }}</span>
                </td>
                <td class="whitespace-nowrap">
                  <StatusBadge :tone="kindTone[o.kind]" :label="kindLabel[o.kind]" />
                  <span v-if="o.kind !== 'OFF'" class="mt-0.5 block text-xs tabular-nums text-muted">{{ describe(o) }}</span>
                </td>
                <td class="whitespace-nowrap text-sm">{{ o.kind === 'OFF' ? '—' : workDaysLabel(parseWorkDays(o.work_days)) }}</td>
                <td class="max-w-xs text-sm text-muted">{{ o.reason || '—' }}</td>
                <td v-if="canEdit" class="whitespace-nowrap text-right">
                  <div class="inline-flex gap-1.5">
                    <IconAction kind="edit" :label="`Edit special schedule for ${o.employee?.full_name}`" @click="edit(o)" />
                    <IconAction kind="delete" :label="`Delete special schedule for ${o.employee?.full_name}`" :loading="busy === `d${o.id}`" @click="remove(o)" />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination :page="list.page.value" :page-size="list.pageSize.value" :total="list.total.value" noun="special schedule" @go="list.go" @size="list.resize" />
      </div>
    </StateView>

    <AppModal v-model="open" :title="form.id ? 'Edit special schedule' : 'New special schedule'" wide>
      <form id="special-form" class="grid gap-4 sm:grid-cols-2" @submit.prevent="save">
        <FormField v-if="form.id" label="Employee" for="sp-emp" class="sm:col-span-2" hint="To give this schedule to more people, add a new special schedule.">
          <SearchSelect id="sp-emp" v-model="form.employee_id" :options="employeeOptions" required disabled />
        </FormField>
        <FormField v-else label="Employees" for="sp-emps" class="sm:col-span-2" hint="Tick one or more people. Search a department or branch, then tick all matches.">
          <MultiSelect id="sp-emps" v-model="form.employee_ids" :options="employeeOptions" noun="employee" placeholder="Choose employees…" search-placeholder="Search name, ID, department, branch…" required />
        </FormField>
        <FormField label="From" for="sp-from"><input id="sp-from" v-model="form.start_date" type="date" class="input" required /></FormField>
        <FormField label="To" for="sp-to"><input id="sp-to" v-model="form.end_date" type="date" class="input" :min="form.start_date" required /></FormField>
        <FormField label="During these dates" class="sm:col-span-2">
          <Segmented v-model="form.kind" :options="kindOptions" label="During these dates" />
        </FormField>

        <FormField v-if="form.kind === 'SHIFT'" label="Shift" for="sp-shift" class="sm:col-span-2">
          <SearchSelect
            id="sp-shift" v-model="form.work_schedule_id" :options="shiftOptions" required :disabled="!shiftBranch"
            :placeholder="shiftBranch ? 'Choose a shift…' : peopleBranches.length > 1 ? 'People of several branches chosen' : 'Choose employees first'"
          />
        </FormField>
        <template v-if="form.kind === 'CUSTOM'">
          <FormField label="Start" for="sp-start"><input id="sp-start" v-model="form.start_time" type="time" class="input" required /></FormField>
          <FormField label="End" for="sp-end" :hint="form.work_type === 'CROSS_DAY' ? 'On the next day (night shift).' : undefined">
            <input id="sp-end" v-model="form.end_time" type="time" class="input" required />
          </FormField>
          <FormField label="Work type"><Segmented v-model="form.work_type" :options="typeOptions" label="Work type" /></FormField>
          <FormField label="Grace period (minutes)" for="sp-grace"><input id="sp-grace" v-model.number="form.grace_minutes" type="number" min="0" max="240" class="input" /></FormField>
        </template>
        <FormField v-if="form.kind !== 'OFF'" label="On these days" class="sm:col-span-2" hint="Other days in the range are days off.">
          <WeekdayPicker v-model="form.work_days" label="On these days" />
        </FormField>
        <FormField label="Reason" for="sp-reason" class="sm:col-span-2" hint="Shown to the employee in the Mini App.">
          <textarea id="sp-reason" v-model="form.reason" class="input" rows="2" maxlength="255" placeholder="e.g. Covering the night help desk" />
        </FormField>
        <p class="rounded-lg bg-surface-2 px-3 py-2 text-sm sm:col-span-2" :class="problem ? 'text-muted' : ''" role="status">{{ problem || preview }}</p>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="open = false">Cancel</AppButton>
        <AppButton type="submit" form="special-form" :loading="busy === 'save'" :disabled="!!problem">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
