<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { overtimeService } from '~/services/overtime.service'
import type { OvertimeRequest } from '~/types/api'
import { formatMinutes } from '~/utils/format'

// The employee's overtime: what they asked for, its status, and cancelling.
const { data: items, error, loading, load } = useResource(() => overtimeService.mine())
const approved = computed(() => (items.value ?? []).filter(o => o.status === 'APPROVED').reduce((n, o) => n + o.minutes, 0))
const pending = computed(() => (items.value ?? []).filter(o => o.status === 'PENDING').reduce((n, o) => n + o.minutes, 0))
// Pending, or approved and not started yet (the API checks the same).
const cancellable = (o: OvertimeRequest) => o.status === 'PENDING' || (o.status === 'APPROVED' && new Date(o.start_at) > new Date())
const cancelling = ref<number | null>(null)

async function cancel(o: OvertimeRequest) {
  if (!(await confirmDialog('Cancel this overtime request?'))) return
  cancelling.value = o.id
  try {
    await overtimeService.cancel(o.id)
    haptic('success')
    await load()
  } catch (e) {
    haptic('error')
    await alertDialog((e as ApiError).message)
  } finally {
    cancelling.value = null
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <header class="flex items-center justify-between">
      <h1 class="text-2xl font-bold">Overtime</h1>
      <AppButton variant="secondary" class="py-2" @click="navigateTo('/overtime/new')">
        <AppIcon name="plus" class="size-4" />New request
      </AppButton>
    </header>

    <div v-if="items?.length" class="grid grid-cols-2 gap-2">
      <div class="rounded-2xl bg-surface px-3 py-3 text-center">
        <p class="text-lg font-semibold tabular-nums text-success">{{ formatMinutes(approved) }}</p>
        <p class="text-xs text-hint">Approved</p>
      </div>
      <div class="rounded-2xl bg-surface px-3 py-3 text-center">
        <p class="text-lg font-semibold tabular-nums text-warning">{{ formatMinutes(pending) }}</p>
        <p class="text-xs text-hint">Waiting for approval</p>
      </div>
    </div>

    <StateView :loading="loading && !items" :error="error" :empty="!items?.length" empty-text="You have no overtime requests yet." @retry="load">
      <template #empty-action>
        <AppButton @click="navigateTo('/overtime/new')">Request overtime</AppButton>
      </template>
      <AppCard>
        <div class="-my-3 divide-y divide-separator">
          <OvertimeItem v-for="o in items" :key="o.id" :item="o">
            <AppButton
              v-if="cancellable(o)" variant="ghost" class="-ml-4 mt-1 py-1 text-danger"
              :loading="cancelling === o.id" @click="cancel(o)"
            >Cancel request</AppButton>
          </OvertimeItem>
        </div>
      </AppCard>
    </StateView>
  </div>
</template>
