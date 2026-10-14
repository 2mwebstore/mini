<script setup lang="ts">
import { settingsService } from '~/services/settings.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Holiday } from '~/types/api'
import { formatDate } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const canEdit = computed(() => auth.can('schedules.manage'))
const { busy, run } = useAction()
const { confirm } = useConfirm()
// Each branch has its own holidays (a new one can go to several branches at once).
const { branchId, target, showBranch, branchName, picker } = useBranchPick()

const year = ref(new Date().getFullYear())
const holidays = useResource(() => settingsService.holidays(year.value, Number(branchId.value) || undefined))
watch([year, branchId], () => holidays.load())

// The same holiday in several branches is one row, with its branches.
const rows = computed(() => {
  const byDay = new Map<string, { date: string; name: string; items: Holiday[] }>()
  for (const h of holidays.data.value ?? []) {
    const key = `${h.date.slice(0, 10)}|${h.name}`
    const row = byDay.get(key) ?? byDay.set(key, { date: h.date, name: h.name, items: [] }).get(key)!
    row.items.push(h)
  }
  return [...byDay.values()]
})

const newHoliday = reactive({ date: '', name: '', branches: [] as string[] })
// New holidays go to the branch shown, or to every branch when all are shown.
watch([target, () => lookups.branches], () => {
  newHoliday.branches = target.value ? [target.value] : (lookups.branches ?? []).filter(b => b.status === 'ACTIVE').map(b => String(b.id))
}, { immediate: true })
async function addHoliday() {
  const ids = (picker.value ? newHoliday.branches : [target.value]).map(Number).filter(Boolean)
  if (!ids.length) return
  const n = ids.length
  if (await run('holiday', () => settingsService.addHoliday(newHoliday.date, newHoliday.name.trim(), ids), n > 1 ? `Holiday added to ${n} branches` : 'Holiday added')) {
    Object.assign(newHoliday, { date: '', name: '' })
    holidays.load()
  }
}
async function removeHoliday(h: Holiday, several: boolean) {
  if (several && !(await confirm({ title: `Remove “${h.name}” from ${branchName(h.branch_id)}?`, message: 'The other branches keep it.', confirmLabel: 'Remove' }))) return
  if (await run(`h${h.id}`, () => settingsService.deleteHoliday(h.id), 'Holiday removed')) holidays.load()
}
</script>

<template>
  <div>
    <PageHeader title="Public holidays" subtitle="Per branch: its staff are not marked absent on a holiday, and leave does not count it.">
      <div class="flex items-center gap-2">
        <AppButton variant="secondary" aria-label="Previous year" @click="year--">‹</AppButton>
        <span class="w-12 text-center text-sm font-medium tabular-nums">{{ year }}</span>
        <AppButton variant="secondary" aria-label="Next year" @click="year++">›</AppButton>
      </div>
    </PageHeader>

    <div v-if="picker" class="mb-4 max-w-xs">
      <FormField label="Branch" for="h-branch">
        <SearchSelect id="h-branch" v-model="branchId" :options="lookups.branchOptions" empty-label="All my branches" />
      </FormField>
    </div>

    <section class="card p-5">
      <form v-if="canEdit" class="mb-4 flex flex-wrap items-end gap-2" @submit.prevent="addHoliday">
        <FormField label="Date" for="h-date"><input id="h-date" v-model="newHoliday.date" class="input" type="date" required /></FormField>
        <FormField label="Name" for="h-name" class="min-w-48 flex-1"><input id="h-name" v-model="newHoliday.name" class="input" required maxlength="150" /></FormField>
        <FormField v-if="picker" label="Branches" for="h-branches" class="min-w-48">
          <MultiSelect id="h-branches" v-model="newHoliday.branches" :options="lookups.branchOptions" noun="branch" placeholder="Choose branches…" required />
        </FormField>
        <AppButton type="submit" :loading="busy === 'holiday'" :disabled="picker ? !newHoliday.branches.length : !target">Add holiday</AppButton>
      </form>
      <StateView :loading="holidays.loading.value" :error="holidays.error.value" :empty="rows.length === 0" :empty-text="`No holidays in ${year}.`" @retry="holidays.load">
        <ul class="divide-y divide-border">
          <li v-for="r in rows" :key="`${r.date}|${r.name}`" class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 py-2 text-sm">
            <span><span class="inline-block w-36 tabular-nums text-muted">{{ formatDate(r.date, true) }}</span> {{ r.name }}</span>
            <span v-if="showBranch" class="flex flex-wrap items-center gap-1">
              <span v-for="h in r.items" :key="h.id" class="inline-flex items-center gap-1 rounded-full bg-surface-2 py-0.5 pl-2.5 pr-1 text-xs">
                {{ branchName(h.branch_id) }}
                <button
                  v-if="canEdit" type="button" class="rounded-full px-1 text-muted hover:bg-border hover:text-fg disabled:opacity-50"
                  :disabled="busy === `h${h.id}`" :aria-label="`Remove ${h.name} from ${branchName(h.branch_id)}`" @click="removeHoliday(h, r.items.length > 1)"
                >×</button>
              </span>
            </span>
            <AppButton
              v-else-if="canEdit" size="sm" variant="ghost" :loading="busy === `h${r.items[0]!.id}`"
              :aria-label="`Remove ${r.name}`" @click="removeHoliday(r.items[0]!, false)"
            >Remove</AppButton>
          </li>
        </ul>
      </StateView>
    </section>
  </div>
</template>
