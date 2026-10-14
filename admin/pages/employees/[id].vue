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

// ---------- admin panel login (super admin) ----------
const login = reactive({ open: false, email: '', password: '' })
const loginOpen = computed({ get: () => login.open, set: v => (login.open = v) })
function openLogin() {
  Object.assign(login, { open: true, email: emp.data.value?.email ?? '', password: '' })
}
async function createLogin() {
  if (await run('login', () => employeeService.createLogin(id, login.email.trim(), login.password), 'Admin panel login created')) login.open = false
}

// ---------- deactivate ----------
const deactivateOpen = ref(false)
async function deactivate() {
  if (await run('deactivate', () => employeeService.deactivate(id), 'Employee deactivated')) {
    deactivateOpen.value = false
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
          Deactivated on {{ formatDate(emp.data.value.deleted_at) }}. Their history is kept; they can no longer use the Mini App or sign in.
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

        <div v-if="!emp.data.value.deleted_at && (auth.can('roles.assign_admin') || (auth.can('staff.manage') && emp.data.value.id !== auth.employee?.id))" class="mt-6 grid gap-4 sm:grid-cols-2">
          <section v-if="auth.can('roles.assign_admin')" class="card p-5">
            <h2 class="text-sm font-semibold">Admin panel login</h2>
            <p class="mt-1 text-sm text-muted">Lets a manager or HR admin sign in to this panel. Staff only use the Mini App.</p>
            <AppButton class="mt-3" variant="secondary" @click="openLogin">Create login</AppButton>
          </section>
          <section v-if="auth.can('staff.manage') && emp.data.value.id !== auth.employee?.id" class="card p-5">
            <h2 class="text-sm font-semibold">Deactivate</h2>
            <p class="mt-1 text-sm text-muted">Blocks the Mini App and panel login. Attendance history is kept.</p>
            <AppButton class="mt-3" variant="danger" @click="deactivateOpen = true">Deactivate employee</AppButton>
          </section>
        </div>
      </template>
    </StateView>

    <AppModal v-model="loginOpen" title="Create admin panel login">
      <form id="login-form" class="flex flex-col gap-3" @submit.prevent="createLogin">
        <FormField label="Email" for="l-email"><input id="l-email" v-model="login.email" class="input" type="email" required autocomplete="off" /></FormField>
        <FormField label="Temporary password" for="l-pass" hint="At least 10 characters. Share it securely.">
          <input id="l-pass" v-model="login.password" class="input" type="password" minlength="10" required autocomplete="new-password" />
        </FormField>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="login.open = false">Cancel</AppButton>
        <AppButton type="submit" form="login-form" :loading="busy === 'login'">Create login</AppButton>
      </template>
    </AppModal>

    <AppModal v-model="deactivateOpen" title="Deactivate employee?">
      <p class="text-sm">
        <b>{{ emp.data.value?.full_name }}</b> will no longer be able to use the Mini App or sign in. Their attendance history stays in reports.
      </p>
      <template #footer>
        <AppButton variant="secondary" @click="deactivateOpen = false">Cancel</AppButton>
        <AppButton variant="danger" :loading="busy === 'deactivate'" @click="deactivate">Deactivate</AppButton>
      </template>
    </AppModal>
  </div>
</template>
