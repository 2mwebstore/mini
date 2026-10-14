<script setup lang="ts">
import { scheduleService } from '~/services/schedule.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { WorkSchedule, WorkType } from '~/types/api'
import { parseWorkDays, shiftHours, workDaysLabel } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const canEdit = computed(() => auth.can('schedules.manage'))
const shifts = useResource(() => scheduleService.shifts())
const { busy, run } = useAction()

const palette = ['#2563eb', '#0891b2', '#059669', '#ca8a04', '#ea580c', '#dc2626', '#db2777', '#7c3aed', '#4f46e5', '#475569']
const typeOptions = [
  { value: 'NORMAL', label: 'Normal (same day)' },
  { value: 'CROSS_DAY', label: 'Cross-day (night shift)' },
]

const form = reactive({
  id: 0, name: '', start_time: '08:00', end_time: '17:00', work_type: 'NORMAL' as WorkType,
  grace_minutes: 10, work_days: [1, 2, 3, 4, 5] as number[], color: palette[0]!, is_default: false,
})
const open = ref(false)
// New shifts get a colour no other shift has, so they stand apart on the roster.
const unusedColor = () => palette.find(c => !shifts.data.value?.some(s => s.color.toLowerCase() === c)) ?? palette[0]!
function edit(s?: WorkSchedule) {
  Object.assign(form, s
    ? { id: s.id, name: s.name, start_time: s.start_time, end_time: s.end_time, work_type: s.work_type, grace_minutes: s.grace_minutes,
        work_days: parseWorkDays(s.work_days), color: s.color, is_default: s.is_default }
    : { id: 0, name: '', start_time: '08:00', end_time: '17:00', work_type: 'NORMAL', grace_minutes: 10, work_days: [1, 2, 3, 4, 5], color: unusedColor(), is_default: false })
  open.value = true
}

// The same rules as the API, shown while typing.
const crossDay = computed(() => form.work_type === 'CROSS_DAY')
const hoursError = computed(() => {
  if (!form.start_time || !form.end_time) return ''
  if (!crossDay.value && form.end_time <= form.start_time) return 'Ends before it starts — choose "Cross-day" for a night shift.'
  if (crossDay.value && form.end_time > form.start_time) return 'A cross-day shift ends the next day: its end must be earlier than its start.'
  return ''
})
const length = computed(() => {
  const [sh, sm] = form.start_time.split(':').map(Number)
  const [eh, em] = form.end_time.split(':').map(Number)
  let mins = (eh! * 60 + em!) - (sh! * 60 + sm!)
  if (crossDay.value) mins += 24 * 60
  return mins > 0 ? `${Math.floor(mins / 60)}h${mins % 60 ? ` ${mins % 60}m` : ''}` : ''
})
// Typing hours that cross midnight switches the work type for you.
watch(() => [form.start_time, form.end_time], ([s, e]) => {
  if (s && e && e <= s) form.work_type = 'CROSS_DAY'
  else if (s && e && e > s && form.work_type === 'CROSS_DAY') form.work_type = 'NORMAL'
})

async function save() {
  if (hoursError.value || !form.work_days.length) return
  const input = { name: form.name.trim(), start_time: form.start_time, end_time: form.end_time, work_type: form.work_type,
    grace_minutes: Number(form.grace_minutes), work_days: form.work_days, color: form.color, ...(form.is_default ? { is_default: true } : {}) }
  const ok = await run('save', () => (form.id ? scheduleService.updateShift(form.id, input) : scheduleService.createShift(input)),
    form.id ? 'Shift saved' : 'Shift created')
  if (ok) {
    open.value = false
    shifts.load()
    lookups.loadSchedules(true).catch(() => {})
  }
}

async function remove(s: WorkSchedule) {
  if (!window.confirm(`Delete the shift "${s.name}"?`)) return
  if (await run(`d${s.id}`, () => scheduleService.deleteShift(s.id), 'Shift deleted')) {
    shifts.load()
    lookups.loadSchedules(true).catch(() => {})
  }
}
</script>

<template>
  <div>
    <PageHeader title="Shifts" subtitle="Working hours, grace period and usual work days. A night shift that ends the next morning is a cross-day shift.">
      <AppButton v-if="canEdit" @click="edit()">New shift</AppButton>
    </PageHeader>

    <StateView :loading="shifts.loading.value && !shifts.data.value" :error="shifts.error.value" :empty="shifts.data.value?.length === 0" empty-text="No shifts yet." @retry="shifts.load">
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>Shift</th><th>Hours</th><th>Work type</th><th>Grace</th><th>Work days</th><th>Staff</th><th v-if="canEdit" class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="s in shifts.data.value ?? []" :key="s.id">
                <td>
                  <span class="flex items-center gap-2 font-medium">
                    <span class="size-3 shrink-0 rounded-full" :style="{ background: s.color }" aria-hidden="true" />
                    {{ s.name }}
                    <StatusBadge v-if="s.is_default" tone="info" label="Default" />
                  </span>
                </td>
                <td class="whitespace-nowrap tabular-nums">{{ shiftHours(s.start_time, s.end_time, s.work_type === 'CROSS_DAY') }}</td>
                <td>
                  <StatusBadge v-if="s.work_type === 'CROSS_DAY'" tone="warning" label="Cross-day · night" />
                  <span v-else class="text-sm text-muted">Normal</span>
                </td>
                <td class="tabular-nums">{{ s.grace_minutes }} min</td>
                <td class="whitespace-nowrap">{{ workDaysLabel(parseWorkDays(s.work_days)) }}</td>
                <td class="tabular-nums">{{ s.employees ?? 0 }}</td>
                <td v-if="canEdit" class="whitespace-nowrap text-right">
                  <AppButton size="sm" variant="ghost" :aria-label="`Edit ${s.name}`" @click="edit(s)">Edit</AppButton>
                  <AppButton v-if="!s.is_default" size="sm" variant="ghost" :loading="busy === `d${s.id}`" :aria-label="`Delete ${s.name}`" @click="remove(s)">Delete</AppButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <p class="mt-3 text-xs text-muted">
        The default shift applies to staff without a usual shift. Change single days on the
        <NuxtLink to="/schedules/roster" class="link">weekly roster</NuxtLink>, or a date range with a
        <NuxtLink to="/schedules/special" class="link">special schedule</NuxtLink>.
      </p>
    </StateView>

    <AppModal v-model="open" :title="form.id ? 'Edit shift' : 'New shift'" wide>
      <form id="shift-form" class="grid gap-4 sm:grid-cols-2" @submit.prevent="save">
        <FormField label="Name" for="sh-name" class="sm:col-span-2">
          <input id="sh-name" v-model="form.name" class="input" required maxlength="100" placeholder="e.g. Night Shift" />
        </FormField>
        <FormField label="Work type" class="sm:col-span-2">
          <Segmented v-model="form.work_type" :options="typeOptions" label="Work type" />
        </FormField>
        <FormField label="Start" for="sh-start"><input id="sh-start" v-model="form.start_time" type="time" class="input" required /></FormField>
        <FormField label="End" for="sh-end" :hint="crossDay ? 'On the next day.' : undefined"><input id="sh-end" v-model="form.end_time" type="time" class="input" required /></FormField>
        <p class="text-sm sm:col-span-2" :class="hoursError ? 'text-danger' : 'text-muted'" role="status">
          <template v-if="hoursError">{{ hoursError }}</template>
          <template v-else-if="crossDay">Starts {{ form.start_time }}, ends the next morning at {{ form.end_time }} ({{ length }}). Attendance counts for the day it starts.</template>
          <template v-else>{{ form.start_time }}–{{ form.end_time }} ({{ length }}).</template>
        </p>
        <FormField label="Grace period (minutes)" for="sh-grace" hint="Late only after this many minutes.">
          <input id="sh-grace" v-model.number="form.grace_minutes" type="number" min="0" max="240" class="input" required />
        </FormField>
        <FormField label="Colour">
          <div class="flex flex-wrap items-center gap-1.5" role="radiogroup" aria-label="Colour">
            <button
              v-for="c in palette"
              :key="c"
              type="button"
              role="radio"
              :aria-checked="form.color === c"
              :aria-label="c"
              class="size-7 rounded-full ring-offset-2 ring-offset-surface transition"
              :class="form.color === c ? 'ring-2 ring-fg' : ''"
              :style="{ background: c }"
              @click="form.color = c"
            />
          </div>
        </FormField>
        <FormField label="Usual work days" class="sm:col-span-2">
          <WeekdayPicker v-model="form.work_days" label="Usual work days" />
          <p v-if="!form.work_days.length" class="mt-1 text-xs text-danger">Choose at least one day.</p>
        </FormField>
        <label v-if="!form.id || !shifts.data.value?.find(s => s.id === form.id)?.is_default" class="flex items-center gap-2 text-sm sm:col-span-2">
          <input v-model="form.is_default" type="checkbox" class="size-4 accent-[var(--primary)]" />
          Make this the default shift (for staff without a usual shift)
        </label>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="open = false">Cancel</AppButton>
        <AppButton type="submit" form="shift-form" :loading="busy === 'save'" :disabled="!!hoursError || !form.work_days.length">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
