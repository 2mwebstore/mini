<script setup lang="ts">
import { useAuthStore } from '~/stores/auth'

const auth = useAuthStore()
const inTelegram = insideTelegram()
const telegramId = webApp()?.initDataUnsafe.user?.id

useHead({ title: computed(() => auth.settings?.company_name ? `${auth.settings.company_name} Attendance` : 'Attendance') })
</script>

<template>
  <div v-if="!inTelegram" class="flex min-h-screen flex-col items-center justify-center gap-3 p-8 text-center">
    <span class="text-5xl" aria-hidden="true">📱</span>
    <h1 class="text-xl font-semibold">Open in Telegram</h1>
    <p class="text-hint">This app runs inside Telegram. Open the company attendance bot and tap <b>Open App</b>.</p>
  </div>

  <div v-else-if="auth.status === 'error'" class="flex min-h-screen flex-col items-center justify-center gap-3 p-8 text-center">
    <template v-if="auth.error?.code === 'NOT_REGISTERED' || auth.error?.code === 'INACTIVE'">
      <span class="text-5xl" aria-hidden="true">🔒</span>
      <h1 class="text-xl font-semibold">No access</h1>
      <p class="text-hint">{{ auth.error.message }}</p>
      <p v-if="telegramId" class="text-sm text-hint">Your Telegram ID: <code class="select-all font-mono text-fg">{{ telegramId }}</code></p>
    </template>
    <template v-else>
      <span class="text-5xl" aria-hidden="true">⚠️</span>
      <p class="text-hint">{{ auth.error?.message }}</p>
      <AppButton v-if="auth.error?.code !== 'INIT_DATA_EXPIRED'" variant="secondary" @click="auth.load()">Try again</AppButton>
      <AppButton v-else variant="secondary" @click="webApp()?.close()">Close</AppButton>
    </template>
  </div>

  <div v-else-if="auth.status !== 'ready'" class="flex min-h-screen items-center justify-center" role="status" aria-label="Loading">
    <span class="size-7 animate-spin rounded-full border-2 border-hint/40 border-t-primary" />
  </div>

  <NuxtLayout v-else>
    <NuxtPage />
  </NuxtLayout>
</template>
