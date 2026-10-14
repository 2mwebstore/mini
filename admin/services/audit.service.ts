import type { AuditLog } from '~/types/api'
import { listPage, type Paging } from './api'

export const auditService = {
  page: (query: { action?: string; employee_id?: number }, p: Paging) => listPage<AuditLog>('/audit-logs', query, p),
}
