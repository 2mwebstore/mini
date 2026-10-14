<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'

// Branch picker for admins who see several branches (the owner, HR, a company
// admin with several branches). An admin of one branch never sees it: the API
// already limits them to that branch.
const model = defineModel<string>({ required: true })
defineProps<{ id: string }>()
const auth = useAuthStore()
const lookups = useLookupsStore()
const visible = computed(() => auth.multiBranch)
onMounted(() => visible.value && lookups.loadBranches().catch(() => {}))
</script>

<template>
  <FormField v-if="visible" label="Branch" :for="id">
    <SearchSelect :id="id" v-model="model" :options="lookups.branchOptions" empty-label="All" />
  </FormField>
</template>
