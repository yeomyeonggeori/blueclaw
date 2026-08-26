export type PolicyDocument = {
  people: Array<{
    personID: string;
    displayName: string;
    emails: string[];
    securityLevelName: string;
    securityLevelRank: number;
    grantedClasses: string[];
    circles: string[];
    isAdmin: boolean;
  }>;
  circles: Array<{
    circleID: string;
    displayName: string;
    mattermostChannelID: string;
    isMattermostManaged: boolean;
    workspaceDirectoryPath: string;
  }>;
  circleSync: {
    mattermostPrivateChannels: Array<{
      circleID: string;
      channelName: string;
      channelID: string;
    }>;
  };
  resourceAccess: Array<{
    resource: string;
    actions: string[];
    circles: string[];
  }>;
  channels: Array<{
    platform: string;
    externalConversationID: string;
    conversationType: string;
    displayName: string;
    defaultSecurityLevelRank: number;
    defaultRequiredClasses: string[];
    isCollectEnabled: boolean;
    isReplyEnabled: boolean;
  }>;
  retention: {
    rawEventDays: number;
  };
};

export async function loadPolicyDocument(): Promise<PolicyDocument> {
  const response = await fetch("/admin/api/policy");
  return response.json();
}

export async function validatePolicyDocument(policyDocument: PolicyDocument): Promise<Response> {
  return fetch("/admin/api/policy/validate", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(policyDocument)
  });
}

export async function savePolicyDocument(policyDocument: PolicyDocument): Promise<Response> {
  return fetch("/admin/api/policy/save", {
    method: "POST",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(policyDocument)
  });
}

export async function loadAuditEntries(): Promise<Array<{ actionName: string; body: string; createdAt: string }>> {
  const response = await fetch("/admin/api/audit");
  return response.json();
}

export async function loadAdminTaskRuns(): Promise<Array<Record<string, unknown>>> {
  const response = await fetch("/admin/api/run");
  return response.json();
}
