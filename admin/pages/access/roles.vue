<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { Permission, PermissionInfo, Role } from '~/types/api'
import type { SelectOption } from '~/utils/options'

// The owner manages every role. A branch's admins (users.branch: Company Admin, Branch
// Admin) manage only their branches' own roles, with permissions they have themselves.
definePageMeta({ permission: ['users.manage', 'users.branch'] })

const auth = useAuthStore()
const scoped = computed(() => !auth.can('users.manage'))
const lookups = useLookupsStore()
onMounted(() => lookups.loadBranches().catch(() => {}))
const roles = useResource(() => accessService.roles()) // all: also the "Start from" choices
// A branch's admins see their branches' roles only; the owner sees every role.
const shown = computed(() => (roles.data.value ?? []).filter(r => !scoped.value || r.branch_id != null))
const pager = usePager(shown)
const catalog = useResource(() => accessService.permissions())
const { busy, run } = useAction()
const { confirm } = useConfirm()

const groups = computed(() => {
  const out: { name: string; items: PermissionInfo[] }[] = []
  for (const p of catalog.data.value ?? []) {
    const g = out.find(x => x.name === p.group) ?? (out.push({ name: p.group, items: [] }), out[out.length - 1]!)
    g.items.push(p)
  }
  return out
})
const label = (code: Permission) => catalog.data.value?.find(p => p.code === code)?.label ?? code

// ---------- editor ----------
const form = reactive({ id: 0, name: '', description: '', permissions: [] as Permission[], locked: false, system: false, readonly: false, branch_id: '' })
const onlyBranch = computed(() => (lookups.branches?.length === 1 ? String(lookups.branches[0]!.id) : ''))
const open = ref(false)
const template = ref('')
const templateOptions = computed((): SelectOption[] => shown.value.map(r => ({ value: String(r.id), label: r.name, sub: r.branch_name || undefined })))
watch(template, id => {
  const r = roles.data.value?.find(x => String(x.id) === id)
  // Only what may be given here (a branch's admins: their own permissions).
  if (r) form.permissions = r.permissions.filter(p => catalog.data.value?.some(c => c.code === p))
})
function edit(r?: Role) {
  template.value = ''
  Object.assign(form, r
    ? { id: r.id, name: r.name, description: r.description, permissions: [...r.permissions], locked: r.locked, system: r.is_system,
        readonly: r.locked || !r.editable, branch_id: r.branch_id ? String(r.branch_id) : '' }
    : { id: 0, name: '', description: '', permissions: ['attendance.self', 'leave.self'], locked: false, system: false, readonly: false,
        branch_id: scoped.value ? onlyBranch.value : '' })
  open.value = true
}
function toggle(p: Permission) {
  form.permissions = form.permissions.includes(p) ? form.permissions.filter(x => x !== p) : [...form.permissions, p]
}
function toggleGroup(items: PermissionInfo[]) {
  const all = items.every(i => form.permissions.includes(i.code))
  const codes = items.map(i => i.code)
  form.permissions = all ? form.permissions.filter(p => !codes.includes(p)) : [...new Set([...form.permissions, ...codes])]
}
async function save() {
  const input = { name: form.name.trim(), description: form.description.trim(), ...(form.locked ? {} : { permissions: form.permissions }),
    ...(form.id ? {} : { branch_id: Number(form.branch_id) || 0 }) }
  const ok = await run('save', () => (form.id ? accessService.updateRole(form.id, input) : accessService.createRole(input)), form.id ? 'Role saved' : 'Role created')
  if (ok) {
    open.value = false
    roles.load()
    lookups.loadRoles(true).catch(() => {})
  }
}
async function remove(r: Role) {
  if (!(await confirm({ title: `Delete the role “${r.name}”?`, message: 'Nobody has this role; it is removed from Role management.' }))) return
  if (await run(`d${r.id}`, () => accessService.deleteRole(r.id), 'Role deleted')) {
    roles.load()
    lookups.loadRoles(true).catch(() => {})
  }
}
</script>

<template>
  <div>
    <PageHeader
      title="Role management"
      :subtitle="scoped ? 'Your branches\' own roles: what the people you add can do. You can only give permissions you have yourself.'
        : 'What each role may do. A company role is for everyone; a branch role only for that branch. Assign roles on System users or in the employee form.'"
    >
      <AppButton @click="edit()">New role</AppButton>
    </PageHeader>

    <StateView
      :loading="roles.loading.value && !roles.data.value" :error="roles.error.value" :empty="!shown.length"
      empty-text="No roles of your branches yet. Add one for the people you manage." @retry="roles.load"
    >
      <div class="grid gap-3 md:grid-cols-2">
        <article v-for="r in pager.rows.value" :key="r.id" class="card flex flex-col p-4">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <h2 class="font-semibold">{{ r.name }}</h2>
              <p class="text-xs text-muted">{{ r.code }}<template v-if="r.description"> · {{ r.description }}</template></p>
            </div>
            <span class="flex shrink-0 flex-col items-end gap-1">
              <StatusBadge :tone="r.locked ? 'warning' : r.is_system ? 'neutral' : 'info'" :label="r.locked ? 'Owner' : r.is_system ? 'Built-in' : 'Custom'" />
              <StatusBadge :tone="r.branch_id ? 'success' : 'neutral'" :label="r.branch_name || 'Company'" />
            </span>
          </div>
          <ul class="mt-3 flex flex-wrap gap-1">
            <li v-if="r.locked" class="rounded-md bg-surface-2 px-2 py-0.5 text-xs">Every permission</li>
            <template v-else>
              <li v-for="p in r.permissions" :key="p" class="rounded-md bg-surface-2 px-2 py-0.5 text-xs">{{ label(p) }}</li>
              <li v-if="!r.permissions.length" class="text-xs text-muted">No permissions</li>
            </template>
          </ul>
          <div class="mt-auto flex items-center justify-between gap-2 pt-3 text-xs text-muted">
            <span>{{ r.employees }} {{ r.employees === 1 ? 'person' : 'people' }} · {{ r.users }} can sign in</span>
            <span class="flex gap-1.5">
              <IconAction :kind="r.locked || !r.editable ? 'view' : 'edit'" :label="`${r.locked || !r.editable ? 'View' : 'Edit'} ${r.name}`" @click="edit(r)" />
              <IconAction
                v-if="!r.is_system && r.editable" kind="delete" :disabled="r.employees > 0"
                :label="r.employees > 0 ? `Give the people of ${r.name} another role first` : `Delete ${r.name}`" :loading="busy === `d${r.id}`" @click="remove(r)"
              />
            </span>
          </div>
        </article>
      </div>
      <div class="card mt-3 overflow-hidden">
        <AppPagination :page="pager.page.value" :page-size="pager.pageSize.value" :total="pager.total.value" noun="role" @go="pager.go" @size="pager.resize" />
      </div>
    </StateView>

    <AppModal v-model="open" :title="form.id ? (form.readonly ? form.name : `Edit ${form.name}`) : 'New role'" wide>
      <form id="role-form" class="grid gap-4" @submit.prevent="save">
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField label="Name" for="r-name"><input id="r-name" v-model="form.name" class="input" required maxlength="64" placeholder="e.g. Shift Supervisor" :disabled="form.readonly" /></FormField>
          <FormField v-if="!form.id" label="Start from" for="r-tpl" hint="Copies that role's permissions.">
            <SearchSelect id="r-tpl" v-model="template" :options="templateOptions" placeholder="Blank" />
          </FormField>
          <FormField label="Description" for="r-desc" class="sm:col-span-2"><input id="r-desc" v-model="form.description" class="input" maxlength="255" :disabled="form.readonly" /></FormField>
          <FormField
            label="For" for="r-branch" class="sm:col-span-2"
            :hint="form.id ? 'A role stays where it was made.' : scoped ? 'Only people of this branch can be given it.' : 'Company role: anyone can be given it. Branch role: only that branch\'s people.'"
          >
            <SearchSelect
              id="r-branch" v-model="form.branch_id" :options="lookups.branchOptions" :disabled="!!form.id"
              :required="scoped" :placeholder="scoped ? 'Choose a branch…' : 'Company role (every branch)'" :empty-label="scoped ? undefined : 'Company role (every branch)'"
            />
          </FormField>
        </div>
        <p v-if="form.locked" class="rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning">The owner role always has every permission, so nobody can be locked out.</p>
        <p v-else-if="form.readonly" class="rounded-lg bg-surface-2 px-3 py-2 text-sm text-muted">You can see this role, not change it.</p>
        <fieldset v-for="g in groups" :key="g.name" class="rounded-lg border border-border p-3" :disabled="form.readonly">
          <legend class="flex items-center gap-2 px-1 text-sm font-semibold">
            {{ g.name }}
            <button v-if="!form.readonly" type="button" class="text-xs font-normal text-primary hover:underline" @click="toggleGroup(g.items)">
              {{ g.items.every(i => form.permissions.includes(i.code)) ? 'none' : 'all' }}
            </button>
          </legend>
          <div class="grid gap-2 sm:grid-cols-2">
            <label v-for="p in g.items" :key="p.code" class="flex cursor-pointer items-start gap-2 rounded-md p-1.5 text-sm hover:bg-surface-2">
              <input type="checkbox" class="mt-0.5 size-4 shrink-0 accent-[var(--primary)]" :checked="form.locked || form.permissions.includes(p.code)" @change="toggle(p.code)" />
              <span><span class="font-medium">{{ p.label }}</span><span class="block text-xs text-muted">{{ p.description }}</span></span>
            </label>
          </div>
        </fieldset>
      </form>
      <template #footer>
        <AppButton variant="secondary" @click="open = false">{{ form.readonly ? 'Close' : 'Cancel' }}</AppButton>
        <AppButton v-if="!form.readonly" type="submit" form="role-form" :loading="busy === 'save'" :disabled="scoped && !form.branch_id">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
