import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'

/**
 * The branch a setup page (departments, positions, shifts, leave types, holidays)
 * shows. Each of those lists belongs to one branch. `branchId` is the picked
 * branch ("" = all the admin's branches, from ?branch_id= on arrival); `target` is
 * where new items go: the picked branch, or the admin's only branch.
 */
export function useBranchPick() {
  const auth = useAuthStore()
  const lookups = useLookupsStore()
  const route = useRoute()
  onMounted(() => lookups.loadBranches().catch(() => {}))
  const branchId = ref(typeof route.query.branch_id === 'string' ? route.query.branch_id : '')
  const onlyBranch = computed(() => (lookups.branches?.length === 1 ? String(lookups.branches[0]!.id) : ''))
  const target = computed(() => branchId.value || onlyBranch.value)
  /** Items of several branches are listed together: label each with its branch. */
  const showBranch = computed(() => auth.multiBranch && !branchId.value)
  const branchName = (id: number) => lookups.branches?.find(b => b.id === id)?.name ?? ''
  return { branchId, target, showBranch, branchName, picker: computed(() => auth.multiBranch) }
}
