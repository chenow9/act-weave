import { defineStore } from "pinia";

import {
  apiClient,
  apiErrorMessage,
  refreshAuthSession,
  setAuthSessionHooks,
  setAuthToken,
  type AuthTokenResponse,
  type AuthUserDTO,
} from "../services/api";
import { applyUserLocale } from "../services/locale";
import { tt } from "../i18n/tt";
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
      applyUserLocale(session.user.locale);
    },
    applyUser(user: AuthUserDTO | User) {
      this.user = userFromDTO(user as AuthUserDTO);
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
        this.error = apiErrorMessage(error, tt("auth.signInFailed"));
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
      applyUserLocale(response.data.locale);
    },
    async restoreSession() {
      this.bindAPIClient();
      if (this.initialized) {
        return;
      }
      if (!restoreInFlight) {
        restoreInFlight = (async () => {
          try {
            const session = await refreshAuthSession();
            this.applySession(session);
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
      this.loading = true;
      this.error = "";
      try {
        // Always ask the Cookie-backed endpoint to revoke the server session. A
        // Refresh Cookie can still exist even when the in-memory Access Token was
        // cleared by a prior failed request.
        await apiClient.post("/auth/logout");
        this.clearSession();
        this.initialized = true;
      } catch (error) {
        // Keep the authenticated UI intact: claiming success while the HttpOnly
        // Refresh Cookie remains valid would allow a silent login after reload.
        this.error = apiErrorMessage(error, tt("auth.signOutFailed"));
        throw error;
      } finally {
        this.loading = false;
      }
    },
    /**
     * Change the current user's password. On 204 the server has already revoked
     * all sessions; the client must clear local auth and re-login. Passwords are
     * never stored in Pinia state or browser storage.
     */
    async changePassword(currentPassword: string, newPassword: string) {
      this.bindAPIClient();
      this.loading = true;
      this.error = "";
      try {
        await apiClient.post("/users/me:change-password", {
          currentPassword,
          newPassword,
        });
        this.clearSession();
        this.initialized = true;
      } catch (error) {
        this.error = apiErrorMessage(error, tt("auth.changePasswordFailed"));
        throw error;
      } finally {
        this.loading = false;
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
