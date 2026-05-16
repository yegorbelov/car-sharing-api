CREATE TABLE IF NOT EXISTS vehicles (
    id SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    city TEXT NOT NULL,
    class TEXT NOT NULL,
    price_per_day_cents INTEGER NOT NULL,
    rating REAL NOT NULL
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

INSERT INTO vehicles (title, city, class, price_per_day_cents, rating)
VALUES
    ('Toyota Camry', 'Moscow', 'sedan', 8000, 4.8),
    ('Kia Rio', 'Saint Petersburg', 'economy', 6000, 4.5),
    ('BMW X3', 'Kazan', 'suv', 14000, 4.9),
    ('Lada Vesta', 'Nizhny Novgorod', 'comfort', 5000, 4.3),
    ('Mercedes-Benz E-Class', 'Moscow', 'business', 18300, 4.7);
