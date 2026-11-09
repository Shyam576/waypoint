export type Role = 'admin' | 'developer' | 'approver' | 'release_coordinator' | 'support';

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export type ProjectStatus = 'active' | 'archived';

export interface Project {
  id: string;
  name: string;
  slug: string;
  owning_team: string;
  repo_url?: string | null;
  status: ProjectStatus;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface Policy {
  id: string;
  project_id: string;
  required_evidence_types: string[];
  required_approvals_count: number;
  block_on_open_rejection: boolean;
  updated_by?: string;
  updated_at: string;
}

export type Stage =
  | 'draft'
  | 'development'
  | 'uat'
  | 'ready_for_production'
  | 'production'
  | 'post_release_support'
  | 'closed';

export type RiskLevel = 'low' | 'medium' | 'high';

export interface Release {
  id: string;
  project_id: string;
  owner_id: string;
  name: string;
  target_date?: string | null;
  risk_level: RiskLevel;
  summary: string;
  rollback_plan: string;
  support_period_days: number;
  stage: Stage;
  support_ends_at?: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export type LinkType = 'work_item' | 'pull_request' | 'commit' | 'ci_run';

export interface ReleaseLink {
  id: string;
  release_id: string;
  link_type: LinkType;
  url: string;
  label?: string | null;
  created_by: string;
  created_at: string;
}

export type EvidenceType =
  | 'change_summary'
  | 'test_results'
  | 'uat_signoff'
  | 'rollback_plan'
  | 'security_review'
  | 'other';

export interface Evidence {
  id: string;
  release_id: string;
  type: EvidenceType;
  title: string;
  notes: string;
  attachment_url?: string | null;
  submitted_by: string;
  submitted_at: string;
}

export type Decision = 'approved' | 'rejected';

export interface Approval {
  id: string;
  release_id: string;
  approver_id: string;
  decision: Decision;
  comment: string;
  decided_at: string;
}

export type Environment = 'development' | 'uat' | 'staging' | 'production';
export type DeploymentResult = 'success' | 'failure';

export interface Deployment {
  id: string;
  release_id: string;
  environment: Environment;
  artifact_digest: string;
  artifact_ref?: string | null;
  commit_sha?: string | null;
  workflow_run_url: string;
  result: DeploymentResult;
  recorded_via: 'ci' | 'web';
  occurred_at: string;
  created_at: string;
}

export type SupportSeverity = 'low' | 'medium' | 'high' | 'critical';
export type SupportStatus = 'open' | 'investigating' | 'resolved' | 'closed';

export interface SupportItem {
  id: string;
  release_id: string;
  summary: string;
  severity: SupportSeverity;
  status: SupportStatus;
  owner_id?: string | null;
  linked_ticket_url?: string | null;
  linked_pr_url?: string | null;
  resolution_note: string;
  source: 'web' | 'mcp';
  created_by: string;
  created_at: string;
  updated_at: string;
  closed_at?: string | null;
}

export interface ChangeNote {
  id: string;
  release_id: string;
  author_id: string;
  summary: string;
  rationale: string;
  affected_service: string;
  tests_run: string;
  tests_status: 'passed' | 'failed' | 'not_run';
  linked_pr_url?: string | null;
  linked_commit_sha?: string | null;
  source: 'web' | 'mcp';
  created_at: string;
}

export interface DesignDecision {
  id: string;
  release_id: string;
  author_id: string;
  decision: string;
  rationale: string;
  alternatives_considered: string;
  impact: string;
  linked_pr_url?: string | null;
  linked_commit_sha?: string | null;
  source: 'web' | 'mcp';
  created_at: string;
}

export type SubmissionType = 'change_note' | 'design_decision' | 'support_note';
export type SubmissionStatus = 'pending' | 'accepted' | 'rejected';

export interface McpSubmission {
  id: string;
  release_id: string;
  type: SubmissionType;
  payload: Record<string, unknown>;
  submitted_by: string;
  status: SubmissionStatus;
  reviewed_by?: string;
  reviewed_at?: string;
  review_note: string;
  created_at: string;
}

export interface EligibilityResult {
  eligible: boolean;
  blockers: string[];
  stage: string;
  required_evidence_types: string[];
  missing_evidence_types: string[] | null;
  required_approvals_count: number;
  approved_count: number;
  most_recent_decision_is_rejection: boolean;
}

export interface AuditEntry {
  id: string;
  entity_type: string;
  entity_id: string;
  action: string;
  actor_id?: string;
  actor_label: string;
  previous_state?: unknown;
  new_state?: unknown;
  remarks: string;
  created_at: string;
}

export interface ReleaseDetail {
  release: Release;
  links: ReleaseLink[];
  evidence: Evidence[];
  change_notes: ChangeNote[];
  design_decisions: DesignDecision[];
  approvals: Approval[];
  deployments: Deployment[];
  support_items: SupportItem[];
  pending_mcp_submissions: McpSubmission[];
  eligibility: EligibilityResult;
  activity: AuditEntry[];
}

export interface DashboardReleaseRow extends Release {
  project_name: string;
  reason?: string;
}

export interface Dashboard {
  needs_my_action: DashboardReleaseRow[];
  blocked: DashboardReleaseRow[];
  upcoming_production: DashboardReleaseRow[];
  recent_deployments: Deployment[];
}

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    details?: unknown;
  };
}
