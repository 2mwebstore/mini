<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { useLookupsStore } from '~/stores/lookups'
import type { SystemUser } from '~/types/api'
import type { SelectOption } from '~/utils/options'

definePageMeta({ permission: 'users.manage' })

const lookups = useLookupsStore()
const users = useResource(() => accessService.users())
onMounted(() => {
  lookups.loadBranches(true).catch(() => {})
  lookups.loadRoles().catch(() => {})
})
const branches = computed(() => (lookups.branches ?? []).filter(b => b.status === 'ACTIVE'))
const { busy, run } = useAction()

const companyWide = (u: SystemUser) => u.role === 'super_admin' || !!lookups.roles?.find(r => r.code === u.role)?.permissions.includes('all.view')
const branchLevel = (u: SystemUser) => !!lookups.roles?.find(r => r.code === u.role)?.permissions.includes('branch.view')
// A branch-level role always reaches the person's own branch: it is not an assignment.
const assigned = (u: SystemUser) => u.branch_ids.filter(id => !(branchLevel(u) && !companyWide(u) && id === u.branch_id))
// Branch powers come from the role; roles without them don't use branch access.
const editable = (u: SystemUser) => !u.self && (companyWide(u) || branchLevel(u))
const options = (u: SystemUser): SelectOption[] => branches.value
  .filter(b => !(branchLevel(u) && !companyWide(u) && b.id === u.branch_id))
  .map(b => ({ value: String(b.id), label: b.name, sub: b.code }))

// Pending edits per user id (branch ids as strings, for the multi-select).
const draft = ref<Record<number, string[]>>({})
const picked = (u: SystemUser) => draft.value[u.id] ?? (u.scope === 'all' ? [] : assigned(u).map(String))
const setPicked = (u: SystemUser, v: string[]) => (draft.value = { ...draft.value, [u.id]: v })
const changed = (u: SystemUser) => draft.value[u.id] !== undefined && [...draft.value[u.id]!].sort().join() !== assigned(u).map(String).sort().join()
function summary(u: SystemUser) {
  if (u.self) return 'Your own access cannot be limited.'
  if (!editable(u)) return u.scope === 'none' ? 'No panel access with this role.' : 'This role has no branch powers.'
  const n = picked(u).length
  if (companyWide(u)) return n ? `Only these ${n} of ${branches.value.length} branches.` : 'No branches chosen = all branches.'
  return `Their own branch (${u.branch_name || '—'})${n ? ` + ${n} more` : ''}.`
}
async function save(u: SystemUser) {
  if (await run(`s${u.id}`, () => accessService.setBranches(u.id, picked(u).map(Number)), `Branch access saved for ${u.full_name}`)) {
    const next = { ...draft.value }
    delete next[u.id]
    draft.value = next
    users.load()
  }
}
function reset(u: SystemUser) {
  const next = { ...draft.value }
  delete next[u.id]
  draft.value = next
}
const rows = computed(() => (users.data.value ?? []).filter(u => u.status === 'ACTIVE'))
</script>

<template>
  <div>
    <PageHeader title="Branch assignment" subtitle="Choose which branches each system user can manage. Users with no branches chosen can manage all branches; a branch admin always has their own branch." />

    <StateView :loading="users.loading.value && !users.data.value" :error="users.error.value" :empty="!rows.length" empty-text="No active system users." @retry="users.load">
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>User</th><th class="w-1/2">Branches</th><th>Access</th><th class="text-right" /></tr></thead>
            <tbody>
              <tr v-for="u in rows" :key="u.id">
                <td>
                  <span class="block font-medium">{{ u.full_name }}<span v-if="u.self" class="ml-1 text-xs font-normal text-muted">(you)</span></span>
                  <span class="block text-xs text-muted">{{ u.role_name }}<template v-if="u.branch_name"> · {{ u.branch_name }}</template></span>
                </td>
                <td class="min-w-64">
                  <MultiSelect
                    v-if="editable(u)"
                    :id="`ba-${u.id}`"
                    :model-value="picked(u)"
                    :options="options(u)"
                    noun="branch"
                    search-placeholder="Search branches…"
                    :label="`Branches for ${u.full_name}`"
                    :placeholder="companyWide(u) ? 'All branches' : 'Only their own branch'"
                    @update:model-value="v => setPicked(u, v)"
                  />
                  <span v-else class="text-sm text-muted">{{ u.scope === 'all' ? 'All branches' : u.scope === 'none' ? '—' : u.branch_ids.map(id => branches.find(b => b.id === id)?.name ?? id).join(', ') || '—' }}</span>
                </td>
                <td class="text-xs" :class="editable(u) ? '' : 'text-muted'">
                  <StatusBadge v-if="editable(u) && companyWide(u) && !picked(u).length" tone="info" label="All branches" class="mb-0.5" />
                  <span class="block">{{ summary(u) }}</span>
                </td>
                <td class="whitespace-nowrap text-right">
                  <template v-if="changed(u)">
                    <AppButton size="sm" variant="ghost" @click="reset(u)">Undo</AppButton>
                    <AppButton size="sm" :loading="busy === `s${u.id}`" @click="save(u)">Save</AppButton>
                  </template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <p class="mt-3 text-xs text-muted">
        Branch access limits where a role's powers apply (staff lists, attendance, leave, reports, roster).
        What they may do there is set by their role in <NuxtLink to="/access/roles" class="link">Role management</NuxtLink>.
      </p>
    </StateView>
  </div>
</template>
