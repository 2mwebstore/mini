<script setup lang="ts">
import { employeeService } from '~/services/employee.service'
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'
import type { OrgItem } from '~/types/api'

// One managed list (departments or positions). Each item belongs to one branch:
// add, rename, delete when unused.
const props = defineProps<{ kind: 'departments' | 'positions'; title: string; noun: string; subtitle: string }>()
const auth = useAuthStore()
const lookups = useLookupsStore()
const canEdit = computed(() => auth.can('staff.manage'))
const { branchId, target, showBranch, picker } = useBranchPick()
const list = usePaged(p => employeeService.orgPage(props.kind, Number(branchId.value) || undefined, p))
watch(branchId, () => list.reset())

const { busy, run } = useAction()
const { confirm } = useConfirm()

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
  if (!name || !target.value) return
  if (await run('add', () => employeeService.orgSave(props.kind, 0, name, Number(target.value)), `${props.noun} added`)) {
    newName.value = ''
    list.refresh()
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
    list.refresh()
    refreshLookups()
  }
}
async function remove(item: OrgItem) {
  if (!(await confirm({ title: `Delete “${item.name}”?`, message: `Nobody is assigned to it${showBranch.value ? ` in ${item.branch_name}` : ''}.` }))) return
  if (await run(`d${item.id}`, () => employeeService.orgDelete(props.kind, item.id), `${props.noun} deleted`)) {
    list.refresh()
    refreshLookups()
  }
}
</script>

<template>
  <div>
    <PageHeader :title="title" :subtitle="subtitle" />
    <section class="card max-w-3xl p-5">
      <div v-if="picker" class="mb-4 max-w-xs">
        <FormField label="Branch" for="org-branch">
          <SearchSelect id="org-branch" v-model="branchId" :options="lookups.branchOptions" empty-label="All my branches" />
        </FormField>
      </div>
      <form v-if="canEdit" class="mb-4 flex flex-wrap items-end gap-2" @submit.prevent="add">
        <FormField :label="`New ${noun.toLowerCase()}`" :for="`org-new-${kind}`" class="min-w-48 flex-1">
          <input :id="`org-new-${kind}`" v-model="newName" class="input" maxlength="100" required :disabled="!target" :placeholder="target ? '' : 'Choose a branch first'" />
        </FormField>
        <AppButton type="submit" :loading="busy === 'add'" :disabled="!target">Add</AppButton>
      </form>
      <StateView :loading="list.loading.value && !list.data.value" :error="list.error.value" :empty="list.total.value === 0" :empty-text="`No ${kind} yet.`" @retry="list.load">
        <ul class="divide-y divide-border" :class="{ 'opacity-60': list.loading.value }">
          <li v-for="item in list.rows.value" :key="item.id" class="flex flex-wrap items-center gap-3 py-2.5 text-sm">
            <form v-if="editing === item.id" class="flex flex-1 items-center gap-2" @submit.prevent="rename(item)">
              <input v-model="editName" class="input flex-1" maxlength="100" required :aria-label="`Rename ${item.name}`" @keydown.esc="editing = null" />
              <AppButton type="submit" size="sm" :loading="busy === `r${item.id}`">Save</AppButton>
              <AppButton size="sm" variant="ghost" @click="editing = null">Cancel</AppButton>
            </form>
            <template v-else>
              <span class="flex-1">
                <span class="font-medium">{{ item.name }}</span>
                <span v-if="showBranch" class="ml-2 text-xs text-muted">{{ item.branch_name }}</span>
              </span>
              <span class="text-xs text-muted">{{ item.employees }} {{ item.employees === 1 ? 'person' : 'people' }}</span>
              <span class="flex gap-1.5">
                <IconAction kind="view" :to="`/employees?branch_id=${item.branch_id}&${filterKey}=${item.id}`" :label="`View the people in ${item.name}`" />
                <template v-if="canEdit">
                  <IconAction kind="edit" :label="`Rename ${item.name}`" @click="startEdit(item)" />
                  <IconAction
                    kind="delete" :disabled="item.employees > 0" :loading="busy === `d${item.id}`"
                    :label="item.employees > 0 ? `Move the people of ${item.name} first` : `Delete ${item.name}`" @click="remove(item)"
                  />
                </template>
              </span>
            </template>
          </li>
        </ul>
        <div class="-mx-5 -mb-5 mt-2">
          <AppPagination :page="list.page.value" :page-size="list.pageSize.value" :total="list.total.value" :noun="noun.toLowerCase()" @go="list.go" @size="list.resize" />
        </div>
      </StateView>
    </section>
  </div>
</template>
