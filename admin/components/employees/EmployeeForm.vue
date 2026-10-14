<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Employee, EmployeeInput, EmployeeStatus, RoleCode } from '~/types/api'
import { employeeStatus, isoDay, shiftHours } from '~/utils/format'
import { isAdminRole, optionsFrom, type SelectOption } from '~/utils/options'

// Create/edit form for an employee. Emits only the fields the API should apply.
const props = defineProps<{ employee?: Employee | null; saving?: boolean }>()
const emit = defineEmits<{ submit: [input: EmployeeInput] }>()

const auth = useAuthStore()
const lookups = useLookupsStore()
onMounted(() => {
  lookups.loadEmployees().catch(() => {})
  lookups.loadDepartments().catch(() => {})
  lookups.loadSchedules().catch(() => {})
  lookups.loadBranches().catch(() => {})
  lookups.loadPositions().catch(() => {})
  lookups.loadRoles().catch(() => {})
})

const e = props.employee
const form = reactive({
  employee_code: e?.employee_code ?? '',
  full_name: e?.full_name ?? '',
  telegram_user_id: e?.telegram_account ? String(e.telegram_account.telegram_user_id) : '',
  department_id: e?.department_id ? String(e.department_id) : '',
  branch_id: e?.branch_id ? String(e.branch_id) : '',
  position_id: e?.position_id ? String(e.position_id) : '',
  phone: e?.phone ?? '',
  email: e?.email ?? '',
  join_date: isoDay(e?.join_date),
  work_schedule_id: e?.work_schedule_id ? String(e.work_schedule_id) : '',
  manager_id: e?.manager_id ? String(e.manager_id) : '',
  role: (e?.role?.code ?? 'staff') as RoleCode,
  status: (e?.status ?? 'ACTIVE') as EmployeeStatus,
})

// Employees are staff: Staff, Manager (reviews their team in the Mini App) or a custom
// role of that kind. Admin roles belong to system users (the API enforces this).
const roles = computed(() => (lookups.roles ?? []).filter(r => r.code === form.role || !isAdminRole(r)))
// Roles that see every branch belong to no branch; everyone else belongs to one.
const companyWide = computed(() => !!lookups.roles?.find(r => r.code === form.role)?.permissions.includes('all.view'))
const branchFixed = computed(() => !auth.can('all.view')) // a branch admin adds staff to their own branch
const activeBranches = computed(() => (lookups.branches ?? []).filter(b => b.status === 'ACTIVE' || String(b.id) === form.branch_id))
const managers = computed(() => (lookups.employees ?? []).filter(m => !m.deleted_at && m.id !== e?.id))
const branchOptions = computed((): SelectOption[] => activeBranches.value.map(b => ({ value: String(b.id), label: b.name })))
const scheduleOptions = computed((): SelectOption[] =>
  (lookups.schedules ?? []).map(s => ({ value: String(s.id), label: s.name, sub: shiftHours(s.start_time, s.end_time, s.work_type === 'CROSS_DAY') })),
)
const managerOptions = computed((): SelectOption[] => managers.value.map(m => ({ value: String(m.id), label: m.full_name, sub: m.employee_code })))
const roleOptions = computed((): SelectOption[] => roles.value.map(r => ({ value: r.code, label: r.name, sub: r.is_system ? undefined : 'custom' })))
const statusOptions = optionsFrom(employeeStatus)
const tgValid = computed(() => form.telegram_user_id === '' || /^\d{1,15}$/.test(form.telegram_user_id))

function submit() {
  if (!tgValid.value) return
  const input: EmployeeInput = {
    full_name: form.full_name.trim(),
    position_id: Number(form.position_id) || 0,
    department_id: Number(form.department_id) || 0,
    phone: form.phone.trim(),
    email: form.email.trim(),
    manager_id: Number(form.manager_id) || 0,
    role: form.role,
  }
  if (!e) input.employee_code = form.employee_code.trim()
  else input.status = form.status
  if (!companyWide.value && !branchFixed.value && form.branch_id) input.branch_id = Number(form.branch_id)
  if (form.join_date) input.join_date = form.join_date
  if (form.work_schedule_id) input.work_schedule_id = Number(form.work_schedule_id)
  const tg = form.telegram_user_id.trim()
  if (tg && tg !== String(e?.telegram_account?.telegram_user_id ?? '')) input.telegram_user_id = Number(tg)
  emit('submit', input)
}
</script>

<template>
  <form class="card grid gap-4 p-5 sm:grid-cols-2" @submit.prevent="submit">
    <FormField label="Employee ID" for="emp-code" :hint="e ? 'Cannot be changed.' : 'e.g. GT001 — letters, digits, - or _'">
      <input id="emp-code" v-model="form.employee_code" class="input uppercase" required :disabled="!!e" pattern="[A-Za-z0-9_\-]{2,32}" />
    </FormField>
    <FormField label="Full name" for="emp-name">
      <input id="emp-name" v-model="form.full_name" class="input" required maxlength="150" />
    </FormField>

    <FormField
      label="Telegram user ID"
      for="emp-tg"
      class="sm:col-span-2"
      hint="Links the employee to the Mini App. They can see their ID by opening the Mini App before they are registered."
    >
      <input id="emp-tg" v-model="form.telegram_user_id" class="input font-mono" inputmode="numeric" :aria-invalid="!tgValid" placeholder="123456789" />
      <p v-if="!tgValid" class="mt-1 text-xs text-danger">Numbers only.</p>
    </FormField>

    <FormField v-if="!companyWide" label="Branch" for="emp-branch">
      <SearchSelect v-if="!branchFixed" id="emp-branch" v-model="form.branch_id" :options="branchOptions" placeholder="Choose a branch…" required />
      <input v-else id="emp-branch" class="input" disabled :value="e?.branch?.name ?? auth.employee?.branch?.name ?? ''" />
    </FormField>
    <FormField label="Department" for="emp-dept">
      <SearchSelect id="emp-dept" v-model="form.department_id" :options="lookups.departmentOptions" empty-label="No department" />
      <NuxtLink v-if="auth.can('all.view')" to="/employees/departments" class="mt-1 inline-block text-xs text-primary hover:underline">Manage departments</NuxtLink>
    </FormField>
    <FormField label="Position" for="emp-pos">
      <SearchSelect id="emp-pos" v-model="form.position_id" :options="lookups.positionOptions" empty-label="No position" />
      <NuxtLink v-if="auth.can('all.view')" to="/employees/positions" class="mt-1 inline-block text-xs text-primary hover:underline">Manage positions</NuxtLink>
    </FormField>
    <FormField label="Phone" for="emp-phone"><input id="emp-phone" v-model="form.phone" class="input" type="tel" maxlength="30" /></FormField>
    <FormField label="Email" for="emp-email"><input id="emp-email" v-model="form.email" class="input" type="email" maxlength="150" /></FormField>
    <FormField label="Join date" for="emp-join"><input id="emp-join" v-model="form.join_date" class="input" type="date" /></FormField>
    <FormField label="Usual shift" for="emp-sched" hint="Change single days on the weekly roster, or a date range with a special schedule.">
      <SearchSelect id="emp-sched" v-model="form.work_schedule_id" :options="scheduleOptions" :empty-label="e ? 'Unchanged' : 'Default shift'" />
    </FormField>
    <FormField label="Manager" for="emp-mgr" hint="The manager reviews this person's attendance and leave.">
      <SearchSelect id="emp-mgr" v-model="form.manager_id" :options="managerOptions" empty-label="No manager" />
    </FormField>
    <FormField label="Role" for="emp-role" hint="Admin panel accounts are system users (Administration → System users).">
      <SearchSelect id="emp-role" v-model="form.role" :options="roleOptions" />
    </FormField>
    <FormField v-if="e" label="Status" for="emp-status">
      <SearchSelect id="emp-status" v-model="form.status" :options="statusOptions" />
    </FormField>

    <div class="flex justify-end gap-2 sm:col-span-2">
      <slot name="actions" />
      <AppButton type="submit" :loading="saving">{{ e ? 'Save changes' : 'Create employee' }}</AppButton>
    </div>
  </form>
</template>
