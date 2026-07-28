import { defineStore } from "pinia";

import { apiClient } from "../services/api";
import type { WorkspaceMember, WorkspaceMemberCandidate, WorkspaceRole } from "../types/domain";

/** Members load state — for member-management UI only (not authorization). */
export type MembersLoadStatus = "IDLE" | "LOADING" | "LOADED" | "ERROR";

interface WorkspaceMembersState {
  membersByWorkspace: Record<string, WorkspaceMember[]>;
  membersLoadStatusByWorkspace: Record<string, MembersLoadStatus>;
}

/**
 * Member CRUD store (ZKL-64). Does not participate in workspace permission
 * projection — that comes from Workspace.currentUserRole.
 */
export const useWorkspaceMembersStore = defineStore("workspaceMembers", {
  state: (): WorkspaceMembersState => ({
    membersByWorkspace: {},
    membersLoadStatusByWorkspace: {},
  }),
  actions: {
    async loadMembers(workspaceId: string, includeDisabled = false) {
      this.membersLoadStatusByWorkspace[workspaceId] = "LOADING";
      try {
        const params = includeDisabled ? { includeDisabled: "true" } : undefined;
        const response = await apiClient.get<{ items: WorkspaceMember[] }>(`/workspaces/${workspaceId}/members`, {
          params,
        });
        this.membersByWorkspace[workspaceId] = response.data.items;
        this.membersLoadStatusByWorkspace[workspaceId] = "LOADED";
        return response.data.items;
      } catch (error) {
        this.membersLoadStatusByWorkspace[workspaceId] = "ERROR";
        throw error;
      }
    },
    async searchMemberCandidates(workspaceId: string, query = "", limit = 20) {
      const params = new URLSearchParams({ query: query.trim(), limit: String(limit) });
      const response = await apiClient.get<{ items: WorkspaceMemberCandidate[] }>(
        `/workspaces/${workspaceId}/member-candidates?${params.toString()}`,
      );
      return response.data.items;
    },
    async addMember(workspaceId: string, userId: string, role: WorkspaceRole) {
      const response = await apiClient.post<WorkspaceMember>(`/workspaces/${workspaceId}/members`, { userId, role });
      this.upsertMember(workspaceId, response.data);
      return response.data;
    },
    async changeMemberRole(workspaceId: string, userId: string, role: WorkspaceRole) {
      const response = await apiClient.patch<WorkspaceMember>(`/workspaces/${workspaceId}/members/${userId}`, {
        role,
      });
      this.upsertMember(workspaceId, response.data);
      return response.data;
    },
    async removeMember(workspaceId: string, userId: string) {
      await apiClient.delete(`/workspaces/${workspaceId}/members/${userId}`);
      this.membersByWorkspace[workspaceId] = (this.membersByWorkspace[workspaceId] || []).filter(
        (member) => member.userId !== userId,
      );
    },
    upsertMember(workspaceId: string, member: WorkspaceMember) {
      const list = this.membersByWorkspace[workspaceId] || [];
      const index = list.findIndex((item) => item.userId === member.userId);
      if (index < 0) {
        this.membersByWorkspace[workspaceId] = [...list, member];
        return;
      }
      const next = list.slice();
      next[index] = member;
      this.membersByWorkspace[workspaceId] = next;
    },
  },
});
