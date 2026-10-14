<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { useLookupsStore } from '~/stores/lookups'
import type { SystemUser } from '~/types/api'

definePageMeta({ permission: 'users.manage' })

const lookups = useLookupsStore()
const users = useResource(() => accessService.users())
onMounted(() => {
  lookups.loadBranches(true).catch(() => {})
  lookups.loadRoles().catch(() => {})
})
const branches = computed(() => (lookups.branches ?? []).filter(b => b.status === 'ACTIVE'))
const { busy, run } = useAction()

// Pending edits per user id: the branch ids ticked (own branch excluded).
const draft = ref<Record<number, number[]>>({})
const ticked = (u: SystemUser) => draft.value[u.id] ?? (u.scope === 'all' ? [] : assigned(u))
const changed = (u: SystemUser) => draft.value[u.id] !== undefined && [...draft.value[u.id]!].sort().join() !== [...assigned(u)].sort().join()
const companyWide = (u: SystemUser) => !!lookups.roles?.find(r => r.code === u.role)?.permissions.includes('all.view')
const branchLevel = (u: SystemUser) => !!lookups.roles?.find(r => r.code === u.role)?.permissions.includes('branch.view')
// A branch-level role always reaches the person's own branch: not a ticked assignment.
const assigned = (u: SystemUser) => u.branch_ids.filter(id => !(branchLevel(u) && !companyWide(u) && id === u.branch_id))
// Branch powers come from the role; team-only roles (managers) don't use branch access.
const editable = (u: SystemUser) => !u.self && (companyWide(u) || branchLevel(u))
const ownBranch = (u: SystemUser, id: number) => branchLevel(u) && u.branch_id === id

function toggle(u: SystemUser, id: number) {
  const now = ticked(u)
  draft.value = { ...draft.value, [u.id]: now.includes(id) ? now.filter(x => x !== id) : [...now, id] }
}
function summary(u: SystemUser) {
  if (!editable(u) && !u.self) return u.scope === 'none' ? 'No panel access' : 'Team only (role has no branch powers)'
  const t = ticked(u)
  if (companyWide(u)) return t.length ? `${t.length} of ${branches.value.length} branches` : 'All branches'
  return `Own branch${t.length ? ` + ${t.length}` : ''}`
}
async function save(u: SystemUser) {
  if (await run(`s${u.id}`, () => accessService.setBranches(u.id, ticked(u)), `Branch access saved for ${u.full_name}`)) {
    const next = { ...draft.value }
    delete next[u.id]
    draft.value = next
    users.load()
  }
}
const rows = computed(() => (users.data.value ?? []).filter(u => u.status === 'ACTIVE'))
</script>

<template>
  <div>
    <PageHeader title="Branch assignment" subtitle="Limit system users to specific branches. Users with no branches ticked can manage all branches; a branch admin always has their own branch." />

    <StateView :loading="users.loading.value && !users.data.value" :error="users.error.value" :empty="!rows.length" empty-text="No active system users." @retry="users.load">
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full min-w-[44rem] border-separate border-spacing-0 text-sm">
            <thead>
              <tr>
                <th class="sticky left-0 z-10 border-b border-border bg-surface px-3 py-2.5 text-left text-xs font-medium uppercase tracking-wide text-muted">User</th>
                <th class="border-b border-border px-3 py-2.5 text-left text-xs font-medium uppercase tracking-wide text-muted">Access</th>
                <th v-for="b in branches" :key="b.id" class="border-b border-l border-border px-2 py-2.5 text-center text-xs font-medium text-muted">{{ b.name }}</th>
                <th class="border-b border-border px-3 py-2.5" />
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in rows" :key="u.id" :class="!editable(u) && 'text-muted'">
                <th scope="row" class="sticky left-0 z-10 border-b border-border bg-surface px-3 py-2 text-left font-normal">
                  <span class="block font-medium text-fg">{{ u.full_name }}<span v-if="u.self" class="ml-1 text-xs font-normal text-muted">(you)</span></span>
                  <span class="block text-xs text-muted">{{ u.role_name }}</span>
                </th>
                <td class="border-b border-border px-3 py-2">
                  <StatusBadge v-if="editable(u) && companyWide(u) && !ticked(u).length" tone="info" label="All branches" />
                  <span v-else class="text-xs">{{ summary(u) }}</span>
                </td>
                <td v-for="b in branches" :key="b.id" class="border-b border-l border-border text-center">
                  <input
                    type="checkbox"
                    class="size-4 accent-[var(--primary)] disabled:opacity-40"
                    :checked="ownBranch(u, b.id) || ticked(u).includes(b.id)"
                    :disabled="!editable(u) || ownBranch(u, b.id)"
                    :title="ownBranch(u, b.id) ? 'Their own branch (branch admin)' : undefined"
                    :aria-label="`${u.full_name}: ${b.name}`"
                    @change="toggle(u, b.id)"
                  />
                </td>
                <td class="whitespace-nowrap border-b border-border px-3 py-2 text-right">
                  <AppButton v-if="changed(u)" size="sm" :loading="busy === `s${u.id}`" @click="save(u)">Save</AppButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <p class="mt-3 text-xs text-muted">
        Branch access limits where a role's powers apply (staff lists, attendance, leave, reports, roster).
        What they may do there is set by their role in <NuxtLink to="/access/roles" class="link">Role management</NuxtLink>. You cannot limit your own access.
      </p>
    </StateView>
  </div>
</template>
