<script setup lang="ts">
import { accessService } from '~/services/access.service'
import { useLookupsStore } from '~/stores/lookups'
import type { Permission, PermissionInfo, Role } from '~/types/api'
import type { SelectOption } from '~/utils/options'

definePageMeta({ permission: 'users.manage' })

const lookups = useLookupsStore()
const roles = useResource(() => accessService.roles())
const catalog = useResource(() => accessService.permissions())
const { busy, run } = useAction()

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
const form = reactive({ id: 0, name: '', description: '', permissions: [] as Permission[], locked: false, system: false })
const open = ref(false)
const template = ref('')
const templateOptions = computed((): SelectOption[] => (roles.data.value ?? []).map(r => ({ value: String(r.id), label: r.name })))
watch(template, id => {
  const r = roles.data.value?.find(x => String(x.id) === id)
  if (r) form.permissions = [...r.permissions]
})
function edit(r?: Role) {
  template.value = ''
  Object.assign(form, r
    ? { id: r.id, name: r.name, description: r.description, permissions: [...r.permissions], locked: r.locked, system: r.is_system }
    : { id: 0, name: '', description: '', permissions: ['attendance.self', 'leave.self'], locked: false, system: false })
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
  const input = { name: form.name.trim(), description: form.description.trim(), ...(form.locked ? {} : { permissions: form.permissions }) }
  const ok = await run('save', () => (form.id ? accessService.updateRole(form.id, input) : accessService.createRole(input)), form.id ? 'Role saved' : 'Role created')
  if (ok) {
    open.value = false
    roles.load()
    lookups.loadRoles(true).catch(() => {})
  }
}
async function remove(r: Role) {
  if (!window.confirm(`Delete the role "${r.name}"?`)) return
  if (await run(`d${r.id}`, () => accessService.deleteRole(r.id), 'Role deleted')) {
    roles.load()
    lookups.loadRoles(true).catch(() => {})
  }
}
</script>

<template>
  <div>
    <PageHeader title="Role management" subtitle="What each role may do. Assign roles to people on System users or in the employee form.">
      <AppButton @click="edit()">New role</AppButton>
    </PageHeader>

    <StateView :loading="roles.loading.value && !roles.data.value" :error="roles.error.value" @retry="roles.load">
      <div class="grid gap-3 md:grid-cols-2">
        <article v-for="r in roles.data.value ?? []" :key="r.id" class="card flex flex-col p-4">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <h2 class="font-semibold">{{ r.name }}</h2>
              <p class="text-xs text-muted">{{ r.code }}<template v-if="r.description"> · {{ r.description }}</template></p>
            </div>
            <StatusBadge :tone="r.locked ? 'warning' : r.is_system ? 'neutral' : 'info'" :label="r.locked ? 'Owner' : r.is_system ? 'Built-in' : 'Custom'" />
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
            <span class="flex gap-1">
              <AppButton size="sm" variant="ghost" :aria-label="`${r.locked ? 'View' : 'Edit'} ${r.name}`" @click="edit(r)">{{ r.locked ? 'View' : 'Edit' }}</AppButton>
              <AppButton v-if="!r.is_system" size="sm" variant="ghost" :disabled="r.employees > 0" :title="r.employees > 0 ? 'Give its people another role first' : undefined"
                :loading="busy === `d${r.id}`" :aria-label="`Delete ${r.name}`" @click="remove(r)">Delete</AppButton>
            </span>
          </div>
        </article>
      </div>
    </StateView>

    <AppModal v-model="open" :title="form.id ? (form.locked ? form.name : `Edit ${form.name}`) : 'New role'" wide>
      <form id="role-form" class="grid gap-4" @submit.prevent="save">
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField label="Name" for="r-name"><input id="r-name" v-model="form.name" class="input" required maxlength="64" placeholder="e.g. Regional Supervisor" /></FormField>
          <FormField v-if="!form.id" label="Start from" for="r-tpl" hint="Copies that role's permissions.">
            <SearchSelect id="r-tpl" v-model="template" :options="templateOptions" placeholder="Blank" />
          </FormField>
          <FormField label="Description" for="r-desc" class="sm:col-span-2"><input id="r-desc" v-model="form.description" class="input" maxlength="255" /></FormField>
        </div>
        <p v-if="form.locked" class="rounded-lg bg-warning/10 px-3 py-2 text-sm text-warning">The owner role always has every permission, so nobody can be locked out.</p>
        <fieldset v-for="g in groups" :key="g.name" class="rounded-lg border border-border p-3" :disabled="form.locked">
          <legend class="flex items-center gap-2 px-1 text-sm font-semibold">
            {{ g.name }}
            <button v-if="!form.locked" type="button" class="text-xs font-normal text-primary hover:underline" @click="toggleGroup(g.items)">
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
        <AppButton variant="secondary" @click="open = false">{{ form.locked ? 'Close' : 'Cancel' }}</AppButton>
        <AppButton v-if="!form.locked || form.id" type="submit" form="role-form" :loading="busy === 'save'">Save</AppButton>
      </template>
    </AppModal>
  </div>
</template>
