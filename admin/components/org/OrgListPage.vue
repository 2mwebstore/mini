<script setup lang="ts">
import { employeeService } from '~/services/employee.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { OrgItem } from '~/types/api'

// One managed list (departments or positions): add, rename, delete when unused.
const props = defineProps<{ kind: 'departments' | 'positions'; title: string; noun: string; subtitle: string }>()
const auth = useAuthStore()
const lookups = useLookupsStore()
// Company-wide lists: changed by staff managers who reach every branch.
const canEdit = computed(() => auth.can('staff.manage') && auth.can('all.view'))
const list = useResource(() => employeeService.orgList(props.kind))
const { busy, run } = useAction()

const newName = ref('')
const editing = ref<number | null>(null)
const editName = ref('')
const filterKey = computed(() => (props.kind === 'departments' ? 'department_id' : 'position_id'))

function refreshLookups() {
  if (props.kind === 'departments') lookups.loadDepartments(true).catch(() => {})
  else lookups.loadPositions(true).catch(() => {})
}
async function add() {
  const name = newName.value.trim()
  if (!name) return
  if (await run('add', () => employeeService.orgSave(props.kind, 0, name), `${props.noun} added`)) {
    newName.value = ''
    list.load()
    refreshLookups()
  }
}
function startEdit(item: OrgItem) {
  editing.value = item.id
  editName.value = item.name
}
async function rename(item: OrgItem) {
  const name = editName.value.trim()
  if (!name || name === item.name) {
    editing.value = null
    return
  }
  if (await run(`r${item.id}`, () => employeeService.orgSave(props.kind, item.id, name), `${props.noun} renamed`)) {
    editing.value = null
    list.load()
    refreshLookups()
  }
}
async function remove(item: OrgItem) {
  if (!window.confirm(`Delete "${item.name}"?`)) return
  if (await run(`d${item.id}`, () => employeeService.orgDelete(props.kind, item.id), `${props.noun} deleted`)) {
    list.load()
    refreshLookups()
  }
}
</script>

<template>
  <div>
    <PageHeader :title="title" :subtitle="subtitle" />
    <section class="card max-w-3xl p-5">
      <form v-if="canEdit" class="mb-4 flex flex-wrap items-end gap-2" @submit.prevent="add">
        <FormField :label="`New ${noun.toLowerCase()}`" :for="`org-new-${kind}`" class="min-w-48 flex-1">
          <input :id="`org-new-${kind}`" v-model="newName" class="input" maxlength="100" required />
        </FormField>
        <AppButton type="submit" :loading="busy === 'add'">Add</AppButton>
      </form>
      <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.data.value?.length === 0" :empty-text="`No ${kind} yet.`" @retry="list.load">
        <ul class="divide-y divide-border">
          <li v-for="item in list.data.value ?? []" :key="item.id" class="flex flex-wrap items-center gap-3 py-2.5 text-sm">
            <form v-if="editing === item.id" class="flex flex-1 items-center gap-2" @submit.prevent="rename(item)">
              <input v-model="editName" class="input flex-1" maxlength="100" required :aria-label="`Rename ${item.name}`" @keydown.esc="editing = null" />
              <AppButton type="submit" size="sm" :loading="busy === `r${item.id}`">Save</AppButton>
              <AppButton size="sm" variant="ghost" @click="editing = null">Cancel</AppButton>
            </form>
            <template v-else>
              <span class="flex-1 font-medium">{{ item.name }}</span>
              <NuxtLink :to="`/employees?${filterKey}=${item.id}`" class="text-xs text-muted hover:text-primary hover:underline">
                {{ item.employees }} {{ item.employees === 1 ? 'person' : 'people' }}
              </NuxtLink>
              <span v-if="canEdit" class="flex gap-1">
                <AppButton size="sm" variant="ghost" :aria-label="`Rename ${item.name}`" @click="startEdit(item)">Rename</AppButton>
                <AppButton
                  size="sm" variant="ghost" :disabled="item.employees > 0" :loading="busy === `d${item.id}`"
                  :title="item.employees > 0 ? 'Move its people first' : undefined" :aria-label="`Delete ${item.name}`" @click="remove(item)"
                >Delete</AppButton>
              </span>
            </template>
          </li>
        </ul>
      </StateView>
    </section>
  </div>
</template>
