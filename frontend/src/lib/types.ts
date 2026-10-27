export type Role = "admin" | "staff";

export interface User {
  id: number;
  email: string;
  name: string;
  role: Role;
}

export interface UserRow extends User {
  active: boolean;
  last_login_at: string | null;
  created_at: string;
}

export interface GroupRef {
  id: number;
  name: string;
}

export interface Contact {
  id: number;
  phone: string;
  name: string;
  email: string;
  tags: string[];
  opted_out: boolean;
  opted_out_at: string | null;
  opt_out_source: string;
  groups: GroupRef[] | null;
  created_at: string;
}

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface Group {
  id: number;
  name: string;
  description: string;
  contacts: number;
  opted_out: number;
  created_at: string;
}

export interface TagCount {
  tag: string;
  count: number;
}

export interface ImportIssue {
  row: number;
  phone: string;
  name: string;
  status: "invalid" | "duplicate";
  reason: string;
}

export interface ImportResult {
  id: number;
  filename: string;
  total: number;
  created: number;
  updated: number;
  duplicates: number;
  invalid: number;
  columns?: Record<string, string>;
  issues?: ImportIssue[];
  created_at: string;
}

export interface TemplateParam {
  name?: string;
  example: string;
}

export interface TemplateButton {
  type: string;
  text: string;
  url?: string;
  params?: TemplateParam[];
  tracked: boolean;
}

export interface TemplateSpec {
  header_format: string;
  header_text?: string;
  header_params?: TemplateParam[];
  header_example_url?: string;
  body: string;
  body_params?: TemplateParam[];
  footer?: string;
  buttons?: TemplateButton[];
}

export interface Template {
  slug: string;
  name: string;
  category: string;
  status: string;
  default_language: string;
  available_languages: string[];
  languages: Record<string, { status: string }>;
  spec: TemplateSpec;
  sendable: boolean;
  synced_at: string;
}

export interface Variables {
  header_media_url: string;
  header_text: string[];
  body: string[];
  buttons: string[][];
  shop_url: string;
}

export interface Audience {
  all: boolean;
  group_ids: number[];
  tags: string[];
}

export type CampaignStatus = "draft" | "scheduled" | "sending" | "paused" | "completed" | "cancelled";

export interface Campaign {
  id: number;
  name: string;
  template_slug: string;
  template_language: string;
  variables: Variables;
  audience: Audience;
  status: CampaignStatus;
  status_reason: string;
  scheduled_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  rate_per_message: number;
  created_at: string;
  updated_at: string;
}

export interface Stats {
  recipients: number;
  queued: number;
  accepted: number;
  sent: number;
  delivered: number;
  read: number;
  failed: number;
  skipped: number;
  cancelled: number;
  unknown: number;
  clicked: number;
  clicks: number;
  replied: number;
  actual_cost: number;
  cost_currency: string;
  priced_count: number;
}

export interface CampaignWithStats extends Campaign {
  stats: Stats;
}

export interface Estimate {
  eligible: number;
  opted_out_excluded: number;
  rate: number;
  currency: string;
  estimated_cost: number;
  send_minutes: number;
}

export interface CampaignDetail {
  campaign: Campaign;
  stats: Stats;
  template?: Template;
  currency: string;
  estimate?: Estimate;
  estimated_cost?: number;
  failure_reasons?: { reason: string; count: number }[];
}

export interface Recipient {
  id: number;
  contact_id: number | null;
  phone: string;
  name: string;
  status: string;
  attempts: number;
  error_code: string;
  error_reason: string;
  sent_at: string | null;
  delivered_at: string | null;
  read_at: string | null;
  failed_at: string | null;
  clicked_at: string | null;
  click_count: number;
  replied_at: string | null;
  cost: number | null;
  message_id: string | null;
}

export interface Reply {
  id: number;
  phone: string;
  name: string;
  text: string;
  is_opt_out: boolean;
  received_at: string;
}

export interface Settings {
  marketing_rate: number;
  currency: string;
  send_rate_per_minute: number;
  default_region: string;
  bird_from: string;
  bird_waba_id: string;
  bird_dashboard_url: string;
  stop_keywords: string[];
  name_fallback: string;
}

export interface SettingsInfo {
  webhook_url: string;
  click_url_pattern: string;
  bird_api_host: string;
  bird_configured: boolean;
  webhook_secret_configured: boolean;
  smtp_configured: boolean;
  webhook_events: string[];
}

export interface Dashboard {
  contacts: { total: number; opted_out: number; groups: number };
  all_time: Stats;
  last_30_days: Stats;
  replies_30_days: number;
  campaigns_by_status: Record<string, number>;
  recent_campaigns: CampaignWithStats[];
  currency: string;
  bird_configured: boolean;
}
