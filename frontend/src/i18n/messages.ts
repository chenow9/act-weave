import type { AppLocale } from "./types";

import enAuth from "../locales/en/auth.json";
import enCommon from "../locales/en/common.json";
import enErrors from "../locales/en/errors.json";
import enNav from "../locales/en/nav.json";
import enOverview from "../locales/en/overview.json";
import enShell from "../locales/en/shell.json";
import enWorkspaces from "../locales/en/workspaces.json";
import enAgents from "../locales/en/agents.json";
import enTools from "../locales/en/tools.json";
import enWorkflow from "../locales/en/workflow.json";
import enLogs from "../locales/en/logs.json";
import enChat from "../locales/en/chat.json";
import enProviders from "../locales/en/providers.json";
import enOpenapi from "../locales/en/openapi.json";
import enAgentAccess from "../locales/en/agentAccess.json";
import enUsers from "../locales/en/users.json";
import enModelApis from "../locales/en/modelApis.json";
import enConnections from "../locales/en/connections.json";
import zhAuth from "../locales/zh-CN/auth.json";
import zhCommon from "../locales/zh-CN/common.json";
import zhErrors from "../locales/zh-CN/errors.json";
import zhNav from "../locales/zh-CN/nav.json";
import zhOverview from "../locales/zh-CN/overview.json";
import zhShell from "../locales/zh-CN/shell.json";
import zhWorkspaces from "../locales/zh-CN/workspaces.json";
import zhAgents from "../locales/zh-CN/agents.json";
import zhTools from "../locales/zh-CN/tools.json";
import zhWorkflow from "../locales/zh-CN/workflow.json";
import zhLogs from "../locales/zh-CN/logs.json";
import zhChat from "../locales/zh-CN/chat.json";
import zhProviders from "../locales/zh-CN/providers.json";
import zhOpenapi from "../locales/zh-CN/openapi.json";
import zhAgentAccess from "../locales/zh-CN/agentAccess.json";
import zhUsers from "../locales/zh-CN/users.json";
import zhModelApis from "../locales/zh-CN/modelApis.json";
import zhConnections from "../locales/zh-CN/connections.json";

export const messages = {
  "zh-CN": {
    common: zhCommon,
    nav: zhNav,
    shell: zhShell,
    auth: zhAuth,
    errors: zhErrors,
    overview: zhOverview,
    workspaces: zhWorkspaces,
    agents: zhAgents,
    tools: zhTools,
    workflow: zhWorkflow,
    logs: zhLogs,
    chat: zhChat,
    providers: zhProviders,
    openapi: zhOpenapi,
    agentAccess: zhAgentAccess,
    users: zhUsers,
    modelApis: zhModelApis,
    connections: zhConnections,
  },
  en: {
    common: enCommon,
    nav: enNav,
    shell: enShell,
    auth: enAuth,
    errors: enErrors,
    overview: enOverview,
    workspaces: enWorkspaces,
    agents: enAgents,
    tools: enTools,
    workflow: enWorkflow,
    logs: enLogs,
    chat: enChat,
    providers: enProviders,
    openapi: enOpenapi,
    agentAccess: enAgentAccess,
    users: enUsers,
    modelApis: enModelApis,
    connections: enConnections,
  },
} as const satisfies Record<AppLocale, Record<string, unknown>>;

export type MessageSchema = (typeof messages)["zh-CN"];
