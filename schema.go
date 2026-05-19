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
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS is_moderator BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE app_users ADD COLUMN IF NOT EXISTS is_arbitrator BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS owner_user_id BIGINT REFERENCES app_users (id);
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS photo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS photo_urls TEXT NOT NULL DEFAULT '[]';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS mileage_km INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS model_year INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS transmission TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS fuel_type TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS drivetrain TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS engine_cc INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS exterior_color TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS condition_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS tech_notes TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS vin TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS review_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS listing_status TEXT NOT NULL DEFAULT 'published';
UPDATE vehicles SET listing_status = 'published' WHERE listing_status IS NULL OR trim(listing_status) = '';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS min_rental_days INTEGER NOT NULL DEFAULT 1;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS max_rental_days INTEGER NOT NULL DEFAULT 14;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS seat_count INTEGER NOT NULL DEFAULT 5;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS pets_allowed BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS fuel_return_policy TEXT NOT NULL DEFAULT 'same_level';
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS moderation_note TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS staff_audit_log (
	id BIGSERIAL PRIMARY KEY,
	actor_user_id BIGINT NOT NULL REFERENCES app_users (id),
	action TEXT NOT NULL,
	entity_type TEXT NOT NULL,
	entity_id BIGINT NOT NULL,
	details JSONB NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS staff_audit_log_created_at_idx ON staff_audit_log (created_at DESC);

CREATE TABLE IF NOT EXISTS rental_disputes (
	id BIGSERIAL PRIMARY KEY,
	deal_id BIGINT NOT NULL UNIQUE REFERENCES rental_deals (id) ON DELETE CASCADE,
	opened_by_user_id BIGINT NOT NULL REFERENCES app_users (id),
	reason_code TEXT NOT NULL,
	description TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'open',
	resolution_code TEXT NOT NULL DEFAULT '',
	resolution_note TEXT NOT NULL DEFAULT '',
	renter_refund_cents BIGINT NOT NULL DEFAULT 0,
	owner_payout_cents BIGINT NOT NULL DEFAULT 0,
	arbitrator_user_id BIGINT REFERENCES app_users (id),
	resolved_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS rental_disputes_status_idx ON rental_disputes (status, created_at);

CREATE TABLE IF NOT EXISTS dispute_evidence (
	id BIGSERIAL PRIMARY KEY,
	dispute_id BIGINT NOT NULL REFERENCES rental_disputes (id) ON DELETE CASCADE,
	uploaded_by_user_id BIGINT NOT NULL REFERENCES app_users (id),
	attachment_url TEXT NOT NULL DEFAULT '',
	attachment_type TEXT NOT NULL DEFAULT 'image',
	caption TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS dispute_evidence_dispute_idx ON dispute_evidence (dispute_id);

CREATE TABLE IF NOT EXISTS vehicle_reviews (
	id BIGSERIAL PRIMARY KEY,
	vehicle_id INTEGER NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
	author_name TEXT NOT NULL,
	rating REAL NOT NULL CHECK (rating >= 1 AND rating <= 5),
	body TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS vehicle_reviews_vehicle_idx ON vehicle_reviews (vehicle_id, created_at DESC);

UPDATE vehicles v
SET review_count = COALESCE(r.cnt, 0),
    rating = COALESCE(r.avg, v.rating)
FROM (
	SELECT vehicle_id, COUNT(*)::int AS cnt, AVG(vr.rating)::real AS avg
	FROM vehicle_reviews vr
	GROUP BY vehicle_id
) r
WHERE v.id = r.vehicle_id;

UPDATE vehicles v
SET review_count = 0
WHERE NOT EXISTS (SELECT 1 FROM vehicle_reviews r WHERE r.vehicle_id = v.id)
  AND v.review_count <> 0;

UPDATE vehicles
SET photo_urls = to_json(ARRAY[photo_url::text])::text
WHERE COALESCE(trim(photo_url), '') <> '' AND (photo_urls IS NULL OR photo_urls = '' OR photo_urls = '[]');

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

ALTER TABLE deal_messages ADD COLUMN IF NOT EXISTS attachment_url TEXT NOT NULL DEFAULT '';
ALTER TABLE deal_messages ADD COLUMN IF NOT EXISTS attachment_type TEXT NOT NULL DEFAULT '';
ALTER TABLE deal_messages ADD COLUMN IF NOT EXISTS attachment_name TEXT NOT NULL DEFAULT '';
ALTER TABLE deal_messages ADD COLUMN IF NOT EXISTS reply_to_id BIGINT REFERENCES deal_messages (id) ON DELETE SET NULL;

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
