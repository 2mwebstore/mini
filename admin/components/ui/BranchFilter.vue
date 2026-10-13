<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'
import { useLookupsStore } from '~/stores/lookups'

// Branch picker for company-wide users (owner/HR). Branch admins never see it:
// the API already limits them to their own branch.
const model = defineModel<string>({ required: true })
defineProps<{ id: string }>()
const auth = useAuthStore()
const lookups = useLookupsStore()
const visible = computed(() => auth.can('all.view'))
onMounted(() => visible.value && lookups.loadBranches().catch(() => {}))
</script>

<template>
  <FormField v-if="visible" label="Branch" :for="id">
    <SearchSelect :id="id" v-model="model" :options="lookups.branchOptions" empty-label="All" />
  </FormField>
</template>
