<script setup lang="ts">
import { employeeService } from '~/services/employee.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import { employeeStatus, roleLabels } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const route = useRoute()
const allBranches = computed(() => auth.can('all.view'))
const filter = reactive({
  q: '',
  department_id: '',
  branch_id: typeof route.query.branch_id === 'string' ? route.query.branch_id : '',
  status: '',
  include_inactive: false,
})

const list = useResource(() =>
  employeeService.list({
    q: filter.q.trim() || undefined,
    department_id: Number(filter.department_id) || undefined,
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
watch(() => [filter.department_id, filter.branch_id, filter.status, filter.include_inactive], () => list.load())
onMounted(() => {
  lookups.loadDepartments().catch(() => {})
  if (allBranches.value) lookups.loadBranches().catch(() => {})
})
</script>

<template>
  <div>
    <PageHeader
      title="Employees"
      :subtitle="auth.can('all.view') ? 'Everyone in the company.' : auth.can('branch.view') ? `Everyone in ${auth.employee?.branch?.name ?? 'your branch'}.` : 'Your team.'"
    >
      <AppButton v-if="auth.can('staff.manage')" @click="navigateTo('/employees/new')">Add employee</AppButton>
    </PageHeader>

    <div class="card mb-4 grid gap-3 p-4" :class="allBranches ? 'sm:grid-cols-5' : 'sm:grid-cols-4'">
      <FormField label="Search" for="e-q"><input id="e-q" v-model="filter.q" class="input" type="search" placeholder="Name or employee ID" /></FormField>
      <FormField v-if="allBranches" label="Branch" for="e-branch">
        <select id="e-branch" v-model="filter.branch_id" class="input">
          <option value="">All</option>
          <option v-for="b in lookups.branches ?? []" :key="b.id" :value="String(b.id)">{{ b.name }}</option>
        </select>
      </FormField>
      <FormField label="Department" for="e-dept">
        <select id="e-dept" v-model="filter.department_id" class="input">
          <option value="">All</option>
          <option v-for="d in lookups.departments ?? []" :key="d.id" :value="String(d.id)">{{ d.name }}</option>
        </select>
      </FormField>
      <FormField label="Status" for="e-status">
        <select id="e-status" v-model="filter.status" class="input">
          <option value="">All</option>
          <option value="ACTIVE">Active</option>
          <option value="SUSPENDED">Suspended</option>
          <option value="INACTIVE">Inactive</option>
        </select>
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
              <td>{{ e.role ? roleLabels[e.role.code] : '' }}</td>
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
