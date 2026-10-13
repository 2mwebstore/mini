import { defineStore } from 'pinia'

export interface Toast {
  id: number
  tone: 'success' | 'error'
  message: string
}

let next = 1

export const useToastStore = defineStore('toast', {
  state: () => ({ items: [] as Toast[] }),
  actions: {
    push(tone: Toast['tone'], message: string) {
      const id = next++
      this.items.push({ id, tone, message })
      setTimeout(() => this.dismiss(id), tone === 'error' ? 6000 : 3500)
    },
    success(message: string) {
      this.push('success', message)
    },
    error(e: unknown) {
      this.push('error', e instanceof Error ? e.message : String(e))
    },
    dismiss(id: number) {
      this.items = this.items.filter(t => t.id !== id)
    },
  },
})
