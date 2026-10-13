import type { Department, Employee, EmployeeInput, Items, WorkSchedule } from '~/types/api'
import { api } from './api'

export const employeeService = {
  list: (query: { q?: string; department_id?: number; branch_id?: number; status?: string; include_inactive?: boolean } = {}) =>
    api.get<Items<Employee>>('/employees', query).then(r => r.items ?? []),
  get: (id: number) => api.get<Employee>(`/employees/${id}`),
  create: (input: EmployeeInput) => api.post<Employee>('/employees', input),
  update: (id: number, input: EmployeeInput) => api.put<Employee>(`/employees/${id}`, input),
  deactivate: (id: number) => api.del(`/employees/${id}`),
  createLogin: (id: number, email: string, password: string) => api.post(`/employees/${id}/user`, { email, password }),
  departments: () => api.get<Items<Department>>('/departments').then(r => r.items ?? []),
  schedules: () => api.get<Items<WorkSchedule>>('/work-schedules').then(r => r.items ?? []),
}
