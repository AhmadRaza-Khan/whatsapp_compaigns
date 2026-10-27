CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    email         text NOT NULL UNIQUE,
    name          text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    role          text NOT NULL CHECK (role IN ('admin', 'staff')),
    active        boolean NOT NULL DEFAULT true,
    last_login_at timestamptz,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE password_resets (
    id         bigserial PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE contacts (
    id             bigserial PRIMARY KEY,
    phone          text NOT NULL UNIQUE,          -- E.164, e.g. +97455123456
    name           text NOT NULL DEFAULT '',
    email          text NOT NULL DEFAULT '',
    tags           text[] NOT NULL DEFAULT '{}',
    opted_out      boolean NOT NULL DEFAULT false,
    opted_out_at   timestamptz,
    opt_out_source text NOT NULL DEFAULT '',      -- keyword | bird | manual
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contacts_tags_idx ON contacts USING gin (tags);
CREATE INDEX contacts_name_idx ON contacts (lower(name));

CREATE TABLE groups (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE contact_groups (
    contact_id bigint NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    group_id   bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (contact_id, group_id)
);
CREATE INDEX contact_groups_group_idx ON contact_groups (group_id);

CREATE TABLE import_batches (
    id         bigserial PRIMARY KEY,
    filename   text NOT NULL,
    user_id    bigint REFERENCES users(id) ON DELETE SET NULL,
    total      int NOT NULL DEFAULT 0,
    created    int NOT NULL DEFAULT 0,
    updated    int NOT NULL DEFAULT 0,
    duplicates int NOT NULL DEFAULT 0,
    invalid    int NOT NULL DEFAULT 0,
    report     jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE templates (
    id               bigserial PRIMARY KEY,
    bird_id          text NOT NULL DEFAULT '',
    slug             text NOT NULL UNIQUE,
    name             text NOT NULL DEFAULT '',
    category         text NOT NULL DEFAULT '',
    status           text NOT NULL DEFAULT '',     -- draft|pending|active|rejected|inactive
    scope            text NOT NULL DEFAULT '',
    default_language text NOT NULL DEFAULT '',
    languages        jsonb NOT NULL DEFAULT '{}',   -- per-language status from Bird
    available_languages text[] NOT NULL DEFAULT '{}',
    live_version_id  text NOT NULL DEFAULT '',
    components       jsonb NOT NULL DEFAULT '[]',   -- content of the default/live language
    raw              jsonb NOT NULL DEFAULT '{}',
    synced_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE campaigns (
    id                bigserial PRIMARY KEY,
    name              text NOT NULL,
    template_slug     text NOT NULL,
    template_language text NOT NULL DEFAULT '',
    variables         jsonb NOT NULL DEFAULT '{}',
    audience          jsonb NOT NULL DEFAULT '{}',
    status            text NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft','scheduled','sending','paused','completed','cancelled')),
    status_reason     text NOT NULL DEFAULT '',
    scheduled_at      timestamptz,
    started_at        timestamptz,
    completed_at      timestamptz,
    rate_per_message  numeric(12,5) NOT NULL DEFAULT 0, -- snapshot of the cost setting at launch
    created_by        bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX campaigns_status_idx ON campaigns (status, scheduled_at);

-- One row per contact per campaign. The UNIQUE constraint plus the claim/state
-- machine in the worker is what guarantees a contact is sent a campaign once.
CREATE TABLE campaign_recipients (
    id              bigserial PRIMARY KEY,
    campaign_id     bigint NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id      bigint REFERENCES contacts(id) ON DELETE SET NULL,
    phone           text NOT NULL,
    contact_name    text NOT NULL DEFAULT '',
    token           text NOT NULL UNIQUE,      -- click-tracking token
    status          text NOT NULL DEFAULT 'pending',
        -- pending|sending|accepted|sent|delivered|read|failed|skipped|cancelled|unknown
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_at       timestamptz,
    bird_message_id text UNIQUE,
    error_code      text NOT NULL DEFAULT '',
    error_reason    text NOT NULL DEFAULT '',
    accepted_at     timestamptz,
    sent_at         timestamptz,
    delivered_at    timestamptz,
    read_at         timestamptz,
    failed_at       timestamptz,
    clicked_at      timestamptz,
    click_count     int NOT NULL DEFAULT 0,
    replied_at      timestamptz,
    cost            numeric(12,5),
    cost_currency   text NOT NULL DEFAULT '',
    cost_checks     int NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, contact_id)
);
CREATE INDEX campaign_recipients_queue_idx ON campaign_recipients (status, next_attempt_at);
CREATE INDEX campaign_recipients_campaign_idx ON campaign_recipients (campaign_id, status);
CREATE INDEX campaign_recipients_contact_idx ON campaign_recipients (contact_id, created_at DESC);

CREATE TABLE clicks (
    id           bigserial PRIMARY KEY,
    recipient_id bigint NOT NULL REFERENCES campaign_recipients(id) ON DELETE CASCADE,
    campaign_id  bigint NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_events (
    id           bigserial PRIMARY KEY,
    webhook_id   text NOT NULL UNIQUE,   -- Bird's webhook-id header, used for dedupe
    event_type   text NOT NULL DEFAULT '',
    payload      jsonb NOT NULL,
    received_at  timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    attempts     int NOT NULL DEFAULT 0,
    error        text NOT NULL DEFAULT ''
);
CREATE INDEX webhook_events_pending_idx ON webhook_events (id) WHERE processed_at IS NULL;

CREATE TABLE inbound_messages (
    id              bigserial PRIMARY KEY,
    bird_message_id text UNIQUE,
    contact_id      bigint REFERENCES contacts(id) ON DELETE SET NULL,
    campaign_id     bigint REFERENCES campaigns(id) ON DELETE SET NULL,
    phone           text NOT NULL DEFAULT '',
    text            text NOT NULL DEFAULT '',
    is_opt_out      boolean NOT NULL DEFAULT false,
    received_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inbound_messages_campaign_idx ON inbound_messages (campaign_id, received_at DESC);
