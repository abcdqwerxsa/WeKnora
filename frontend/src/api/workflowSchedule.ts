import { get, post, del } from '@/utils/request'

/**
 * Workflow schedule client — cron-scheduled runs of published workflows.
 * Schedules are per-workflow; only published workflows may be scheduled.
 * All routes are JWT-only (deliberately not in the API-key allowlist).
 */

export interface WorkflowSchedule {
  id: string
  tenant_id?: number
  workflow_id: string
  creator_id?: string
  /** Standard 5-field cron expression (min hour dom month dow). */
  cron: string
  query?: string
  inputs?: Record<string, unknown> | null
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface CreateWorkflowSchedulePayload {
  cron: string
  query?: string
  inputs?: Record<string, unknown>
  enabled?: boolean
}

export interface WorkflowScheduleListResponse {
  success: boolean
  message?: string
  data?: { schedules: WorkflowSchedule[]; total: number }
}

export interface WorkflowScheduleMutationResponse {
  success: boolean
  data?: WorkflowSchedule
  message?: string
}

export const listWorkflowSchedules = (workflowId: string): Promise<WorkflowScheduleListResponse> =>
  get(`/api/v1/workflows/${workflowId}/schedules`)

export const createWorkflowSchedule = (
  workflowId: string,
  payload: CreateWorkflowSchedulePayload,
): Promise<WorkflowScheduleMutationResponse> => post(`/api/v1/workflows/${workflowId}/schedules`, payload)

export const deleteWorkflowSchedule = (workflowId: string, scheduleId: string): Promise<{ success: boolean }> =>
  del(`/api/v1/workflows/${workflowId}/schedules/${scheduleId}`)

export const enableWorkflowSchedule = (workflowId: string, scheduleId: string): Promise<WorkflowScheduleMutationResponse> =>
  post(`/api/v1/workflows/${workflowId}/schedules/${scheduleId}/enable`)

export const disableWorkflowSchedule = (workflowId: string, scheduleId: string): Promise<WorkflowScheduleMutationResponse> =>
  post(`/api/v1/workflows/${workflowId}/schedules/${scheduleId}/disable`)
