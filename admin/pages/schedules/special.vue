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
const list = useResource(() => scheduleService.overrides({ when: filter.when || undefined, branch_id: filter.branch_id || undefined, q: filter.q.trim() || undefined }))
watch(() => [filter.when, filter.branch_id], () => list.load())
let typing: ReturnType<typeof setTimeout> | undefined
watch(() => filter.q, () => {
  clearTimeout(typing)
  typing = setTimeout(() => list.load(), 300)
})
onMounted(() => {
  lookups.loadEmployees().catch(() => {})
  lookups.loadSchedules().catch(() => {})
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
  id: 0, employee_id: '', start_date: todayISO(tz), end_date: todayISO(tz), kind: 'SHIFT' as OverrideKind, work_schedule_id: '',
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
const employeeOptions = computed((): SelectOption[] =>
  (lookups.employees ?? []).filter(e => !e.deleted_at && e.status !== 'INACTIVE').map(e => ({ value: String(e.id), label: e.full_name, sub: e.employee_code })))
const chosenShift = computed(() => lookups.schedules?.find(s => String(s.id) === form.work_schedule_id))
// Picking a shift starts from its usual days.
watch(() => form.work_schedule_id, () => {
  if (chosenShift.value && !form.id) form.work_days = parseWorkDays(chosenShift.value.work_days)
})
watch(() => [form.start_time, form.end_time], ([s, e]) => {
  if (s && e) form.work_type = e <= s ? 'CROSS_DAY' : 'NORMAL'
})

const problem = computed(() => {
  if (!form.employee_id) return 'Choose an employee.'
  if (form.end_date < form.start_date) return 'The end date is before the start date.'
  if (form.kind === 'SHIFT' && !form.work_schedule_id) return 'Choose the shift to work.'
  if (form.kind !== 'OFF' && !form.work_days.length) return 'Choose at least one work day.'
  return ''
})
const preview = computed(() => {
  if (problem.value) return ''
  const who = lookups.employees?.find(e => String(e.id) === form.employee_id)?.full_name ?? ''
  const range = form.start_date === form.end_date ? formatDate(form.start_date) : `${formatDayMonth(form.start_date)} – ${formatDate(form.end_date)}`
  const n = days(form)
  const span = `${range} (${n} day${n === 1 ? '' : 's'})`
  if (form.kind === 'OFF') return `${who} does not work ${span}.`
  const what = form.kind === 'SHIFT' && chosenShift.value
    ? `${chosenShift.value.name} ${shiftHours(chosenShift.value.start_time, chosenShift.value.end_time, chosenShift.value.work_type === 'CROSS_DAY')}`
    : shiftHours(form.start_time, form.end_time, form.work_type === 'CROSS_DAY')
  return `${who} works ${what} on ${workDaysLabel(form.work_days)}, ${span}; other days off.`
})

const { busy, run } = useAction()
async function save() {
  if (problem.value) return
  const input: OverrideInput = { employee_id: Number(form.employee_id), start_date: form.start_date, end_date: form.end_date, kind: form.kind, reason: form.reason.trim() }
  if (form.kind === 'SHIFT') Object.assign(input, { work_schedule_id: Number(form.work_schedule_id), work_days: form.work_days })
  if (form.kind === 'CUSTOM') {
    Object.assign(input, { start_time: form.start_time, end_time: form.end_time, work_type: form.work_type, grace_minutes: Number(form.grace_minutes), work_days: form.work_days })
  }
  const ok = await run('save', () => (form.id ? scheduleService.updateOverride(form.id, input) : scheduleService.createOverride(input)),
    form.id ? 'Special schedule saved' : 'Special schedule added')
  if (ok) {
    open.value = false
    list.load()
  }
}
async function remove(o: ScheduleOverride) {
  if (!window.confirm(`Delete the special schedule for ${o.employee?.full_name} (${formatDate(o.start_date)} – ${formatDate(o.end_date)})?`)) return
  if (await run(`d${o.id}`, () => scheduleService.deleteOverride(o.id), 'Special schedule deleted')) list.load()
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
      :empty="list.data.value?.length === 0"
      :empty-text="filter.when === 'active' ? 'No special schedules in force today.' : 'No special schedules.'"
      @retry="list.load"
    >
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Employee</th><th>Dates</th><th>Schedule</th><th>Days</th><th>Reason</th><th v-if="canEdit" class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="o in list.data.value ?? []" :key="o.id">
                <td>
                  <span class="font-medium">{{ o.employee?.full_name }}</span>
                  <span class="block text-xs text-muted">{{ o.employee?.employee_code }}<template v-if="o.employee?.branch && auth.can('all.view')"> · {{ o.employee.branch.name }}</template></span>
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
                  <AppButton size="sm" variant="ghost" :aria-label="`Edit special schedule for ${o.employee?.full_name}`" @click="edit(o)">Edit</AppButton>
                  <AppButton size="sm" variant="ghost" :loading="busy === `d${o.id}`" :aria-label="`Delete special schedule for ${o.employee?.full_name}`" @click="remove(o)">Delete</AppButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </StateView>

    <AppModal v-model="open" :title="form.id ? 'Edit special schedule' : 'New special schedule'" wide>
      <form id="special-form" class="grid gap-4 sm:grid-cols-2" @submit.prevent="save">
        <FormField label="Employee" for="sp-emp" class="sm:col-span-2">
          <SearchSelect id="sp-emp" v-model="form.employee_id" :options="employeeOptions" placeholder="Choose an employee…" required :disabled="!!form.id" />
        </FormField>
        <FormField label="From" for="sp-from"><input id="sp-from" v-model="form.start_date" type="date" class="input" required /></FormField>
        <FormField label="To" for="sp-to"><input id="sp-to" v-model="form.end_date" type="date" class="input" :min="form.start_date" required /></FormField>
        <FormField label="During these dates" class="sm:col-span-2">
          <Segmented v-model="form.kind" :options="kindOptions" label="During these dates" />
        </FormField>

        <FormField v-if="form.kind === 'SHIFT'" label="Shift" for="sp-shift" class="sm:col-span-2">
          <SearchSelect id="sp-shift" v-model="form.work_schedule_id" :options="lookups.shiftOptions" placeholder="Choose a shift…" required />
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
