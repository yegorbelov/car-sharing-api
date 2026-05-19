package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	demoSeedPassword = "Demo1234"
	demoMarkerEmail  = "anna.owner@carsharing.demo"
)

type demoUserSpec struct {
	email, fullName string
	balanceCents    int64
	isAdmin         bool
	isModerator     bool
	isArbitrator    bool
}

type demoVehicleSpec struct {
	title, city, class string
	priceCents         int32
	rating             float64
	reviewCount        int32
	ownerEmail         string
	mileageKm          int32
	modelYear          int32
	transmission       string
	fuelType           string
	drivetrain         string
	engineCC           int32
	exteriorColor      string
	conditionSummary   string
	techNotes          string
	vin                string
}

// demoVehicleGallery — stable public URLs so the app can swipe a multi-photo gallery.
var demoVehicleGallery = []string{
	"https://images.unsplash.com/photo-1494976388531-d1058494cdd8?auto=format&fit=crop&w=800&q=80",
	"https://images.unsplash.com/photo-1503376780353-7e6692767b70?auto=format&fit=crop&w=800&q=80",
	"https://images.unsplash.com/photo-1492144534655-ae79c964c9d7?auto=format&fit=crop&w=800&q=80",
}

// Staff accounts for RBAC demos; inserted on startup if missing (existing DBs skip full reseed).
var demoStaffUsers = []demoUserSpec{
	{email: "admin@carsharing.demo", fullName: "Platform Admin", balanceCents: 500_000, isAdmin: true},
	{email: "moderator@carsharing.demo", fullName: "Listing Moderator", balanceCents: 500_000, isModerator: true},
	{email: "arbitrator@carsharing.demo", fullName: "Dispute Arbitrator", balanceCents: 500_000, isArbitrator: true},
}

func ensureStaffDemoUsers(ctx context.Context, pool *pgxpool.Pool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(demoSeedPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("demo staff password hash: %w", err)
	}
	pw := string(hash)

	for _, u := range demoStaffUsers {
		var id int64
		err := pool.QueryRow(ctx, `
			SELECT id FROM app_users WHERE lower(email) = lower($1)
		`, u.email).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = pool.Exec(ctx, `
				INSERT INTO app_users (email, password_hash, full_name, balance_cents, is_admin, is_moderator, is_arbitrator)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, u.email, pw, u.fullName, u.balanceCents, u.isAdmin, u.isModerator, u.isArbitrator)
			if err != nil {
				return fmt.Errorf("insert staff user %s: %w", u.email, err)
			}
			log.Printf("demo staff user created: %s / %s", u.email, demoSeedPassword)
			continue
		}
		if err != nil {
			return err
		}
		_, err = pool.Exec(ctx, `
			UPDATE app_users
			SET is_admin = $1, is_moderator = $2, is_arbitrator = $3
			WHERE id = $4
		`, u.isAdmin, u.isModerator, u.isArbitrator, id)
		if err != nil {
			return fmt.Errorf("update staff roles %s: %w", u.email, err)
		}
	}
	return nil
}

func hasLegacyInitVehicles(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM vehicles
		WHERE model_year = 0 AND COALESCE(transmission, '') = ''
	`).Scan(&n)
	return n > 0, err
}

func ensureDevSeed(ctx context.Context, pool *pgxpool.Pool) error {
	if err := ensureStaffDemoUsers(ctx, pool); err != nil {
		return err
	}

	var markerExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM app_users WHERE lower(email) = lower($1))
	`, demoMarkerEmail).Scan(&markerExists); err != nil {
		return err
	}
	if markerExists {
		if err := repairOrphanVehicles(ctx, pool); err != nil {
			return err
		}
		if err := backfillDemoVehicleGalleries(ctx, pool); err != nil {
			return err
		}
		return backfillVehicleReviews(ctx, pool)
	}

	legacy, err := hasLegacyInitVehicles(ctx, pool)
	if err != nil {
		return err
	}

	var userCount, vehicleCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM app_users`).Scan(&userCount); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM vehicles`).Scan(&vehicleCount); err != nil {
		return err
	}

	if userCount == 0 || legacy || vehicleCount == 0 {
		if err := insertFullDemoData(ctx, pool); err != nil {
			return err
		}
		log.Printf("demo seed loaded — log in with %s / %s (and other *@carsharing.demo users)", demoMarkerEmail, demoSeedPassword)
		return nil
	}

	if err := backfillVehicleReviews(ctx, pool); err != nil {
		return err
	}
	return repairOrphanVehicles(ctx, pool)
}

func backfillVehicleReviews(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT id, rating FROM vehicles v
		WHERE NOT EXISTS (SELECT 1 FROM vehicle_reviews r WHERE r.vehicle_id = v.id)
		ORDER BY id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		id     int32
		rating float64
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.rating); err != nil {
			return err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pending) == 0 {
		return syncVehicleReviewStats(ctx, pool)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, v := range pending {
		if err := seedVehicleReviews(ctx, tx, v.id, v.rating); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	log.Printf("backfilled vehicle reviews for %d listing(s)", len(pending))
	return syncVehicleReviewStats(ctx, pool)
}

func syncVehicleReviewStats(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
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
	`)
	return err
}

func repairOrphanVehicles(ctx context.Context, pool *pgxpool.Pool) error {
	var ownerID int64
	err := pool.QueryRow(ctx, `
		SELECT id FROM app_users WHERE lower(email) = lower($1)
	`, demoMarkerEmail).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE vehicles
		SET owner_user_id = $1
		WHERE owner_user_id IS NULL
	`, ownerID)
	return err
}

func backfillDemoVehicleGalleries(ctx context.Context, pool *pgxpool.Pool) error {
	jsonStr, err := marshalVehiclePhotoURLs(demoVehicleGallery)
	if err != nil {
		return err
	}
	cover := primaryVehiclePhoto(demoVehicleGallery)
	_, err = pool.Exec(ctx, `
		UPDATE vehicles
		SET photo_url = $1, photo_urls = $2
		WHERE COALESCE(trim(photo_url), '') = ''
		  AND (photo_urls IS NULL OR trim(photo_urls) = '' OR photo_urls = '[]')
	`, cover, jsonStr)
	return err
}

func insertFullDemoData(ctx context.Context, pool *pgxpool.Pool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(demoSeedPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("demo password hash: %w", err)
	}
	pw := string(hash)

	users := []demoUserSpec{
		{email: "anna.owner@carsharing.demo", fullName: "Anna Petrova", balanceCents: 480_000},
		{email: "dmitry.owner@carsharing.demo", fullName: "Dmitry Sokolov", balanceCents: 620_000},
		{email: "elena.owner@carsharing.demo", fullName: "Elena Volkova", balanceCents: 510_000},
		{email: "ivan.renter@carsharing.demo", fullName: "Ivan Smirnov", balanceCents: 500_000},
		{email: "maria.renter@carsharing.demo", fullName: "Maria Kozlova", balanceCents: 500_000},
	}
	users = append(users, demoStaffUsers...)

	vehicles := []demoVehicleSpec{
		{
			title: "Toyota Camry", city: "Moscow", class: "sedan", priceCents: 8000, rating: 4.8, reviewCount: 124,
			ownerEmail: "anna.owner@carsharing.demo", mileageKm: 42_000, modelYear: 2021,
			transmission: "automatic", fuelType: "petrol", drivetrain: "fwd", engineCC: 2500,
			exteriorColor: "Silver", conditionSummary: "Well maintained, one owner, full service history.",
			techNotes: "Bluetooth, rear camera, cruise control.", vin: "JTDBR32E720123456",
		},
		{
			title: "Mercedes-Benz E-Class", city: "Moscow", class: "business", priceCents: 18300, rating: 4.7, reviewCount: 89,
			ownerEmail: "elena.owner@carsharing.demo", mileageKm: 28_500, modelYear: 2022,
			transmission: "automatic", fuelType: "diesel", drivetrain: "rwd", engineCC: 2000,
			exteriorColor: "Black", conditionSummary: "Executive trim, leather interior, non-smoker.",
			techNotes: "Panoramic roof, heated seats, driver assist package.", vin: "W1KZF8DB5NA123789",
		},
		{
			title: "BMW X3", city: "Kazan", class: "suv", priceCents: 14000, rating: 4.9, reviewCount: 156,
			ownerEmail: "anna.owner@carsharing.demo", mileageKm: 19_800, modelYear: 2023,
			transmission: "automatic", fuelType: "petrol", drivetrain: "awd", engineCC: 2000,
			exteriorColor: "Blue", conditionSummary: "Like new, dealer serviced, winter tires included.",
			techNotes: "Navigation, parking sensors, Apple CarPlay.", vin: "5UXCR6C05P9K12345",
		},
		{
			title: "Kia Rio", city: "Saint Petersburg", class: "economy", priceCents: 6000, rating: 4.5, reviewCount: 67,
			ownerEmail: "dmitry.owner@carsharing.demo", mileageKm: 67_200, modelYear: 2020,
			transmission: "manual", fuelType: "petrol", drivetrain: "fwd", engineCC: 1400,
			exteriorColor: "White", conditionSummary: "Reliable city car, ideal for short trips.",
			techNotes: "A/C, USB, economical fuel consumption.", vin: "KNADC2435L6123456",
		},
		{
			title: "Lada Vesta", city: "Nizhny Novgorod", class: "comfort", priceCents: 5000, rating: 4.3, reviewCount: 41,
			ownerEmail: "dmitry.owner@carsharing.demo", mileageKm: 88_000, modelYear: 2019,
			transmission: "manual", fuelType: "petrol", drivetrain: "fwd", engineCC: 1600,
			exteriorColor: "Red", conditionSummary: "Spacious trunk, recently replaced brakes.",
			techNotes: "Heated mirrors, all-season tires.", vin: "XTA219040L0123456",
		},
		{
			title: "Hyundai Tucson", city: "Moscow", class: "suv", priceCents: 12000, rating: 4.6, reviewCount: 98,
			ownerEmail: "elena.owner@carsharing.demo", mileageKm: 35_400, modelYear: 2022,
			transmission: "automatic", fuelType: "hybrid", drivetrain: "awd", engineCC: 1600,
			exteriorColor: "Graphite", conditionSummary: "Family SUV, pet-free, garage kept.",
			techNotes: "Wireless charging, lane keep, blind spot monitor.", vin: "KM8J3CA24NU123456",
		},
		{
			title: "Volkswagen Polo", city: "Saint Petersburg", class: "economy", priceCents: 5500, rating: 4.4, reviewCount: 53,
			ownerEmail: "anna.owner@carsharing.demo", mileageKm: 54_100, modelYear: 2021,
			transmission: "automatic", fuelType: "petrol", drivetrain: "fwd", engineCC: 1100,
			exteriorColor: "Gray", conditionSummary: "Compact and easy to park, great for tourists.",
			techNotes: "ISOFIX mounts, DAB radio.", vin: "WVWZZZ6RZMY123456",
		},
		{
			title: "Tesla Model 3", city: "Kazan", class: "business", priceCents: 16500, rating: 4.95, reviewCount: 203,
			ownerEmail: "elena.owner@carsharing.demo", mileageKm: 12_300, modelYear: 2023,
			transmission: "automatic", fuelType: "electric", drivetrain: "rwd", engineCC: 0,
			exteriorColor: "Pearl White", conditionSummary: "Long Range, supercharger access, autopilot enabled.",
			techNotes: "Glass roof, premium connectivity, mobile connector included.", vin: "5YJ3E1EA1PF123456",
		},
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Replace bare init.sql / placeholder listings with a full demo dataset.
	if _, err := tx.Exec(ctx, `DELETE FROM vehicle_reviews`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM deal_messages`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wallet_ledger`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM rental_deals`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM vehicles`); err != nil {
		return err
	}

	userIDs := make(map[string]int64, len(users))
	for _, u := range users {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO app_users (email, password_hash, full_name, balance_cents, is_admin, is_moderator, is_arbitrator)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id
		`, u.email, pw, u.fullName, u.balanceCents, u.isAdmin, u.isModerator, u.isArbitrator).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert user %s: %w", u.email, err)
		}
		userIDs[u.email] = id
	}

	galleryJSON, err := marshalVehiclePhotoURLs(demoVehicleGallery)
	if err != nil {
		return err
	}
	galleryCover := primaryVehiclePhoto(demoVehicleGallery)

	vehicleIDs := make(map[string]int32, len(vehicles))
	for _, v := range vehicles {
		ownerID := userIDs[v.ownerEmail]
		var id int32
		err := tx.QueryRow(ctx, `
			INSERT INTO vehicles (
				title, city, class, price_per_day_cents, rating, review_count, owner_user_id,
				photo_url, photo_urls,
				mileage_km, model_year, transmission, fuel_type, drivetrain,
				engine_cc, exterior_color, condition_summary, tech_notes, vin
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
			RETURNING id
		`, v.title, v.city, v.class, v.priceCents, v.rating, v.reviewCount, ownerID,
			galleryCover, galleryJSON,
			v.mileageKm, v.modelYear, v.transmission, v.fuelType, v.drivetrain,
			v.engineCC, v.exteriorColor, v.conditionSummary, v.techNotes, v.vin,
		).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert vehicle %s: %w", v.title, err)
		}
		vehicleIDs[v.title] = id
		if err := seedVehicleReviews(ctx, tx, id, v.rating); err != nil {
			return fmt.Errorf("seed reviews %s: %w", v.title, err)
		}
	}

	ivanID := userIDs["ivan.renter@carsharing.demo"]
	mariaID := userIDs["maria.renter@carsharing.demo"]
	annaID := userIDs["anna.owner@carsharing.demo"]
	dmitryID := userIDs["dmitry.owner@carsharing.demo"]

	camryID := vehicleIDs["Toyota Camry"]
	bmwID := vehicleIDs["BMW X3"]
	kiaID := vehicleIDs["Kia Rio"]

	holdCamry := computeHoldCents(3, 8000)
	holdBMW := computeHoldCents(4, 14000)
	holdKia := computeHoldCents(3, 6000)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	startPending := today.AddDate(0, 0, 2)
	endPending := startPending.AddDate(0, 0, 3)
	startActive := today
	endActive := today.AddDate(0, 0, 4)
	startDone := today.AddDate(0, 0, -14)
	endDone := startDone.AddDate(0, 0, 3)

	var pendingDealID, activeDealID, completedDealID int64

	if err := tx.QueryRow(ctx, `
		INSERT INTO rental_deals (
			vehicle_id, renter_id, owner_id, status, hold_amount_cents, day_count, start_date, end_date
		) VALUES ($1, $2, $3, $4, $5, 3, $6::date, $7::date)
		RETURNING id
	`, camryID, ivanID, annaID, dealPendingOwner, holdCamry, startPending, endPending).Scan(&pendingDealID); err != nil {
		return err
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO rental_deals (
			vehicle_id, renter_id, owner_id, status, hold_amount_cents, day_count, start_date, end_date
		) VALUES ($1, $2, $3, $4, $5, 4, $6::date, $7::date)
		RETURNING id
	`, bmwID, mariaID, annaID, dealActive, holdBMW, startActive, endActive).Scan(&activeDealID); err != nil {
		return err
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO rental_deals (
			vehicle_id, renter_id, owner_id, status, hold_amount_cents, day_count, start_date, end_date,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 3, $6::date, $7::date, NOW() - INTERVAL '20 days', NOW() - INTERVAL '5 days')
		RETURNING id
	`, kiaID, mariaID, dmitryID, dealCompleted, holdKia, startDone, endDone).Scan(&completedDealID); err != nil {
		return err
	}

	applyHold := func(userID, dealID, hold int64, note string) error {
		_, err := tx.Exec(ctx, `UPDATE app_users SET balance_cents = balance_cents - $1 WHERE id = $2`, hold, userID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
			VALUES ($1, $2, $3, 'hold', $4)
		`, userID, dealID, -hold, note)
		return err
	}

	if err := applyHold(ivanID, pendingDealID, holdCamry, "Security hold for rental request #"+fmt.Sprint(pendingDealID)); err != nil {
		return err
	}
	if err := applyHold(mariaID, activeDealID, holdBMW, "Security hold for rental request #"+fmt.Sprint(activeDealID)); err != nil {
		return err
	}
	if err := applyHold(mariaID, completedDealID, holdKia, "Security hold for rental request #"+fmt.Sprint(completedDealID)); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `UPDATE app_users SET balance_cents = balance_cents + $1 WHERE id = $2`, holdKia, dmitryID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_ledger (user_id, deal_id, delta_cents, entry_type, note)
		VALUES ($1, $2, $3, 'payout_owner', $4)
	`, dmitryID, completedDealID, holdKia, "Rental payout for deal #"+fmt.Sprint(completedDealID))
	if err != nil {
		return err
	}

	messages := []struct {
		dealID, senderID int64
		body             string
	}{
		{activeDealID, mariaID, "Hi! Can I pick up the BMW tomorrow morning around 10?"},
		{activeDealID, annaID, "Sure, the keys will be in the lockbox. I'll send the code after you confirm."},
		{activeDealID, mariaID, "Perfect, thanks! I'll take good care of it."},
		{pendingDealID, ivanID, "Planning a weekend trip to Tver — is winter tire kit included?"},
		{pendingDealID, annaID, "Yes, winter tires are already mounted. Full tank on handoff."},
	}
	for _, m := range messages {
		_, err := tx.Exec(ctx, `
			INSERT INTO deal_messages (deal_id, sender_id, body)
			VALUES ($1, $2, $3)
		`, m.dealID, m.senderID, m.body)
		if err != nil {
			return err
		}
	}

	// Align balances with holds and completed payout.
	_, err = tx.Exec(ctx, `UPDATE app_users SET balance_cents = $1 WHERE id = $2`, 500_000-holdCamry, ivanID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE app_users SET balance_cents = $1 WHERE id = $2`, 500_000-holdBMW-holdKia, mariaID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE app_users SET balance_cents = $1 WHERE id = $2`, 620_000+holdKia, dmitryID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
