import type { AuditLog, Items } from '~/types/api'
import { api } from './api'

export const auditService = {
  list: (query: { action?: string; employee_id?: number; before_id?: number; limit?: number }) =>
    api.get<Items<AuditLog>>('/audit-logs', query).then(r => r.items ?? []),
}
