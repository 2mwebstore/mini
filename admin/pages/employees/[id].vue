<script setup lang="ts">
import { employeeService } from '~/services/employee.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { EmployeeInput } from '~/types/api'
import { employeeStatus, formatDate } from '~/utils/format'

const auth = useAuthStore()
const lookups = useLookupsStore()
const id = Number(useRoute().params.id)
const emp = useResource(() => employeeService.get(id))
const { busy, run } = useAction()
const formKey = ref(0) // re-mount the form with fresh values after a save

const canEdit = computed(() => auth.can('staff.manage') && !emp.data.value?.deleted_at)

async function save(input: EmployeeInput) {
  const ok = await run('save', async () => {
    emp.data.value = await employeeService.update(id, input)
  }, 'Changes saved')
  if (ok) {
    formKey.value++
    lookups.invalidateStaff()
  }
}

// ---------- deactivate ----------
const { confirm } = useConfirm()
async function deactivate() {
  const e = emp.data.value!
  const ok = await confirm({
    title: 'Deactivate this employee?',
    message: 'They can no longer use the Mini App. Their attendance and leave history stays in reports.',
    details: [{ label: 'Employee', value: `${e.full_name} (${e.employee_code})` }, { label: 'Branch', value: e.branch?.name ?? '—' }],
    confirmLabel: 'Deactivate',
  })
  if (ok && await run('deactivate', () => employeeService.deactivate(id), 'Employee deactivated')) {
    lookups.invalidateStaff()
    emp.load()
  }
}
</script>

<template>
  <div>
    <NuxtLink to="/employees" class="link text-sm">← Employees</NuxtLink>
    <StateView :loading="emp.loading.value && !emp.data.value" :error="emp.error.value" @retry="emp.load">
      <template v-if="emp.data.value">
        <PageHeader
          class="mt-3"
          :title="emp.data.value.full_name"
          :subtitle="[emp.data.value.employee_code, emp.data.value.role?.name ?? '', emp.data.value.branch?.name].filter(Boolean).join(' · ')"
        >
          <StatusBadge v-bind="emp.data.value.deleted_at ? { label: 'Deactivated', tone: 'neutral' } : employeeStatus[emp.data.value.status]" />
          <AppButton variant="secondary" @click="navigateTo({ path: '/attendance', query: { employee_id: String(id) } })">Attendance</AppButton>
        </PageHeader>

        <p v-if="emp.data.value.deleted_at" class="card mb-4 border-warning/40 bg-warning/5 px-4 py-3 text-sm">
          Deactivated on {{ formatDate(emp.data.value.deleted_at) }}. Their history is kept; they can no longer use the Mini App.
        </p>

        <EmployeeForm v-if="canEdit" :key="formKey" :employee="emp.data.value" :saving="busy === 'save'" @submit="save" />
        <div v-else class="card grid gap-4 p-5 text-sm sm:grid-cols-3">
          <div><p class="label">Branch</p>{{ emp.data.value.branch?.name || 'All branches' }}</div>
          <div><p class="label">Department</p>{{ emp.data.value.department?.name || '—' }}</div>
          <div><p class="label">Position</p>{{ emp.data.value.position || '—' }}</div>
          <div><p class="label">Telegram</p>{{ emp.data.value.telegram_account?.telegram_user_id ?? 'Not linked' }}</div>
          <div><p class="label">Email</p>{{ emp.data.value.email || '—' }}</div>
          <div><p class="label">Phone</p>{{ emp.data.value.phone || '—' }}</div>
          <div><p class="label">Joined</p>{{ formatDate(emp.data.value.join_date) }}</div>
        </div>

        <div v-if="!emp.data.value.deleted_at && auth.can('staff.manage')" class="mt-6 grid gap-4 sm:grid-cols-2">
          <section class="card p-5">
            <h2 class="text-sm font-semibold">Deactivate</h2>
            <p class="mt-1 text-sm text-muted">Blocks the Mini App. Attendance history is kept.</p>
            <AppButton class="mt-3" variant="danger" :loading="busy === 'deactivate'" @click="deactivate">Deactivate employee</AppButton>
          </section>
          <section v-if="auth.can('users.manage')" class="card p-5">
            <h2 class="text-sm font-semibold">Admin panel</h2>
            <p class="mt-1 text-sm text-muted">Employees use the Mini App. If this person also runs the panel, give them a separate system user.</p>
            <AppButton class="mt-3" variant="secondary" @click="navigateTo('/access/users')">System users</AppButton>
          </section>
        </div>
      </template>
    </StateView>

  </div>
</template>
