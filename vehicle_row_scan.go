package main

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
)

const vehicleCompletedTripsSQL = `(
	SELECT COUNT(*)::int FROM rental_deals d
	WHERE d.vehicle_id = vehicles.id AND d.status = 'completed'
)`

const vehicleListSelectSQL = `
	id, title, city, class, price_per_day_cents, rating, review_count,
	created_at::text, owner_user_id,
	photo_url, photo_urls, mileage_km, model_year, transmission, fuel_type, drivetrain,
	engine_cc, exterior_color, condition_summary, tech_notes, vin,
	latitude, longitude, listing_status,
	min_rental_days, max_rental_days, seat_count, pets_allowed, fuel_return_policy,
	moderation_note,
` + vehicleCompletedTripsSQL

func scanVehicleRow(
	rows pgx.Rows,
) (vehicleRow, bool, error) {
	var v vehicleRow
	var owner sql.NullInt64
	var legacyPhoto, photoURLsJSON string
	var lat, lng sql.NullFloat64
	if err := rows.Scan(
		&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &v.ReviewCount,
		&v.CreatedAt, &owner,
		&legacyPhoto, &photoURLsJSON, &v.MileageKm, &v.ModelYear, &v.Transmission, &v.FuelType, &v.Drivetrain,
		&v.EngineCC, &v.ExteriorColor, &v.ConditionSummary, &v.TechNotes, &v.VIN,
		&lat, &lng, &v.ListingStatus,
		&v.MinRentalDays, &v.MaxRentalDays, &v.SeatCount, &v.PetsAllowed, &v.FuelReturnPolicy,
		&v.ModerationNote, &v.CompletedTrips,
	); err != nil {
		return v, false, err
	}
	finalizeVehicleRow(&v, owner, legacyPhoto, photoURLsJSON, lat, lng)
	return v, true, nil
}

func scanVehicleRowQuery(
	row pgx.Row,
) (vehicleRow, error) {
	var v vehicleRow
	var owner sql.NullInt64
	var legacyPhoto, photoURLsJSON string
	var lat, lng sql.NullFloat64
	if err := row.Scan(
		&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &v.ReviewCount,
		&v.CreatedAt, &owner,
		&legacyPhoto, &photoURLsJSON, &v.MileageKm, &v.ModelYear, &v.Transmission, &v.FuelType, &v.Drivetrain,
		&v.EngineCC, &v.ExteriorColor, &v.ConditionSummary, &v.TechNotes, &v.VIN,
		&lat, &lng, &v.ListingStatus,
		&v.MinRentalDays, &v.MaxRentalDays, &v.SeatCount, &v.PetsAllowed, &v.FuelReturnPolicy,
		&v.ModerationNote, &v.CompletedTrips,
	); err != nil {
		return v, err
	}
	finalizeVehicleRow(&v, owner, legacyPhoto, photoURLsJSON, lat, lng)
	return v, nil
}

func finalizeVehicleRow(
	v *vehicleRow,
	owner sql.NullInt64,
	legacyPhoto, photoURLsJSON string,
	lat, lng sql.NullFloat64,
) {
	v.PricePerDay = float64(v.PricePerDayCents) / 100
	if owner.Valid {
		oid := owner.Int64
		v.OwnerUserID = &oid
	}
	if lat.Valid {
		l := lat.Float64
		v.Latitude = &l
	}
	if lng.Valid {
		l := lng.Float64
		v.Longitude = &l
	}
	fillVehicleRowPhotos(v, legacyPhoto, photoURLsJSON)
}

func scanVehicleRowReturning(row pgx.Row) (vehicleRow, error) {
	var v vehicleRow
	var owner sql.NullInt64
	var legacyPhoto, photoURLsJSON string
	var lat, lng sql.NullFloat64
	if err := row.Scan(
		&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &v.ReviewCount,
		&v.CreatedAt, &owner,
		&legacyPhoto, &photoURLsJSON, &v.MileageKm, &v.ModelYear, &v.Transmission, &v.FuelType, &v.Drivetrain,
		&v.EngineCC, &v.ExteriorColor, &v.ConditionSummary, &v.TechNotes, &v.VIN,
		&lat, &lng, &v.ListingStatus,
		&v.MinRentalDays, &v.MaxRentalDays, &v.SeatCount, &v.PetsAllowed, &v.FuelReturnPolicy,
		&v.ModerationNote,
	); err != nil {
		return v, err
	}
	v.CompletedTrips = 0
	finalizeVehicleRow(&v, owner, legacyPhoto, photoURLsJSON, lat, lng)
	return v, nil
}

func (a *api) fillVehicleCompletedTrips(ctx context.Context, v *vehicleRow) error {
	return a.db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM rental_deals
		WHERE vehicle_id = $1 AND status = 'completed'
	`, v.ID).Scan(&v.CompletedTrips)
}
