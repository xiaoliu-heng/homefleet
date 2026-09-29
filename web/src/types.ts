export type Capability = { available: boolean; reason?: string };
export type GPU = {
  id: string;
  name: string;
  vendor: string;
  source: string;
  utilization: number | null;
  memory_used: number | null;
  memory_total: number | null;
  temperature: number | null;
  unified_memory: boolean;
  memory_note?: string;
  error?: string;
};
export type Sample = {
  at: string;
  cpu: number | null;
  memory_percent: number | null;
  memory_total: number;
  memory_available: number;
  disks: {
    mount: string;
    device: string;
    filesystem: string;
    total: number;
    free: number;
    used_percent: number;
  }[];
  gpus: GPU[];
  errors: Record<string, string>;
};
export type Device = {
  id: string;
  name: string;
  group: string;
  kind: string;
  os: string;
  platform: string;
  arch: string;
  hostname: string;
  agent_version: string;
  run_user: string;
  addresses: { interface: string; address: string }[];
  capabilities: Record<string, Capability>;
  last_seen: string;
  online: boolean;
  revoked: boolean;
  management_url?: string;
  probe?: {
    type: string;
    target: string;
    last_error?: string;
    latency_ms: number;
    checked_at: string;
  };
  latest?: Sample;
};
export type Package = {
  id: string;
  name: string;
  version: string;
  available_version?: string;
  manager: string;
  scope?: string;
};
export type Inventory = { packages: Package[]; at: string; error?: string };
export type Project = {
  id?: string;
  name: string;
  version?: number;
  repository: string;
  ref: string;
  directory: string;
  directories: Record<string, string>;
  platforms: Record<string, { steps: string[]; health_check: string }>;
  env: Record<string, string>;
  secret_env?: Record<string, string>;
  secret_keys?: string[];
  compose_file: string;
  updated_at?: string;
};
export type Action = {
  agent_version?: string;
  kind: string;
  operation: string;
  package?: string;
  catalog_id?: string;
  scope?: string;
  project_id?: string;
  project?: Project;
};
export type Step = {
  name: string;
  program: string;
  args: string[];
  script?: string;
  directory?: string;
  identity: string;
  health: boolean;
  package_transaction: boolean;
};
export type Plan = {
  agent_update?: { from_version: string; version: string; artifact: AgentArtifact };
  steps: Step[];
  warnings: string[];
  resolved_commit?: string;
  package?: string;
  expires_at: string;
};
export type Target = {
  id: string;
  job_id: string;
  device_id: string;
  device_name: string;
  state: string;
  reason?: string;
  plan?: Plan;
  result?: {
    agent_version?: string;
    state: string;
    reason?: string;
    health?: string;
    commit?: string;
    exit_code: number;
  };
  cancel_requested: boolean;
};
export type Job = {
  id: string;
  mode: string;
  action: Action;
  created_at: string;
  source_id?: string;
  status: string;
  targets: Target[];
};
export type Log = { seq: number; at: string; stream: string; text: string };
export type AgentArtifact = { name: string; sha256: string; size: number };
export type AgentRelease = { version: string; minimum_version: string; published_at: string; artifacts: Record<string, AgentArtifact> };
export type Catalog = {
  id: string;
  name: string;
  description: string;
  packages: Record<string, string>;
};
