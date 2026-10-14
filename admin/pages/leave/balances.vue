<script setup lang="ts">
import { leaveService } from '~/services/leave.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { EmployeeBalances, LeaveBalance } from '~/types/api'
import { todayISO } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const thisYear = Number(todayISO(auth.company.timezone).slice(0, 4))
const filter = reactive({ year: thisYear, branch_id: '', department_id: '', q: '' })
// Departments belong to one branch: drop a choice the new branch doesn't have.
watch(() => filter.branch_id, (b) => {
  if (!lookups.fits('departments', filter.department_id, b)) filter.department_id = ''
}, { flush: 'sync' })
// Leave types are per branch, so balances are shown one branch at a time (the
// first one until another is picked); an admin of one branch just sees theirs.
const pickBranch = computed(() => auth.multiBranch)
const data = useResource(
  () => leaveService.balances({ year: filter.year, branch_id: filter.branch_id || undefined, department_id: filter.department_id || undefined, q: filter.q.trim() || undefined }),
  { immediate: false },
)
watch(() => [filter.year, filter.branch_id, filter.department_id], () => data.load())
onMounted(async () => {
  if (!pickBranch.value) return data.load()
  const branches = await lookups.loadBranches().catch(() => [])
  if (branches.length) filter.branch_id = String((branches.find(b => b.status === 'ACTIVE') ?? branches[0])!.id) // loads via the watch
  else data.load()
})
let typing: ReturnType<typeof setTimeout> | undefined
watch(() => filter.q, () => {
  clearTimeout(typing)
  typing = setTimeout(() => data.load(), 300)
})
onMounted(() => lookups.loadDepartments().catch(() => {}))

const rows = computed(() => data.data.value?.items ?? [])
// Columns: types with an allowance, plus any with someone's own entitlement.
const columns = computed(() => (data.data.value?.types ?? []).filter(t => t.annual_days > 0 || rows.value.some(r => r.balances.some(b => b.leave_type_id === t.id && b.tracked))))
const cell = (r: EmployeeBalances, typeId: number) => r.balances.find(b => b.leave_type_id === typeId)
const fmt = (n: number) => (n % 1 ? n.toFixed(1) : String(n))
const totals = computed(() => columns.value.map(t => {
  const bs = rows.value.map(r => cell(r, t.id)).filter((b): b is LeaveBalance => !!b)
  return { used: bs.reduce((s, b) => s + b.used, 0), pending: bs.reduce((s, b) => s + b.pending, 0) }
}))

// ---------- edit one entitlement ----------
const editing = ref<{ row: EmployeeBalances; b: LeaveBalance } | null>(null)
const form = reactive({ allocated: 0, carried: 0, note: '' })
const editOpen = computed({ get: () => !!editing.value, set: v => !v && (editing.value = null) })
function edit(row: EmployeeBalances, b?: LeaveBalance) {
  if (!row.can_edit || !b) return
  editing.value = { row, b }
  Object.assign(form, { allocated: b.allocated, carried: b.carried, note: b.note ?? '' })
}
const typeDefault = computed(() => columns.value.find(t => t.id === editing.value?.b.leave_type_id)?.annual_days ?? 0)
const { busy, run } = useAction()
async function save() {
  const e = editing.value
  if (!e) return
  const ok = await run('save', () => leaveService.setBalance({ employee_id: e.row.employee_id, leave_type_id: e.b.leave_type_id, year: filter.year,
    allocated: Number(form.allocated), carried: Number(form.carried), note: form.note.trim() }), 'Balance saved')
  if (ok) {
    editing.value = null
    data.load()
  }
}
async function reset() {
  const e = editing.value
  if (!e) return
  if (await run('reset', () => leaveService.resetBalance(e.row.employee_id, e.b.leave_type_id, filter.year), 'Back to the default allowance')) {
    editing.value = null
    data.load()
  }
}
</script>

<template>
  <div>
    <PageHeader title="Leave balances" subtitle="Each person's entitlement, used and pending days, and what is left — per leave type and year. Click a balance to adjust it." />

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4">
      <FormField label="Year" for="lb-year">
        <div class="flex items-center gap-1">
          <AppButton variant="secondary" aria-label="Previous year" @click="filter.year--">‹</AppButton>
          <span id="lb-year" class="input flex-1 text-center tabular-nums">{{ filter.year }}</span>
          <AppButton variant="secondary" aria-label="Next year" @click="filter.year++">›</AppButton>
        </div>
      </FormField>
      <FormField v-if="pickBranch" label="Branch" for="lb-branch">
        <SearchSelect id="lb-branch" v-model="filter.branch_id" :options="lookups.branchOptions" />
      </FormField>
      <FormField label="Department" for="lb-dept"><SearchSelect id="lb-dept" v-model="filter.department_id" :options="lookups.departmentOptions(filter.branch_id)" empty-label="All" /></FormField>
      <FormField label="Search" for="lb-q"><input id="lb-q" v-model="filter.q" type="search" class="input" placeholder="Name or employee ID" /></FormField>
    </div>

    <StateView :loading="data.loading.value && !data.data.value" :error="data.error.value" :empty="!rows.length" empty-text="No staff match these filters." @retry="data.load">
      <p v-if="!columns.length" class="card p-6 text-center text-sm text-muted">
        No leave type has a yearly allowance. Set one on <NuxtLink to="/leave/types" class="link">Leave types</NuxtLink>.
      </p>
      <div v-else class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[40rem] border-separate border-spacing-0 text-sm">
            <thead>
              <tr>
                <th class="sticky left-0 z-10 border-b border-border bg-surface px-3 py-2.5 text-left text-xs font-medium uppercase tracking-wide text-muted">Employee</th>
                <th v-for="t in columns" :key="t.id" class="border-b border-l border-border px-3 py-2.5 text-left text-xs font-medium text-muted">
                  <span class="flex items-center gap-1.5 uppercase tracking-wide"><span class="size-2.5 rounded-full" :style="{ background: t.color }" />{{ t.name }}</span>
                  <span class="font-normal normal-case">{{ fmt(t.annual_days) }} days / year</span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in rows" :key="r.employee_id">
                <th scope="row" class="sticky left-0 z-10 border-b border-border bg-surface px-3 py-2 text-left font-normal">
                  <span class="block font-medium">{{ r.full_name }}</span>
                  <span class="block text-xs text-muted">{{ r.employee_code }}<template v-if="r.branch && auth.multiBranch"> · {{ r.branch }}</template></span>
                </th>
                <td v-for="t in columns" :key="t.id" class="border-b border-l border-border p-1">
                  <template v-for="b in [cell(r, t.id)]" :key="t.id">
                    <button
                      v-if="b"
                      type="button"
                      class="w-full rounded-md px-2 py-1.5 text-left transition"
                      :class="r.can_edit ? 'hover:bg-surface-2' : 'cursor-default'"
                      :disabled="!r.can_edit"
                      :aria-label="`${r.full_name}, ${t.name}: ${fmt(b.remaining)} of ${fmt(b.allocated + b.carried)} days left${r.can_edit ? '. Adjust' : ''}`"
                      @click="edit(r, b)"
                    >
                      <span class="flex items-baseline gap-1">
                        <span class="text-base font-semibold tabular-nums" :class="b.available < 0 ? 'text-danger' : b.remaining === 0 ? 'text-muted' : ''">{{ fmt(b.remaining) }}</span>
                        <span class="text-xs text-muted">/ {{ fmt(b.allocated + b.carried) }} left</span>
                        <span v-if="b.custom" class="ml-auto rounded bg-info/10 px-1 text-[10px] font-medium text-info" :title="b.note || 'Adjusted by HR'">custom</span>
                      </span>
                      <span class="block text-xs text-muted">
                        used {{ fmt(b.used) }}<template v-if="b.pending"> · <span class="text-warning">{{ fmt(b.pending) }} pending</span></template><template v-if="b.carried"> · +{{ fmt(b.carried) }} carried</template>
                      </span>
                    </button>
                  </template>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <th scope="row" class="sticky left-0 z-10 bg-surface px-3 py-2 text-left text-xs font-medium text-muted">Total taken</th>
                <td v-for="(t, i) in columns" :key="t.id" class="border-l border-border px-3 py-2 text-xs tabular-nums text-muted">
                  {{ fmt(totals[i]!.used) }} day{{ totals[i]!.used === 1 ? '' : 's' }}<template v-if="totals[i]!.pending"> · {{ fmt(totals[i]!.pending) }} pending</template>
                </td>
              </tr>
            </tfoot>
          </table>
        </div>
      </div>
      <p class="mt-3 text-xs text-muted">Days count in the year a request starts. "Left" excludes pending requests' days until they are approved; they are still reserved when people ask for more.</p>
    </StateView>

    <AppModal v-model="editOpen" :title="editing ? `${editing.row.full_name} · ${editing.b.name} ${filter.year}` : 'Adjust balance'">
      <form v-if="editing" id="lb-form" class="grid gap-4 sm:grid-cols-2" @submit.prevent="save">
        <FormField label="Entitlement (days)" for="lb-alloc" :hint="`Default for this type: ${fmt(typeDefault)}`">
          <input id="lb-alloc" v-model.number="form.allocated" type="number" min="0" max="365" step="0.5" class="input" required />
        </FormField>
        <FormField label="Carried over (days)" for="lb-carry" :hint="`From ${filter.year - 1}`">
          <input id="lb-carry" v-model.number="form.carried" type="number" min="0" max="365" step="0.5" class="input" />
        </FormField>
        <FormField label="Note" for="lb-note" class="sm:col-span-2">
          <input id="lb-note" v-model="form.note" class="input" maxlength="255" placeholder="e.g. Joined in July — pro-rated" />
        </FormField>
        <p class="rounded-lg bg-surface-2 px-3 py-2 text-sm sm:col-span-2">
          Used {{ fmt(editing.b.used) }}, pending {{ fmt(editing.b.pending) }} →
          <b>{{ fmt(Number(form.allocated) + Number(form.carried) - editing.b.used) }}</b> days left.
        </p>
      </form>
      <template #footer>
        <AppButton v-if="editing?.b.custom" variant="ghost" class="mr-auto" :loading="busy === 'reset'" @click="reset">Reset to default</AppButton>
        <AppButton variant="secondary" @click="editing = null">Cancel</AppButton>
        <AppButton type="submit" form="lb-form" :loading="busy === 'save'">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
