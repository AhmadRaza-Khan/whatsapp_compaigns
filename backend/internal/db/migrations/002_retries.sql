-- A recipient can be re-sent (e.g. after Meta 131049). Each re-send is a new
-- round with its own Bird idempotency key; earlier rounds' charges are kept.
ALTER TABLE campaign_recipients
    ADD COLUMN send_round int NOT NULL DEFAULT 0,
    ADD COLUMN prior_cost numeric(12,5) NOT NULL DEFAULT 0;
