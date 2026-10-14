<script setup lang="ts">
import { employeeService } from '~/services/employee.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import { employeeStatus } from '~/utils/format'
import { optionsFrom } from '~/utils/options'

const statusOptions = optionsFrom(employeeStatus)

const auth = useAuthStore()
const lookups = useLookupsStore()
const route = useRoute()
const allBranches = computed(() => auth.multiBranch) // a branch column and filter
const fromUrl = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const filter = reactive({
  q: '',
  department_id: fromUrl('department_id'),
  position_id: fromUrl('position_id'),
  branch_id: fromUrl('branch_id'),
  status: '',
  include_inactive: false,
})
// Departments and positions belong to one branch: drop a choice the new branch doesn't have.
watch(() => filter.branch_id, (b) => {
  if (!lookups.fits('departments', filter.department_id, b)) filter.department_id = ''
  if (!lookups.fits('positions', filter.position_id, b)) filter.position_id = ''
}, { flush: 'sync' })

const list = useResource(() =>
  employeeService.list({
    q: filter.q.trim() || undefined,
    department_id: Number(filter.department_id) || undefined,
    position_id: Number(filter.position_id) || undefined,
    branch_id: Number(filter.branch_id) || undefined,
    status: filter.status || undefined,
    include_inactive: filter.include_inactive || undefined,
  }),
)
let timer: ReturnType<typeof setTimeout> | undefined
watch(() => filter.q, () => {
  clearTimeout(timer)
  timer = setTimeout(list.load, 250)
})
watch(() => [filter.department_id, filter.position_id, filter.branch_id, filter.status, filter.include_inactive], () => list.load())
onMounted(() => {
  lookups.loadDepartments().catch(() => {})
  lookups.loadPositions().catch(() => {})
  if (allBranches.value) lookups.loadBranches().catch(() => {})
})
</script>

<template>
  <div>
    <PageHeader
      title="Employees"
      :subtitle="auth.can('all.view') && auth.allBranches ? 'Everyone in the company.'
        : auth.branchIds.length > 1 ? `Everyone in your ${auth.branchIds.length} branches.`
        : auth.can('branch.view') || auth.can('all.view') ? `Everyone in ${auth.employee?.branch?.name ?? 'your branch'}.` : 'Your team.'"
    >
      <AppButton v-if="auth.can('staff.manage')" @click="navigateTo('/employees/new')">Add employee</AppButton>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4 sm:grid-cols-2" :class="allBranches ? 'lg:grid-cols-6' : 'lg:grid-cols-5'">
      <FormField label="Search" for="e-q"><input id="e-q" v-model="filter.q" class="input" type="search" placeholder="Name or employee ID" /></FormField>
      <FormField v-if="allBranches" label="Branch" for="e-branch">
        <SearchSelect id="e-branch" v-model="filter.branch_id" :options="lookups.branchOptions" empty-label="All" />
      </FormField>
      <FormField label="Department" for="e-dept">
        <SearchSelect id="e-dept" v-model="filter.department_id" :options="lookups.departmentOptions(filter.branch_id)" empty-label="All" />
      </FormField>
      <FormField label="Position" for="e-pos">
        <SearchSelect id="e-pos" v-model="filter.position_id" :options="lookups.positionOptions(filter.branch_id)" empty-label="All" />
      </FormField>
      <FormField label="Status" for="e-status">
        <SearchSelect id="e-status" v-model="filter.status" :options="statusOptions" empty-label="All" />
      </FormField>
      <label class="flex items-end gap-2 pb-2 text-sm">
        <input v-model="filter.include_inactive" type="checkbox" class="size-4 accent-[var(--primary)]" />
        Show deactivated
      </label>
    </div>

    <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.data.value?.length === 0" empty-text="No employees match." @retry="list.load">
      <div class="card overflow-x-auto">
        <table class="table">
          <thead><tr><th>ID</th><th>Name</th><th v-if="allBranches">Branch</th><th>Department</th><th>Position</th><th>Role</th><th>Mini App</th><th>Status</th></tr></thead>
          <tbody>
            <tr v-for="e in list.data.value ?? []" :key="e.id" class="cursor-pointer hover:bg-surface-2/60" @click="navigateTo(`/employees/${e.id}`)">
              <td class="font-mono text-xs">{{ e.employee_code }}</td>
              <td><NuxtLink :to="`/employees/${e.id}`" class="font-medium hover:underline" @click.stop>{{ e.full_name }}</NuxtLink></td>
              <td v-if="allBranches" class="text-muted">{{ e.branch?.name || 'All branches' }}</td>
              <td class="text-muted">{{ e.department?.name || '—' }}</td>
              <td class="text-muted">{{ e.position || '—' }}</td>
              <td>{{ e.role?.name ?? '' }}</td>
              <td>
                <StatusBadge v-if="e.telegram_account" tone="success" :label="e.telegram_account.username ? `@${e.telegram_account.username}` : 'Linked'" />
                <StatusBadge v-else tone="warning" label="Not linked" />
              </td>
              <td><StatusBadge v-bind="e.deleted_at ? { label: 'Deactivated', tone: 'neutral' } : employeeStatus[e.status]" /></td>
            </tr>
          </tbody>
        </table>
      </div>
    </StateView>
  </div>
</template>
