import type { DailyReport, Report } from '~/types/api'
import { api, apiUrl } from './api'

export interface ReportFilter {
  from: string
  to: string
  employee_id?: number
  department_id?: number
  branch_id?: number
}

export const reportService = {
  daily: (date?: string, branchId?: number) => api.get<DailyReport>('/reports/daily', { date, branch_id: branchId }),
  summary: (f: ReportFilter) => api.get<Report>('/reports/summary', { ...f }),
  /** The browser downloads it directly (the session cookie authenticates the GET). */
  exportUrl: (f: ReportFilter, format: 'xlsx' | 'csv') => apiUrl('/reports/export', { ...f, format }),
}
