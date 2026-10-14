<script setup lang="ts">
import { scheduleService } from '~/services/schedule.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { RosterCell, RosterItem, RosterRow } from '~/types/api'
import { addDays, formatDate, formatDayMonth, isoWeekday, mondayOf, shiftHours, todayISO, weekdayShort } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const tz = auth.company.timezone
const route = useRoute()
const router = useRouter()
const today = todayISO(tz)

// Filters live in the URL so a week can be bookmarked or shared.
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const filter = reactive({ week: mondayOf(q('week') || today), branch_id: q('branch_id'), department_id: q('department_id'), q: q('q') })
const query = computed(() => ({
  from: filter.week,
  branch_id: filter.branch_id || undefined,
  department_id: filter.department_id || undefined,
  q: filter.q.trim() || undefined,
}))
const roster = useResource(() => scheduleService.roster(query.value))
watch(() => [filter.week, filter.branch_id, filter.department_id], () => roster.load())
let typing: ReturnType<typeof setTimeout> | undefined
watch(() => filter.q, () => {
  clearTimeout(typing)
  typing = setTimeout(() => roster.load(), 300)
})
watch(query, v => router.replace({ query: Object.fromEntries(Object.entries({ week: v.from, ...v, from: undefined }).filter(([, x]) => x)) }))
onMounted(() => lookups.loadDepartments().catch(() => {}))

const rows = computed(() => roster.data.value?.rows ?? [])
const days = computed(() => (roster.data.value?.days ?? []).map(d => d.slice(0, 10)))
const shifts = computed(() => roster.data.value?.shifts ?? [])
const weekLabel = computed(() => `${formatDayMonth(filter.week)} – ${formatDate(addDays(filter.week, 6))}`)
const thisWeek = computed(() => filter.week === mondayOf(today))
const canEditAny = computed(() => rows.value.some(r => r.can_edit))
const worksOn = (c: RosterCell) => c.work && !c.leave && !c.holiday
const coverage = computed(() => days.value.map((_, i) => rows.value.filter(r => r.cells[i] && worksOn(r.cells[i]!)).length))

function cellLabel(row: RosterRow, c: RosterCell) {
  const day = `${weekdayShort[isoWeekday(c.date) - 1]} ${formatDayMonth(c.date)}`
  let what = c.leave ? `on leave (${c.leave})` : c.holiday && c.work ? `holiday (${c.holiday})` : !c.work ? 'day off'
    : `${c.shift_name} ${shiftHours(c.start, c.end, c.cross_day)}${c.cross_day ? ', ends next day' : ''}`
  if (c.source === 'ROSTER') what += ', set on the roster'
  if (c.source === 'SPECIAL') what += `, special schedule${c.note ? `: ${c.note}` : ''}`
  return `${row.full_name}, ${day}: ${what}`
}
function cellStyle(c: RosterCell) {
  if (!worksOn(c) || !c.color) return undefined
  return { background: `${c.color}1f`, borderLeftColor: c.color }
}

// ---------- change a day ----------
const editing = ref<{ row: RosterRow; index: number } | null>(null)
const choice = ref('reset') // a shift id, 'off' or 'reset' (the usual pattern)
const wholeWeek = ref(false)
const editOpen = computed({ get: () => !!editing.value, set: v => !v && (editing.value = null) })
const editCell = computed(() => (editing.value ? editing.value.row.cells[editing.value.index] ?? null : null))
function openCell(row: RosterRow, i: number) {
  if (!row.can_edit) return
  const c = row.cells[i]!
  editing.value = { row, index: i }
  wholeWeek.value = false
  choice.value = c.source === 'ROSTER' ? (c.work && c.shift_id ? String(c.shift_id) : 'off') : 'reset'
}
const { busy, run } = useAction()
async function saveCell() {
  const e = editing.value
  if (!e) return
  const indexes = wholeWeek.value ? e.row.cells.map((_, i) => i) : [e.index]
  const items = indexes
    .filter(i => e.row.cells[i]!.source !== 'SPECIAL') // a special schedule wins anyway
    .map((i): RosterItem => {
      const it: RosterItem = { employee_id: e.row.employee_id, date: e.row.cells[i]!.date.slice(0, 10) }
      if (choice.value === 'off') it.off = true
      else if (choice.value === 'reset') it.reset = true
      else it.work_schedule_id = Number(choice.value)
      return it
    })
  if (!items.length) {
    editing.value = null
    return
  }
  if (await run('cell', () => scheduleService.setRoster(items), 'Roster updated')) {
    editing.value = null
    roster.load()
  }
}

async function copyLastWeek() {
  if (!window.confirm(`Copy last week's roster into the week of ${formatDate(filter.week)}? Days already set this week are overwritten.`)) return
  if (await run('copy', () => scheduleService.copyRoster(addDays(filter.week, -7), filter.week, filter.branch_id), 'Last week copied')) roster.load()
}
const exportUrl = (format: 'xlsx' | 'csv') => scheduleService.exportUrl(query.value, format)
</script>

<template>
  <div>
    <PageHeader title="Weekly roster" subtitle="Who works which shift each day. Click a day to change it.">
      <AppButton v-if="canEditAny" variant="secondary" :loading="busy === 'copy'" @click="copyLastWeek">Copy last week</AppButton>
      <template v-if="auth.can('report.export')">
        <a :href="exportUrl('xlsx')" class="inline-flex items-center gap-2 rounded-lg border border-border bg-surface px-3.5 py-2 text-sm font-medium hover:bg-surface-2">
          <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 3v12m0 0-4-4m4 4 4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" /></svg>
          Excel
        </a>
        <a :href="exportUrl('csv')" class="rounded-lg border border-border bg-surface px-3.5 py-2 text-sm font-medium hover:bg-surface-2">CSV</a>
      </template>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4">
      <FormField label="Week" for="r-week">
        <div class="flex items-center gap-1">
          <AppButton variant="secondary" aria-label="Previous week" @click="filter.week = addDays(filter.week, -7)">‹</AppButton>
          <input id="r-week" :value="filter.week" type="date" class="input min-w-0 flex-1" @change="e => (filter.week = mondayOf((e.target as HTMLInputElement).value || today))" />
          <AppButton variant="secondary" aria-label="Next week" @click="filter.week = addDays(filter.week, 7)">›</AppButton>
        </div>
      </FormField>
      <BranchFilter id="r-branch" v-model="filter.branch_id" />
      <FormField label="Department" for="r-dept">
        <SearchSelect id="r-dept" v-model="filter.department_id" :options="lookups.departmentOptions" empty-label="All" />
      </FormField>
      <FormField label="Search" for="r-q"><input id="r-q" v-model="filter.q" type="search" class="input" placeholder="Name or employee ID" /></FormField>
    </div>

    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <p class="text-sm font-medium">
        {{ weekLabel }}
        <button v-if="!thisWeek" type="button" class="ml-2 text-xs font-normal text-primary hover:underline" @click="filter.week = mondayOf(today)">This week</button>
      </p>
      <ul class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted" aria-label="Legend">
        <li v-for="s in shifts" :key="s.id" class="flex items-center gap-1"><span class="size-2.5 rounded-sm" :style="{ background: s.color }" />{{ s.name }}</li>
        <li class="flex items-center gap-1"><span class="text-primary">●</span> set on roster</li>
        <li class="flex items-center gap-1"><span class="text-warning">★</span> special schedule</li>
      </ul>
    </div>

    <StateView :loading="roster.loading.value && !roster.data.value" :error="roster.error.value" :empty="!rows.length" empty-text="No staff match these filters." @retry="roster.load">
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[60rem] table-fixed border-separate border-spacing-0 text-sm">
            <thead>
              <tr>
                <th class="sticky left-0 z-10 w-56 border-b border-border bg-surface px-3 py-2.5 text-left text-xs font-medium uppercase tracking-wide text-muted">Employee</th>
                <th
                  v-for="d in days"
                  :key="d"
                  class="border-b border-l border-border px-2 py-2 text-center text-xs font-medium"
                  :class="d === today ? 'bg-primary/10 text-primary' : 'text-muted'"
                  :aria-current="d === today ? 'date' : undefined"
                >
                  <span class="block uppercase tracking-wide">{{ weekdayShort[isoWeekday(d) - 1] }}</span>
                  <span class="block text-sm font-semibold text-fg">{{ formatDayMonth(d) }}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in rows" :key="row.employee_id">
                <th scope="row" class="sticky left-0 z-10 border-b border-border bg-surface px-3 py-2 text-left font-normal">
                  <span class="block truncate font-medium">{{ row.full_name }}</span>
                  <span class="block truncate text-xs text-muted">{{ row.employee_code }}<template v-if="row.branch && auth.can('all.view')"> · {{ row.branch }}</template> · {{ row.usual_shift }}</span>
                </th>
                <td v-for="(c, i) in row.cells" :key="c.date" class="border-b border-l border-border p-1" :class="days[i] === today && 'bg-primary/5'">
                  <button
                    type="button"
                    class="relative flex h-14 w-full flex-col justify-center rounded-md border-l-[3px] border-transparent px-2 text-left text-xs transition"
                    :class="[
                      c.leave ? 'bg-info/10 text-info' : c.holiday && c.work ? 'bg-warning/10 text-warning' : !c.work ? 'bg-surface-2 text-muted' : '',
                      row.can_edit ? 'cursor-pointer hover:ring-2 hover:ring-primary/30' : 'cursor-default',
                      c.source === 'SPECIAL' && 'outline-1 -outline-offset-2 outline-dashed outline-warning/60',
                    ]"
                    :style="cellStyle(c)"
                    :aria-label="cellLabel(row, c) + (row.can_edit ? '. Change' : '')"
                    :disabled="!row.can_edit"
                    @click="openCell(row, i)"
                  >
                    <template v-if="c.leave"><span class="font-medium">Leave</span><span class="truncate" :title="c.leave">{{ c.leave }}</span></template>
                    <template v-else-if="c.holiday && c.work"><span class="font-medium">Holiday</span><span class="truncate" :title="c.holiday">{{ c.holiday }}</span></template>
                    <template v-else-if="!c.work"><span class="font-medium">Off</span></template>
                    <template v-else>
                      <span class="truncate font-medium text-fg">{{ c.shift_name }}</span>
                      <span class="truncate whitespace-nowrap tabular-nums text-muted">{{ c.start }}–{{ c.end }}<sup v-if="c.cross_day" class="ml-0.5">+1</sup></span>
                    </template>
                    <span v-if="c.source === 'ROSTER'" class="absolute right-1 top-0.5 text-[10px] text-primary" aria-hidden="true">●</span>
                    <span v-if="c.source === 'SPECIAL'" class="absolute right-1 top-0.5 text-[11px] text-warning" aria-hidden="true">★</span>
                  </button>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <th scope="row" class="sticky left-0 z-10 bg-surface px-3 py-2 text-left text-xs font-medium text-muted">Working</th>
                <td v-for="(n, i) in coverage" :key="i" class="border-l border-border px-2 py-2 text-center text-xs tabular-nums text-muted">{{ n }} / {{ rows.length }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </div>
    </StateView>

    <AppModal v-model="editOpen" :title="editing && editCell ? `${editing.row.full_name} · ${weekdayShort[isoWeekday(editCell.date) - 1]} ${formatDayMonth(editCell.date)}` : 'Change day'">
      <template v-if="editing && editCell">
        <p v-if="editCell.source === 'SPECIAL'" class="mb-3 rounded-lg bg-warning/10 p-3 text-sm text-warning">
          ★ A special schedule decides this day<template v-if="editCell.note"> ({{ editCell.note }})</template>.
          Change it on <NuxtLink to="/schedules/special" class="font-medium underline">Special schedules</NuxtLink>.
        </p>
        <p v-if="editCell.leave" class="mb-3 rounded-lg bg-info/10 p-3 text-sm text-info">On approved leave ({{ editCell.leave }}).</p>
        <p v-else-if="editCell.holiday" class="mb-3 rounded-lg bg-surface-2 p-3 text-sm">Public holiday: {{ editCell.holiday }}.</p>
        <fieldset class="flex flex-col gap-1.5">
          <legend class="label">Works</legend>
          <label v-for="s in shifts" :key="s.id" class="flex cursor-pointer items-center gap-3 rounded-lg border border-border px-3 py-2 text-sm has-[:checked]:border-primary has-[:checked]:bg-primary/5">
            <input v-model="choice" type="radio" name="roster-choice" :value="String(s.id)" class="accent-[var(--primary)]" />
            <span class="size-3 shrink-0 rounded-full" :style="{ background: s.color }" aria-hidden="true" />
            <span class="flex-1 font-medium">{{ s.name }}</span>
            <span class="tabular-nums text-muted">{{ shiftHours(s.start_time, s.end_time, s.work_type === 'CROSS_DAY') }}</span>
          </label>
          <label class="flex cursor-pointer items-center gap-3 rounded-lg border border-border px-3 py-2 text-sm has-[:checked]:border-primary has-[:checked]:bg-primary/5">
            <input v-model="choice" type="radio" name="roster-choice" value="off" class="accent-[var(--primary)]" />
            <span class="flex-1 font-medium">Day off</span>
          </label>
          <label class="flex cursor-pointer items-center gap-3 rounded-lg border border-border px-3 py-2 text-sm has-[:checked]:border-primary has-[:checked]:bg-primary/5">
            <input v-model="choice" type="radio" name="roster-choice" value="reset" class="accent-[var(--primary)]" />
            <span class="flex-1"><span class="font-medium">Usual schedule</span> <span class="text-muted">· {{ editing.row.usual_shift }} on its work days</span></span>
          </label>
        </fieldset>
        <label class="mt-3 flex items-center gap-2 text-sm">
          <input v-model="wholeWeek" type="checkbox" class="size-4 accent-[var(--primary)]" />
          Apply to the whole week ({{ formatDayMonth(days[0] ?? filter.week) }} – {{ formatDayMonth(days[days.length - 1] ?? filter.week) }})
        </label>
      </template>
      <template #footer>
        <AppButton variant="secondary" @click="editing = null">Cancel</AppButton>
        <AppButton :loading="busy === 'cell'" :disabled="editCell?.source === 'SPECIAL' && !wholeWeek" @click="saveCell">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
