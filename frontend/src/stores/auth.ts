import { defineStore } from "pinia";

import {
  apiClient,
  apiErrorMessage,
  setAuthSessionHooks,
  setAuthToken,
  type AuthTokenResponse,
  type AuthUserDTO,
} from "../services/api";
import type { User } from "../types/domain";

interface AuthState {
  token: string;
  user: User | null;
  mustChangePassword: boolean;
  initialized: boolean;
  clientBound: boolean;
  loading: boolean;
  error: string;
}

let restoreInFlight: Promise<void> | null = null;

export const useAuthStore = defineStore("auth", {
  state: (): AuthState => ({
    token: "",
    user: null,
    mustChangePassword: false,
    initialized: false,
    clientBound: false,
    loading: false,
    error: "",
  }),
  getters: {
    isAuthenticated: (state) => Boolean(state.token && state.user),
  },
  actions: {
    bindAPIClient() {
      if (this.clientBound) {
        return;
      }
      this.clientBound = true;
      clearLegacyStoredSession();
      setAuthSessionHooks({
        onRefreshed: (session) => this.applySession(session),
        onExpired: () => this.clearSession(),
      });
    },
    applySession(session: AuthTokenResponse) {
      this.token = session.accessToken;
      this.user = userFromDTO(session.user);
      this.mustChangePassword = session.mustChangePassword;
      setAuthToken(session.accessToken);
    },
    clearSession() {
      this.token = "";
      this.user = null;
      this.mustChangePassword = false;
      setAuthToken("");
    },
    async login(username: string, password: string) {
      this.bindAPIClient();
      this.loading = true;
      this.error = "";

      try {
        const response = await apiClient.post<AuthTokenResponse>("/auth/login", { username, password });
        this.applySession(response.data);
        this.initialized = true;
      } catch (error) {
        this.clearSession();
        this.error = apiErrorMessage(error, "登录失败，请检查用户名或密码。");
        throw error;
      } finally {
        this.loading = false;
      }
    },
    async loadCurrentUser() {
      if (!this.token) {
        return;
      }

      const response = await apiClient.get<AuthUserDTO>("/users/me");
      this.user = userFromDTO(response.data);
    },
    async restoreSession() {
      this.bindAPIClient();
      if (this.initialized) {
        return;
      }
      if (!restoreInFlight) {
        restoreInFlight = (async () => {
          try {
            const response = await apiClient.post<AuthTokenResponse>("/auth/refresh");
            this.applySession(response.data);
          } catch {
            this.clearSession();
          } finally {
            this.initialized = true;
            restoreInFlight = null;
          }
        })();
      }
      await restoreInFlight;
    },
    async logout() {
      this.bindAPIClient();
      const revoke = this.token ? apiClient.post("/auth/logout") : Promise.resolve();
      this.clearSession();
      try {
        await revoke;
      } catch {
        // Local logout is authoritative; the HttpOnly refresh cookie is short-lived
        // and the server request is best effort when connectivity is unavailable.
      }
    },
  },
});

function userFromDTO(value: AuthUserDTO): User {
  return {
    ...value,
    role: value.platformRole === "PLATFORM_ADMIN" ? "Platform Admin" : "User",
  };
}

function clearLegacyStoredSession() {
  if (typeof localStorage !== "undefined") {
    localStorage.removeItem("actweave.session");
  }
}
