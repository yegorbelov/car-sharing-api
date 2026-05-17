package main

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type api struct {
	db *pgxpool.Pool
}

type vehicleRow struct {
	ID               int32    `json:"id"`
	Title            string   `json:"title"`
	City             string   `json:"city"`
	Class            string   `json:"class"`
	PricePerDayCents int32    `json:"pricePerDayCents"`
	PricePerDay      float64  `json:"pricePerDay"`
	Rating           float64  `json:"rating"`
	OwnerUserID      *int64   `json:"ownerUserId,omitempty"`
	PhotoURL         string   `json:"photoUrl,omitempty"`
	PhotoURLs        []string `json:"photoUrls"`
	MileageKm        int32    `json:"mileageKm"`
	ModelYear        int32    `json:"modelYear"`
	Transmission     string   `json:"transmission"`
	FuelType         string   `json:"fuelType"`
	Drivetrain       string   `json:"drivetrain"`
	EngineCC         int32    `json:"engineCc"`
	ExteriorColor    string   `json:"exteriorColor"`
	ConditionSummary string   `json:"conditionSummary"`
	TechNotes        string   `json:"techNotes"`
	VIN              string   `json:"vin"`
}

type createVehicleRequest struct {
	Title            string   `json:"title"`
	City             string   `json:"city"`
	Class            string   `json:"class"`
	PricePerDay      float64  `json:"pricePerDay"`
	Rating           *float64 `json:"rating"`
	MileageKm        *int32   `json:"mileageKm"`
	ModelYear        *int32   `json:"modelYear"`
	Transmission     *string  `json:"transmission"`
	FuelType         *string  `json:"fuelType"`
	Drivetrain       *string  `json:"drivetrain"`
	EngineCC         *int32   `json:"engineCc"`
	ExteriorColor    *string  `json:"exteriorColor"`
	ConditionSummary *string  `json:"conditionSummary"`
	TechNotes        *string  `json:"techNotes"`
	VIN              *string  `json:"vin"`
}

var allowedTransmission = map[string]struct{}{
	"automatic": {}, "manual": {}, "cvt": {}, "other": {},
}
var allowedFuel = map[string]struct{}{
	"petrol": {}, "diesel": {}, "electric": {}, "hybrid": {}, "lpg": {}, "other": {},
}
var allowedDrivetrain = map[string]struct{}{
	"fwd": {}, "rwd": {}, "awd": {}, "other": {},
}

func fillVehicleRowPhotos(v *vehicleRow, legacyPhoto, photoURLsJSON string) {
	list := effectiveVehiclePhotoList(photoURLsJSON, legacyPhoto)
	v.PhotoURLs = list
	v.PhotoURL = primaryVehiclePhoto(list)
}

func (a *api) getVehicles(c *echo.Context) error {
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT id, title, city, class, price_per_day_cents, rating, owner_user_id,
			photo_url, photo_urls, mileage_km, model_year, transmission, fuel_type, drivetrain,
			engine_cc, exterior_color, condition_summary, tech_notes, vin
		FROM vehicles ORDER BY id
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []vehicleRow
	for rows.Next() {
		var v vehicleRow
		var owner sql.NullInt64
		var legacyPhoto, photoURLsJSON string
		if err := rows.Scan(
			&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &owner,
			&legacyPhoto, &photoURLsJSON, &v.MileageKm, &v.ModelYear, &v.Transmission, &v.FuelType, &v.Drivetrain,
			&v.EngineCC, &v.ExteriorColor, &v.ConditionSummary, &v.TechNotes, &v.VIN,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		v.PricePerDay = float64(v.PricePerDayCents) / 100
		if owner.Valid {
			oid := owner.Int64
			v.OwnerUserID = &oid
		}
		fillVehicleRowPhotos(&v, legacyPhoto, photoURLsJSON)
		list = append(list, v)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if list == nil {
		list = []vehicleRow{}
	}
	return c.JSON(http.StatusOK, list)
}

func (a *api) getVehicle(c *echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ctx := c.Request().Context()
	var v vehicleRow
	var owner sql.NullInt64
	var legacyPhoto, photoURLsJSON string
	err = a.db.QueryRow(ctx, `
		SELECT id, title, city, class, price_per_day_cents, rating, owner_user_id,
			photo_url, photo_urls, mileage_km, model_year, transmission, fuel_type, drivetrain,
			engine_cc, exterior_color, condition_summary, tech_notes, vin
		FROM vehicles WHERE id = $1
	`, id).Scan(
		&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &owner,
		&legacyPhoto, &photoURLsJSON, &v.MileageKm, &v.ModelYear, &v.Transmission, &v.FuelType, &v.Drivetrain,
		&v.EngineCC, &v.ExteriorColor, &v.ConditionSummary, &v.TechNotes, &v.VIN,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	v.PricePerDay = float64(v.PricePerDayCents) / 100
	if owner.Valid {
		oid := owner.Int64
		v.OwnerUserID = &oid
	}
	fillVehicleRowPhotos(&v, legacyPhoto, photoURLsJSON)
	return c.JSON(http.StatusOK, v)
}

func isAlnumVIN(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func (a *api) postVehicle(c *echo.Context) error {
	uid := authUserID(c)
	var req createVehicleRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	title := strings.TrimSpace(req.Title)
	city := strings.TrimSpace(req.City)
	class := strings.TrimSpace(req.Class)
	if title == "" || city == "" || class == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing_fields"})
	}
	if req.PricePerDay <= 0 || req.PricePerDay > 50_000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_price"})
	}
	cents := int32(math.Round(req.PricePerDay * 100))
	if cents < 1 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_price"})
	}
	rating := 4.5
	if req.Rating != nil {
		rating = *req.Rating
		if rating < 1 || rating > 5 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_rating"})
		}
	}

	mileageKm := int32(0)
	if req.MileageKm != nil {
		mileageKm = *req.MileageKm
	}
	if mileageKm < 0 || mileageKm > 2_000_000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_mileage"})
	}

	modelYear := int32(0)
	if req.ModelYear != nil {
		modelYear = *req.ModelYear
	}
	if modelYear != 0 && (modelYear < 1980 || modelYear > 2035) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_model_year"})
	}

	trans := ""
	if req.Transmission != nil {
		trans = strings.ToLower(strings.TrimSpace(*req.Transmission))
	}
	if _, ok := allowedTransmission[trans]; !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_transmission"})
	}

	fuel := ""
	if req.FuelType != nil {
		fuel = strings.ToLower(strings.TrimSpace(*req.FuelType))
	}
	if _, ok := allowedFuel[fuel]; !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_fuel_type"})
	}

	drive := ""
	if req.Drivetrain != nil {
		drive = strings.ToLower(strings.TrimSpace(*req.Drivetrain))
	}
	if _, ok := allowedDrivetrain[drive]; !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_drivetrain"})
	}

	engineCC := int32(0)
	if req.EngineCC != nil {
		engineCC = *req.EngineCC
	}
	if engineCC < 0 || engineCC > 20_000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_engine_cc"})
	}

	color := ""
	if req.ExteriorColor != nil {
		color = strings.TrimSpace(*req.ExteriorColor)
	}
	if len(color) > 64 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_exterior_color"})
	}

	cond := ""
	if req.ConditionSummary != nil {
		cond = strings.TrimSpace(*req.ConditionSummary)
	}
	if len(cond) < 3 || len(cond) > 2000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_condition_summary"})
	}

	notes := ""
	if req.TechNotes != nil {
		notes = strings.TrimSpace(*req.TechNotes)
	}
	if len(notes) > 4000 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_tech_notes"})
	}

	vin := ""
	if req.VIN != nil {
		vin = strings.ToUpper(strings.TrimSpace(*req.VIN))
	}
	if len(vin) > 17 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_vin"})
	}
	if vin != "" && !isAlnumVIN(vin) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_vin"})
	}

	ctx := c.Request().Context()
	var v vehicleRow
	var owner sql.NullInt64
	var legacyPhoto, photoURLsJSON string
	err := a.db.QueryRow(ctx, `
		INSERT INTO vehicles (
			title, city, class, price_per_day_cents, rating, owner_user_id,
			mileage_km, model_year, transmission, fuel_type, drivetrain,
			engine_cc, exterior_color, condition_summary, tech_notes, vin
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, title, city, class, price_per_day_cents, rating, owner_user_id,
			photo_url, photo_urls, mileage_km, model_year, transmission, fuel_type, drivetrain,
			engine_cc, exterior_color, condition_summary, tech_notes, vin
	`, title, city, class, cents, rating, uid,
		mileageKm, modelYear, trans, fuel, drive,
		engineCC, color, cond, notes, vin,
	).Scan(
		&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &owner,
		&legacyPhoto, &photoURLsJSON, &v.MileageKm, &v.ModelYear, &v.Transmission, &v.FuelType, &v.Drivetrain,
		&v.EngineCC, &v.ExteriorColor, &v.ConditionSummary, &v.TechNotes, &v.VIN,
	)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23503" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "session_invalid"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	v.PricePerDay = float64(v.PricePerDayCents) / 100
	if owner.Valid {
		oid := owner.Int64
		v.OwnerUserID = &oid
	}
	fillVehicleRowPhotos(&v, legacyPhoto, photoURLsJSON)
	return c.JSON(http.StatusCreated, v)
}

func registerAPIRoutes(e *echo.Echo, pool *pgxpool.Pool) {
	h := &api{db: pool}
	g := e.Group("/api/v1")
	g.GET("/vehicles", h.getVehicles)
	g.GET("/vehicles/:id", h.getVehicle)
	g.POST("/auth/register", h.postRegister)
	g.POST("/auth/login", h.postLogin)
	g.GET("/auth/me", h.getMe)

	secured := g.Group("", h.requireAuth)
	secured.POST("/vehicles", h.postVehicle)
	secured.POST("/auth/avatar", h.postAvatar)
	secured.POST("/vehicles/:id/photo", h.postVehiclePhoto)
	registerDealRoutes(secured, h)
}
