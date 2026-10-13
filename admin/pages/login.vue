<script setup lang="ts">
import type { ApiError } from '~/services/api'
import { useAuthStore } from '~/stores/auth'

definePageMeta({ layout: 'auth', public: true })

const auth = useAuthStore()
const route = useRoute()
const email = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await auth.login(email.value, password.value)
    const to = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/') ? route.query.redirect : '/'
    await navigateTo(to)
  } catch (e) {
    error.value = (e as ApiError).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="w-full max-w-sm">
    <div class="mb-6 text-center">
      <h1 class="text-xl font-semibold">Attendance admin</h1>
      <p class="mt-1 text-sm text-muted">Sign in with your admin panel account.</p>
    </div>
    <form class="card flex flex-col gap-4 p-6" @submit.prevent="submit">
      <FormField label="Email" for="email">
        <input id="email" v-model="email" class="input" type="email" autocomplete="username" required autofocus />
      </FormField>
      <FormField label="Password" for="password">
        <input id="password" v-model="password" class="input" type="password" autocomplete="current-password" required />
      </FormField>
      <p v-if="error" class="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger" role="alert">{{ error }}</p>
      <AppButton type="submit" :loading="busy">Sign in</AppButton>
    </form>
    <p class="mt-4 text-center text-xs text-muted">Accounts are created by a super admin.</p>
  </div>
</template>
