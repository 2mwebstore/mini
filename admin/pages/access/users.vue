<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Role, RoleCode, SystemUser, SystemUserInput } from '~/types/api'
import { formatDateTime } from '~/utils/format'
import { isAdminRole, type SelectOption } from '~/utils/options'

// The owner manages every admin account; a Company Admin (users.branch) the accounts
// of their own branches — the API decides which roles and branches they may give.
definePageMeta({ permission: ['users.manage', 'users.branch'] })

const auth = useAuthStore()
const lookups = useLookupsStore()
const scoped = computed(() => !auth.can('users.manage'))
const users = usePaged(p => accessService.usersPage(p))
onMounted(() => {
  lookups.loadRoles().catch(() => {})
  lookups.loadBranches().catch(() => {})
})
const { busy, run } = useAction()
const { confirm } = useConfirm()

const branchName = (id: number) => lookups.branches?.find(b => b.id === id)?.name ?? `#${id}`
function access(u: SystemUser) {
  switch (u.scope) {
    case 'all': return 'All branches'
    case 'branches': return u.branch_ids.map(branchName).join(', ')
    case 'team': return 'Their team only'
    default: return 'No panel access'
  }
}

// System users get admin roles; managers and staff are employees (they use the Mini App).
// Only the roles the signed-in user may give are offered.
const adminRoles = computed(() => (lookups.roles ?? []).filter(r => isAdminRole(r) && r.assignable))
const roleOptions = computed((): SelectOption[] => adminRoles.value.map(r => ({ value: r.code, label: r.name, sub: r.branch_name || (r.is_system ? undefined : 'custom') })))
const roleOf = (code: string): Role | undefined => lookups.roles?.find(r => r.code === code)
// Company-wide roles see every branch; the others work in a home branch.
const companyWide = (code: string) => code === 'super_admin' || !!roleOf(code)?.permissions.includes('all.view')
const branchOptions = computed((): SelectOption[] => (lookups.branches ?? []).filter(b => b.status === 'ACTIVE').map(b => ({
  value: String(b.id), label: b.name, sub: b.admin ? `admin: ${b.admin.full_name}` : undefined,
})))

// ---------- create / edit ----------
const editing = ref<SystemUser | null>(null) // null + formOpen = new
const formOpen = ref(false)
const form = reactive({ full_name: '', email: '', phone: '', role: '' as string, branch_id: '', telegram: '', password: '', active: true })
function openForm(u?: SystemUser) {
  editing.value = u ?? null
  Object.assign(form, u
    ? { full_name: u.full_name, email: u.email, phone: u.phone, role: u.role, branch_id: u.branch_id ? String(u.branch_id) : '',
        telegram: u.telegram_user_id ? String(u.telegram_user_id) : '', password: '', active: u.status === 'ACTIVE' }
    : { full_name: '', email: '', phone: '', role: '', branch_id: '', telegram: '', password: '', active: true })
  formOpen.value = true
}
const editRoleOptions = computed((): SelectOption[] => {
  const u = editing.value
  const opts = [...roleOptions.value]
  if (u && !opts.some(o => o.value === u.role)) opts.unshift({ value: u.role, label: u.role_name, sub: 'no panel access' })
  return opts
})
const needsBranch = computed(() => !!form.role && !companyWide(form.role))
// A branch's own role goes with that branch.
const roleBranch = computed(() => roleOf(form.role)?.branch_id ?? null)
watch(roleBranch, b => b && (form.branch_id = String(b)))
// Accounts made by a Company Admin always work in one of their branches.
const branchRequired = computed(() => form.role === 'branch_admin' || scoped.value)
function generate(): string {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const bytes = crypto.getRandomValues(new Uint8Array(14))
  return Array.from(bytes, b => chars[b % chars.length]).join('')
}
async function save() {
  const u = editing.value
  const tg = form.telegram.trim()
  const input: SystemUserInput = {
    full_name: form.full_name.trim(), email: form.email.trim(), phone: form.phone.trim(),
    branch_id: needsBranch.value ? Number(form.branch_id) || 0 : 0,
    telegram_user_id: tg ? Number(tg) : u?.telegram_user_id ? 0 : undefined,
  }
  if (!u || form.role !== u.role) input.role = form.role as RoleCode
  if (u && !u.self) input.status = form.active ? 'ACTIVE' : 'INACTIVE'
  if (!u) input.password = form.password
  const ok = await run('save', () => (u ? accessService.updateUser(u.id, input) : accessService.createUser(input)),
    u ? 'System user saved' : 'System user created')
  if (ok) {
    formOpen.value = false
    users.refresh()
    lookups.loadBranches(true).catch(() => {}) // branch admins change with roles
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
  const ok = await confirm({
    title: 'Delete this system user?',
    message: 'They can no longer sign in to the admin panel. Their past actions stay in the audit log. Employees are not affected.',
    details: [{ label: 'Name', value: u.full_name }, { label: 'Email', value: u.email }, { label: 'Role', value: u.role_name }],
  })
  if (!ok) return
  if (await run(`d${u.id}`, () => accessService.deleteUser(u.id), 'System user deleted')) {
    users.refresh()
    lookups.loadBranches(true).catch(() => {})
  }
}
</script>

<template>
  <div>
    <PageHeader
      title="System users"
      :subtitle="scoped ? 'Admin panel accounts for your branches — branch admins and other company admins. Separate from employees (the staff who check in).'
        : 'Admin panel accounts — separate from employees (the staff who check in). Each has a role, branch access and a sign-in email.'"
    >
      <AppButton @click="openForm()">New system user</AppButton>
    </PageHeader>

    <StateView :loading="users.loading.value && !users.data.value" :error="users.error.value" :empty="users.total.value === 0" empty-text="No system users yet." @retry="users.load">
      <div class="card overflow-hidden" :class="{ 'opacity-60': users.loading.value }">
        <div class="overflow-x-auto">
          <table class="table">
            <thead><tr><th>User</th><th>Role</th><th>Branch access</th><th>Telegram</th><th>Status</th><th>Last sign-in</th><th class="text-right">Actions</th></tr></thead>
            <tbody>
              <tr v-for="u in users.rows.value" :key="u.id">
                <td>
                  <span class="font-medium">{{ u.full_name }}</span><span v-if="u.self" class="ml-1.5 text-xs text-muted">(you)</span>
                  <span class="block text-xs text-muted">{{ u.email }}<template v-if="u.phone"> · {{ u.phone }}</template></span>
                </td>
                <td>
                  {{ u.role_name }}
                  <span v-if="u.branch_name" class="block text-xs text-muted">{{ u.branch_name }}</span>
                </td>
                <td class="max-w-56 text-sm">
                  <NuxtLink to="/access/branches" class="hover:underline" :class="u.scope === 'none' ? 'text-danger' : u.scope === 'all' ? 'text-muted' : ''">{{ access(u) }}</NuxtLink>
                </td>
                <td class="text-sm" :class="u.telegram_user_id ? '' : 'text-muted'">{{ u.telegram_user_id ? 'Linked' : '—' }}</td>
                <td><StatusBadge :tone="u.status === 'ACTIVE' ? 'success' : 'neutral'" :label="u.status === 'ACTIVE' ? 'Active' : 'Disabled'" /></td>
                <td class="whitespace-nowrap text-sm text-muted">{{ u.last_login_at ? formatDateTime(u.last_login_at, auth.company.timezone) : 'Never' }}</td>
                <td class="whitespace-nowrap text-right">
                  <div class="inline-flex gap-1.5">
                    <IconAction kind="edit" :label="`Edit ${u.full_name}`" @click="openForm(u)" />
                    <IconAction kind="password" :label="`Reset password of ${u.full_name}`" @click="pwFor = u; pw = ''" />
                    <IconAction v-if="!u.self" kind="delete" :label="`Delete ${u.full_name}`" :loading="busy === `d${u.id}`" @click="remove(u)" />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <AppPagination :page="users.page.value" :page-size="users.pageSize.value" :total="users.total.value" noun="system user" @go="users.go" @size="users.resize" />
      </div>
      <p class="mt-3 text-xs text-muted">
        A role decides what someone may do (<NuxtLink v-if="!scoped" to="/access/roles" class="link">Role management</NuxtLink><template v-else>you can give roles with no more powers than your own</template>);
        branch access decides where (<NuxtLink to="/access/branches" class="link">Branch assignment</NuxtLink>).
        Someone who also works as staff has a separate record on <NuxtLink to="/employees" class="link">Employees</NuxtLink>.
      </p>
    </StateView>

    <AppModal v-model="formOpen" :title="editing ? `Edit ${editing.full_name}` : 'New system user'" wide>
      <form id="u-form" class="grid gap-3 sm:grid-cols-2" @submit.prevent="save">
        <FormField label="Full name" for="u-name"><input id="u-name" v-model="form.full_name" class="input" required maxlength="150" /></FormField>
        <FormField label="Email (sign-in)" for="u-email"><input id="u-email" v-model="form.email" type="email" class="input" required maxlength="150" /></FormField>
        <FormField label="Role" for="u-role" :hint="editing?.self ? 'You cannot change your own role.' : 'Admin roles. Managers and staff are employees (Mini App).'">
          <SearchSelect id="u-role" v-model="form.role" :options="editing ? editRoleOptions : roleOptions" placeholder="Choose a role…" required :disabled="editing?.self" />
        </FormField>
        <FormField
          v-if="needsBranch" label="Branch" for="u-branch"
          :hint="form.role === 'branch_admin' ? 'Becomes this branch’s admin (one per branch).'
            : scoped ? 'The branch they work in. Add more of your branches on Branch assignment.'
            : 'Optional home branch. Add more branches on Branch assignment; branches they create are theirs too.'"
        >
          <SearchSelect
            id="u-branch" v-model="form.branch_id" :options="branchOptions" :required="branchRequired" :disabled="!!roleBranch"
            :placeholder="branchRequired ? 'Choose a branch…' : 'No home branch'" :empty-label="branchRequired ? undefined : 'No home branch'"
          />
        </FormField>
        <FormField v-else label="Branches" hint="Every branch — limit on Branch assignment."><p class="input bg-surface-2 text-muted">All branches</p></FormField>
        <FormField label="Phone" for="u-phone"><input id="u-phone" v-model="form.phone" class="input" maxlength="32" /></FormField>
        <FormField label="Telegram user ID (optional)" for="u-tg" hint="For review notifications and the Mini App's Team tab.">
          <input id="u-tg" v-model="form.telegram" class="input" inputmode="numeric" pattern="[0-9]*" placeholder="e.g. 123456789" />
        </FormField>
        <FormField v-if="!editing" label="Password" for="u-pw" class="sm:col-span-2" hint="At least 10 characters. Share it privately.">
          <div class="flex gap-2">
            <input id="u-pw" v-model="form.password" type="text" class="input font-mono" required minlength="10" autocomplete="new-password" />
            <AppButton variant="secondary" @click="form.password = generate()">Generate</AppButton>
          </div>
        </FormField>
        <label v-else class="flex items-center gap-2 text-sm sm:col-span-2" :class="editing.self && 'opacity-50'">
          <input v-model="form.active" type="checkbox" class="size-4 accent-[var(--primary)]" :disabled="editing.self" />
          Can sign in
        </label>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="formOpen = false">Cancel</AppButton>
        <AppButton type="submit" form="u-form" :loading="busy === 'save'">{{ editing ? 'Save' : 'Create system user' }}</AppButton>
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
