<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { SystemUser } from '~/types/api'
import { formatDateTime } from '~/utils/format'
import { isAdminRole, type SelectOption } from '~/utils/options'

definePageMeta({ permission: 'users.manage' })

const auth = useAuthStore()
const lookups = useLookupsStore()
const users = useResource(() => accessService.users())
onMounted(() => {
  lookups.loadRoles().catch(() => {})
  lookups.loadEmployees().catch(() => {})
  lookups.loadBranches().catch(() => {})
})
const { busy, run } = useAction()

const branchName = (id: number) => lookups.branches?.find(b => b.id === id)?.name ?? `#${id}`
function access(u: SystemUser) {
  switch (u.scope) {
    case 'all': return 'All branches'
    case 'branches': return u.branch_ids.map(branchName).join(', ')
    case 'team': return 'Their team only'
    default: return 'No panel access'
  }
}
// Roles that can use the panel; admin roles only for those allowed to grant them.
const panelRoles = computed(() =>
  (lookups.roles ?? []).filter(r => r.code !== 'branch_admin' && r.permissions.some(p => p !== 'attendance.self' && p !== 'leave.self')
    && (auth.can('roles.assign_admin') || !isAdminRole(r))))
const roleOptions = computed((): SelectOption[] => panelRoles.value.map(r => ({ value: r.code, label: r.name, sub: r.is_system ? undefined : 'custom' })))

// ---------- new login ----------
const createOpen = ref(false)
const create = reactive({ employee_id: '', role: '', email: '', password: '' })
const candidates = computed((): SelectOption[] => (lookups.employees ?? [])
  .filter(e => !e.deleted_at && e.status === 'ACTIVE' && !e.has_login)
  .map(e => ({ value: String(e.id), label: e.full_name, sub: e.employee_code })))
watch(() => create.employee_id, id => {
  const e = lookups.employees?.find(x => String(x.id) === id)
  if (!e) return
  create.email = e.email || create.email
  create.role = panelRoles.value.some(r => r.code === e.role?.code) ? e.role!.code : ''
})
function openCreate() {
  Object.assign(create, { employee_id: '', role: '', email: '', password: '' })
  createOpen.value = true
}
function generate(): string {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const bytes = crypto.getRandomValues(new Uint8Array(14))
  return Array.from(bytes, b => chars[b % chars.length]).join('')
}
async function saveCreate() {
  const ok = await run('create', () => accessService.createUser({ employee_id: Number(create.employee_id), role: create.role || undefined,
    email: create.email.trim(), password: create.password }), 'Login created')
  if (ok) {
    createOpen.value = false
    users.load()
    lookups.loadEmployees(true).catch(() => {})
  }
}

// ---------- edit ----------
const editing = ref<SystemUser | null>(null)
const edit = reactive({ email: '', role: '', active: true })
const editOpen = computed({ get: () => !!editing.value, set: v => !v && (editing.value = null) })
function openEdit(u: SystemUser) {
  editing.value = u
  Object.assign(edit, { email: u.email, role: u.role, active: u.status === 'ACTIVE' })
}
const editRoleOptions = computed((): SelectOption[] => {
  const u = editing.value
  const opts = [...roleOptions.value]
  if (u && !opts.some(o => o.value === u.role)) opts.unshift({ value: u.role, label: u.role_name })
  return opts
})
async function saveEdit() {
  const u = editing.value!
  const ok = await run('edit', () => accessService.updateUser(u.id, { email: edit.email.trim(), role: edit.role !== u.role ? edit.role : undefined,
    status: edit.active ? 'ACTIVE' : 'INACTIVE' }), 'System user saved')
  if (ok) {
    editing.value = null
    users.load()
  }
}

// ---------- password ----------
const pwFor = ref<SystemUser | null>(null)
const pw = ref('')
const pwOpen = computed({ get: () => !!pwFor.value, set: v => !v && (pwFor.value = null) })
async function savePassword() {
  if (await run('pw', () => accessService.resetPassword(pwFor.value!.id, pw.value), 'Password changed')) pwFor.value = null
}

async function remove(u: SystemUser) {
  if (!window.confirm(`Delete the login of ${u.full_name} (${u.email})? The employee record stays.`)) return
  if (await run(`d${u.id}`, () => accessService.deleteUser(u.id), 'Login deleted')) {
    users.load()
    lookups.loadEmployees(true).catch(() => {})
  }
}
</script>

<template>
  <div>
    <PageHeader title="System users" subtitle="Admin panel accounts: who can sign in, their role, branch access and sign-in details.">
      <AppButton @click="openCreate">New login</AppButton>
    </PageHeader>

    <StateView :loading="users.loading.value && !users.data.value" :error="users.error.value" :empty="users.data.value?.length === 0" empty-text="No panel logins yet." @retry="users.load">
      <div class="card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>User</th><th>Role</th><th>Branch access</th><th>Status</th><th>Last sign-in</th><th class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="u in users.data.value ?? []" :key="u.id">
                <td>
                  <span class="font-medium">{{ u.full_name }}</span><span v-if="u.self" class="ml-1.5 text-xs text-muted">(you)</span>
                  <span class="block text-xs text-muted">{{ u.email }} · {{ u.employee_code }}</span>
                </td>
                <td>{{ u.role_name }}</td>
                <td class="max-w-56 text-sm">
                  <NuxtLink to="/access/branches" class="hover:underline" :class="u.scope === 'none' ? 'text-danger' : u.scope === 'all' ? 'text-muted' : ''">{{ access(u) }}</NuxtLink>
                </td>
                <td><StatusBadge :tone="u.status === 'ACTIVE' ? 'success' : 'neutral'" :label="u.status === 'ACTIVE' ? 'Active' : 'Disabled'" /></td>
                <td class="whitespace-nowrap text-sm text-muted">{{ u.last_login_at ? formatDateTime(u.last_login_at, auth.company.timezone) : 'Never' }}</td>
                <td class="whitespace-nowrap text-right">
                  <AppButton size="sm" variant="ghost" :aria-label="`Edit ${u.full_name}`" @click="openEdit(u)">Edit</AppButton>
                  <AppButton size="sm" variant="ghost" :aria-label="`Reset password of ${u.full_name}`" @click="pwFor = u; pw = ''">Password</AppButton>
                  <AppButton v-if="!u.self" size="sm" variant="ghost" :loading="busy === `d${u.id}`" :aria-label="`Delete login of ${u.full_name}`" @click="remove(u)">Delete</AppButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <p class="mt-3 text-xs text-muted">
        A role decides what someone may do (<NuxtLink to="/access/roles" class="link">Role management</NuxtLink>);
        branch access decides where (<NuxtLink to="/access/branches" class="link">Branch assignment</NuxtLink>).
      </p>
    </StateView>

    <AppModal v-model="createOpen" title="New login">
      <form id="u-create" class="grid gap-3" @submit.prevent="saveCreate">
        <FormField label="Employee" for="u-emp" hint="Employees without a panel login.">
          <SearchSelect id="u-emp" v-model="create.employee_id" :options="candidates" placeholder="Choose an employee…" required />
        </FormField>
        <FormField label="Role" for="u-role" hint="Staff need a role with admin panel access.">
          <SearchSelect id="u-role" v-model="create.role" :options="roleOptions" placeholder="Choose a role…" required />
        </FormField>
        <FormField label="Email (sign-in)" for="u-email"><input id="u-email" v-model="create.email" type="email" class="input" required maxlength="150" /></FormField>
        <FormField label="Password" for="u-pw" hint="At least 10 characters. Share it privately.">
          <div class="flex gap-2">
            <input id="u-pw" v-model="create.password" type="text" class="input font-mono" required minlength="10" autocomplete="new-password" />
            <AppButton variant="secondary" @click="create.password = generate()">Generate</AppButton>
          </div>
        </FormField>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="createOpen = false">Cancel</AppButton>
        <AppButton type="submit" form="u-create" :loading="busy === 'create'">Create login</AppButton>
      </template>
    </AppModal>

    <AppModal v-model="editOpen" :title="editing ? `Edit ${editing.full_name}` : 'Edit'">
      <form v-if="editing" id="u-edit" class="grid gap-3" @submit.prevent="saveEdit">
        <FormField label="Email (sign-in)" for="ue-email"><input id="ue-email" v-model="edit.email" type="email" class="input" required /></FormField>
        <FormField label="Role" for="ue-role" :hint="editing.self ? 'You cannot change your own role.' : editing.role === 'branch_admin' ? 'Branch admins change on the Branches page.' : undefined">
          <SearchSelect id="ue-role" v-model="edit.role" :options="editRoleOptions" :disabled="editing.self || editing.role === 'branch_admin'" />
        </FormField>
        <label class="flex items-center gap-2 text-sm" :class="editing.self && 'opacity-50'">
          <input v-model="edit.active" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="editing.self" />
          Can sign in
        </label>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="editing = null">Cancel</AppButton>
        <AppButton type="submit" form="u-edit" :loading="busy === 'edit'">Save</AppButton>
      </template>
    </AppModal>

    <AppModal v-model="pwOpen" :title="pwFor ? `New password for ${pwFor.full_name}` : 'New password'">
      <form id="u-pw-form" @submit.prevent="savePassword">
        <FormField label="New password" for="u-newpw" hint="At least 10 characters. Their other sessions stay signed in until they expire.">
          <div class="flex gap-2">
            <input id="u-newpw" v-model="pw" type="text" class="input font-mono" required minlength="10" autocomplete="new-password" />
            <AppButton variant="secondary" @click="pw = generate()">Generate</AppButton>
          </div>
        </FormField>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="pwFor = null">Cancel</AppButton>
        <AppButton type="submit" form="u-pw-form" :loading="busy === 'pw'">Change password</AppButton>
      </template>
    </AppModal>
  </div>
</template>
