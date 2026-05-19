package main

import (
	"math"
	"strings"
	"unicode"
)

type parsedVehicleInput struct {
	Title, City, Class, Transmission, FuelType, Drivetrain string
	ExteriorColor, ConditionSummary, TechNotes, VIN      string
	PriceCents, MileageKm, ModelYear, EngineCC           int32
	MinRentalDays, MaxRentalDays, SeatCount              int32
	Rating                                               float64
	Latitude, Longitude                                  float64
	PetsAllowed                                          bool
	FuelReturnPolicy                                     string
}

func parseVehicleInput(req createVehicleRequest) (parsedVehicleInput, string) {
	var out parsedVehicleInput
	out.Title = strings.TrimSpace(req.Title)
	out.City = strings.TrimSpace(req.City)
	out.Class = strings.TrimSpace(req.Class)
	if out.Title == "" || out.City == "" || out.Class == "" {
		return out, "missing_fields"
	}
	if req.PricePerDay <= 0 || req.PricePerDay > 50_000 {
		return out, "invalid_price"
	}
	out.PriceCents = int32(math.Round(req.PricePerDay * 100))
	if out.PriceCents < 1 {
		return out, "invalid_price"
	}
	out.Rating = 4.5
	if req.Rating != nil {
		out.Rating = *req.Rating
		if out.Rating < 1 || out.Rating > 5 {
			return out, "invalid_rating"
		}
	}
	if req.MileageKm != nil {
		out.MileageKm = *req.MileageKm
	}
	if out.MileageKm < 0 || out.MileageKm > 2_000_000 {
		return out, "invalid_mileage"
	}
	if req.ModelYear != nil {
		out.ModelYear = *req.ModelYear
	}
	if out.ModelYear != 0 && (out.ModelYear < 1980 || out.ModelYear > 2035) {
		return out, "invalid_model_year"
	}
	if req.Transmission != nil {
		out.Transmission = strings.ToLower(strings.TrimSpace(*req.Transmission))
	}
	if _, ok := allowedTransmission[out.Transmission]; !ok {
		return out, "invalid_transmission"
	}
	if req.FuelType != nil {
		out.FuelType = strings.ToLower(strings.TrimSpace(*req.FuelType))
	}
	if _, ok := allowedFuel[out.FuelType]; !ok {
		return out, "invalid_fuel_type"
	}
	if req.Drivetrain != nil {
		out.Drivetrain = strings.ToLower(strings.TrimSpace(*req.Drivetrain))
	}
	if _, ok := allowedDrivetrain[out.Drivetrain]; !ok {
		return out, "invalid_drivetrain"
	}
	if req.EngineCC != nil {
		out.EngineCC = *req.EngineCC
	}
	if out.EngineCC < 0 || out.EngineCC > 20_000 {
		return out, "invalid_engine_cc"
	}
	if req.ExteriorColor != nil {
		out.ExteriorColor = strings.TrimSpace(*req.ExteriorColor)
	}
	if len(out.ExteriorColor) > 64 {
		return out, "invalid_exterior_color"
	}
	if req.ConditionSummary != nil {
		out.ConditionSummary = strings.TrimSpace(*req.ConditionSummary)
	}
	if len(out.ConditionSummary) < 3 || len(out.ConditionSummary) > 2000 {
		return out, "invalid_condition_summary"
	}
	if req.TechNotes != nil {
		out.TechNotes = strings.TrimSpace(*req.TechNotes)
	}
	if len(out.TechNotes) > 4000 {
		return out, "invalid_tech_notes"
	}
	if req.VIN != nil {
		out.VIN = strings.ToUpper(strings.TrimSpace(*req.VIN))
	}
	if len(out.VIN) > 17 {
		return out, "invalid_vin"
	}
	if out.VIN != "" && !isAlnumVIN(out.VIN) {
		return out, "invalid_vin"
	}
	if req.Latitude == nil || req.Longitude == nil {
		return out, "missing_location"
	}
	out.Latitude = *req.Latitude
	out.Longitude = *req.Longitude
	if out.Latitude < -90 || out.Latitude > 90 || out.Longitude < -180 || out.Longitude > 180 {
		return out, "invalid_location"
	}
	out.MinRentalDays = 1
	if req.MinRentalDays != nil {
		out.MinRentalDays = *req.MinRentalDays
	}
	out.MaxRentalDays = 14
	if req.MaxRentalDays != nil {
		out.MaxRentalDays = *req.MaxRentalDays
	}
	if out.MinRentalDays < 1 || out.MinRentalDays > 90 {
		return out, "invalid_min_rental_days"
	}
	if out.MaxRentalDays < 1 || out.MaxRentalDays > 90 {
		return out, "invalid_max_rental_days"
	}
	if out.MinRentalDays > out.MaxRentalDays {
		return out, "invalid_rental_days_range"
	}
	out.SeatCount = 5
	if req.SeatCount != nil {
		out.SeatCount = *req.SeatCount
	}
	if out.SeatCount < 1 || out.SeatCount > 12 {
		return out, "invalid_seat_count"
	}
	out.PetsAllowed = false
	if req.PetsAllowed != nil {
		out.PetsAllowed = *req.PetsAllowed
	}
	out.FuelReturnPolicy = "same_level"
	if req.FuelReturnPolicy != nil {
		out.FuelReturnPolicy = strings.ToLower(strings.TrimSpace(*req.FuelReturnPolicy))
	}
	if _, ok := allowedFuelReturnPolicy[out.FuelReturnPolicy]; !ok {
		return out, "invalid_fuel_return_policy"
	}
	return out, ""
}

func isAlnumVIN(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
