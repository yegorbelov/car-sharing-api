package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS app_users (
	id BIGSERIAL PRIMARY KEY,
	email TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	full_name TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS app_users_email_lower_idx ON app_users (lower(email));

ALTER TABLE app_users ADD COLUMN IF NOT EXISTS balance_cents BIGINT NOT NULL DEFAULT 500000;
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS avatar_url TEXT NOT NULL DEFAULT '';

ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS owner_user_id BIGINT REFERENCES app_users (id);
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS photo_url TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS rental_deals (
	id BIGSERIAL PRIMARY KEY,
	vehicle_id INTEGER NOT NULL REFERENCES vehicles (id),
	renter_id BIGINT NOT NULL REFERENCES app_users (id),
	owner_id BIGINT NOT NULL REFERENCES app_users (id),
	status TEXT NOT NULL,
	hold_amount_cents BIGINT NOT NULL,
	day_count INTEGER NOT NULL DEFAULT 3,
	start_date DATE NOT NULL,
	end_date DATE NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rental_deals_renter_idx ON rental_deals (renter_id);
CREATE INDEX IF NOT EXISTS rental_deals_owner_idx ON rental_deals (owner_id);
CREATE INDEX IF NOT EXISTS rental_deals_vehicle_idx ON rental_deals (vehicle_id);

CREATE TABLE IF NOT EXISTS deal_messages (
	id BIGSERIAL PRIMARY KEY,
	deal_id BIGINT NOT NULL REFERENCES rental_deals (id) ON DELETE CASCADE,
	sender_id BIGINT NOT NULL REFERENCES app_users (id),
	body TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS deal_messages_deal_idx ON deal_messages (deal_id, created_at);

CREATE TABLE IF NOT EXISTS wallet_ledger (
	id BIGSERIAL PRIMARY KEY,
	user_id BIGINT NOT NULL REFERENCES app_users (id),
	deal_id BIGINT REFERENCES rental_deals (id),
	delta_cents BIGINT NOT NULL,
	entry_type TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS wallet_ledger_user_idx ON wallet_ledger (user_id, id DESC);

UPDATE vehicles v
SET owner_user_id = (SELECT id FROM app_users ORDER BY id ASC LIMIT 1)
WHERE owner_user_id IS NULL
  AND EXISTS (SELECT 1 FROM app_users LIMIT 1);
`)
	return err
}
