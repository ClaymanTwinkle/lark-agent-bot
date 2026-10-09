import api from './client';

export interface SystemStatus {
  version: string;
  uptime_seconds: number;
  connected_platforms: string[];
  projects_count: number;
  bridge_adapters: { platform: string; project: string; capabilities: string[] }[];
}

export const getStatus = () => api.get<SystemStatus>('/status');
// With wait, the restart waits for tasks in progress (busy) to finish, up to
// max_wait_mins.
export const restartSystem = (body?: { session_key?: string; platform?: string; wait?: boolean }) =>
  api.post<{ message: string; busy?: number; max_wait_mins?: number }>('/restart', body);
export const reloadConfig = () => api.post<{ message: string; projects_added: string[]; projects_removed: string[]; projects_updated: string[] }>('/reload');
