CREATE TABLE IF NOT EXISTS vehicles (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    city TEXT NOT NULL,
    class TEXT NOT NULL,
    price_per_day_cents INTEGER NOT NULL,
    rating REAL NOT NULL,
    review_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS bookings (
    id SERIAL PRIMARY KEY,
    vehicle_id INTEGER REFERENCES vehicles (id),
    renter_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    start_date DATE,
    end_date DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Demo users, vehicles (with owners), deals, and wallet data are inserted on API startup (seed.go).
