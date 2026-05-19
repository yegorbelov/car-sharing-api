package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type api struct {
	db      *pgxpool.Pool
	chatHub *dealChatHub
}

type vehicleRow struct {
	ID               int32    `json:"id"`
	Title            string   `json:"title"`
	City             string   `json:"city"`
	Class            string   `json:"class"`
	PricePerDayCents int32    `json:"pricePerDayCents"`
	PricePerDay      float64  `json:"pricePerDay"`
	Rating           float64  `json:"rating"`
	ReviewCount      int32    `json:"reviewCount"`
	CreatedAt        string   `json:"createdAt"`
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
	Latitude         *float64 `json:"latitude,omitempty"`
	Longitude        *float64 `json:"longitude,omitempty"`
	ListingStatus    string   `json:"listingStatus"`
	MinRentalDays    int32    `json:"minRentalDays"`
	MaxRentalDays    int32    `json:"maxRentalDays"`
	SeatCount        int32    `json:"seatCount"`
	PetsAllowed        bool   `json:"petsAllowed"`
	FuelReturnPolicy   string `json:"fuelReturnPolicy"`
	ModerationNote     string `json:"moderationNote"`
	CompletedTrips     int32  `json:"completedTrips"`
}

const (
	vehicleStatusPublished          = "published"
	vehicleStatusPendingModeration  = "pending_moderation"
	vehicleStatusUnpublished        = "unpublished"
)

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
	Latitude         *float64 `json:"latitude"`
	Longitude        *float64 `json:"longitude"`
	PhotoURLs        []string `json:"photoUrls"`
	MinRentalDays    *int32   `json:"minRentalDays"`
	MaxRentalDays    *int32   `json:"maxRentalDays"`
	SeatCount        *int32   `json:"seatCount"`
	PetsAllowed        *bool   `json:"petsAllowed"`
	FuelReturnPolicy   *string `json:"fuelReturnPolicy"`
}

const vehicleReturningCols = `
	id, title, city, class, price_per_day_cents, rating, review_count,
	created_at::text, owner_user_id,
	photo_url, photo_urls, mileage_km, model_year, transmission, fuel_type, drivetrain,
	engine_cc, exterior_color, condition_summary, tech_notes, vin,
	latitude, longitude, listing_status,
	min_rental_days, max_rental_days, seat_count, pets_allowed, fuel_return_policy,
	moderation_note`

var allowedFuelReturnPolicy = map[string]struct{}{
	"same_level": {}, "full_tank": {}, "quarter_tank": {},
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
		SELECT `+vehicleListSelectSQL+`
		FROM vehicles
		WHERE listing_status = 'published'
		ORDER BY id
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []vehicleRow
	for rows.Next() {
		v, _, err := scanVehicleRow(rows)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
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
	row := a.db.QueryRow(ctx, `
		SELECT `+vehicleListSelectSQL+`
		FROM vehicles WHERE id = $1 AND listing_status = 'published'
	`, id)
	v, err := scanVehicleRowQuery(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, v)
}

func (a *api) getMyVehicles(c *echo.Context) error {
	uid := authUserID(c)
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT `+vehicleListSelectSQL+`
		FROM vehicles
		WHERE owner_user_id = $1
		ORDER BY created_at DESC, id DESC
	`, uid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []vehicleRow
	for rows.Next() {
		v, _, err := scanVehicleRow(rows)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
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

func (a *api) postVehicle(c *echo.Context) error {
	uid := authUserID(c)
	var req createVehicleRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	in, errKey := parseVehicleInput(req)
	if errKey != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errKey})
	}

	ctx := c.Request().Context()
	row := a.db.QueryRow(ctx, `
		INSERT INTO vehicles (
			title, city, class, price_per_day_cents, rating, owner_user_id,
			mileage_km, model_year, transmission, fuel_type, drivetrain,
			engine_cc, exterior_color, condition_summary, tech_notes, vin,
			latitude, longitude, listing_status,
			min_rental_days, max_rental_days, seat_count, pets_allowed, fuel_return_policy
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		RETURNING `+vehicleReturningCols+`
	`, in.Title, in.City, in.Class, in.PriceCents, in.Rating, uid,
		in.MileageKm, in.ModelYear, in.Transmission, in.FuelType, in.Drivetrain,
		in.EngineCC, in.ExteriorColor, in.ConditionSummary, in.TechNotes, in.VIN,
		in.Latitude, in.Longitude, vehicleStatusPendingModeration,
		in.MinRentalDays, in.MaxRentalDays, in.SeatCount, in.PetsAllowed, in.FuelReturnPolicy,
	)
	v, err := scanVehicleRowReturning(row)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23503" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "session_invalid"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	v.Latitude = &in.Latitude
	v.Longitude = &in.Longitude
	return c.JSON(http.StatusCreated, v)
}

func (a *api) patchVehicle(c *echo.Context) error {
	uid := authUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	var req createVehicleRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	in, errKey := parseVehicleInput(req)
	if errKey != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errKey})
	}

	photoJSON := ""
	cover := ""
	if req.PhotoURLs != nil {
		if len(req.PhotoURLs) > maxVehiclePhotos {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "too_many_photos"})
		}
		clean := make([]string, 0, len(req.PhotoURLs))
		for _, u := range req.PhotoURLs {
			u = strings.TrimSpace(u)
			if u != "" {
				clean = append(clean, u)
			}
		}
		var marshalErr error
		photoJSON, marshalErr = marshalVehiclePhotoURLs(clean)
		if marshalErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": marshalErr.Error()})
		}
		cover = primaryVehiclePhoto(clean)
	}

	ctx := c.Request().Context()
	var row pgx.Row

	if req.PhotoURLs != nil {
		row = a.db.QueryRow(ctx, `
			UPDATE vehicles SET
				title = $1, city = $2, class = $3, price_per_day_cents = $4,
				mileage_km = $5, model_year = $6, transmission = $7, fuel_type = $8,
				drivetrain = $9, engine_cc = $10, exterior_color = $11,
				condition_summary = $12, tech_notes = $13, vin = $14,
				latitude = $15, longitude = $16,
				min_rental_days = $17, max_rental_days = $18, seat_count = $19, pets_allowed = $20,
				fuel_return_policy = $21,
				photo_urls = $22, photo_url = $23,
				listing_status = CASE
					WHEN listing_status IN ($24, $25) THEN $26
					ELSE listing_status
				END,
				moderation_note = CASE
					WHEN listing_status IN ($24, $25) THEN ''
					ELSE moderation_note
				END
			WHERE id = $27 AND owner_user_id = $28
			RETURNING `+vehicleReturningCols+`
		`, in.Title, in.City, in.Class, in.PriceCents,
			in.MileageKm, in.ModelYear, in.Transmission, in.FuelType, in.Drivetrain,
			in.EngineCC, in.ExteriorColor, in.ConditionSummary, in.TechNotes, in.VIN,
			in.Latitude, in.Longitude,
			in.MinRentalDays, in.MaxRentalDays, in.SeatCount, in.PetsAllowed, in.FuelReturnPolicy,
			photoJSON, cover,
			vehicleStatusPublished, vehicleStatusRejected, vehicleStatusPendingModeration,
			id, uid,
		)
	} else {
		row = a.db.QueryRow(ctx, `
			UPDATE vehicles SET
				title = $1, city = $2, class = $3, price_per_day_cents = $4,
				mileage_km = $5, model_year = $6, transmission = $7, fuel_type = $8,
				drivetrain = $9, engine_cc = $10, exterior_color = $11,
				condition_summary = $12, tech_notes = $13, vin = $14,
				latitude = $15, longitude = $16,
				min_rental_days = $17, max_rental_days = $18, seat_count = $19, pets_allowed = $20,
				fuel_return_policy = $21,
				listing_status = CASE
					WHEN listing_status IN ($22, $23) THEN $24
					ELSE listing_status
				END,
				moderation_note = CASE
					WHEN listing_status IN ($22, $23) THEN ''
					ELSE moderation_note
				END
			WHERE id = $25 AND owner_user_id = $26
			RETURNING `+vehicleReturningCols+`
		`, in.Title, in.City, in.Class, in.PriceCents,
			in.MileageKm, in.ModelYear, in.Transmission, in.FuelType, in.Drivetrain,
			in.EngineCC, in.ExteriorColor, in.ConditionSummary, in.TechNotes, in.VIN,
			in.Latitude, in.Longitude,
			in.MinRentalDays, in.MaxRentalDays, in.SeatCount, in.PetsAllowed, in.FuelReturnPolicy,
			vehicleStatusPublished, vehicleStatusRejected, vehicleStatusPendingModeration,
			id, uid,
		)
	}
	v, err := scanVehicleRowReturning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := a.fillVehicleCompletedTrips(ctx, &v); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, v)
}

func (a *api) unpublishVehicle(c *echo.Context) error {
	uid := authUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var active int32
	err = a.db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM rental_deals
		WHERE vehicle_id = $1 AND status IN ($2, $3)
	`, id, dealPendingOwner, dealActive).Scan(&active)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if active > 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "has_active_deals"})
	}

	row := a.db.QueryRow(ctx, `
		UPDATE vehicles SET listing_status = $1
		WHERE id = $2 AND owner_user_id = $3
			AND listing_status IN ($4, $5)
		RETURNING `+vehicleReturningCols+`
	`, vehicleStatusUnpublished, id, uid, vehicleStatusPublished, vehicleStatusPendingModeration)
	v, err := scanVehicleRowReturning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := a.fillVehicleCompletedTrips(ctx, &v); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, v)
}

func (a *api) publishVehicle(c *echo.Context) error {
	uid := authUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var active int32
	err = a.db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM rental_deals
		WHERE vehicle_id = $1 AND status IN ($2, $3)
	`, id, dealPendingOwner, dealActive).Scan(&active)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if active > 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "has_active_deals"})
	}

	row := a.db.QueryRow(ctx, `
		UPDATE vehicles SET listing_status = $1
		WHERE id = $2 AND owner_user_id = $3
			AND listing_status = $4
		RETURNING `+vehicleReturningCols+`
	`, vehicleStatusPendingModeration, id, uid, vehicleStatusUnpublished)
	v, err := scanVehicleRowReturning(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := a.fillVehicleCompletedTrips(ctx, &v); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, v)
}

func registerAPIRoutes(e *echo.Echo, pool *pgxpool.Pool) {
	h := &api{db: pool, chatHub: newDealChatHub()}
	g := e.Group("/api/v1")
	g.GET("/vehicles", h.getVehicles)
	g.GET("/vehicles/:id", h.getVehicle)
	g.GET("/vehicles/:id/reviews", h.getVehicleReviews)
	g.GET("/users/:id/profile", h.getUserProfile)
	g.POST("/auth/register", h.postRegister)
	g.POST("/auth/login", h.postLogin)
	g.GET("/auth/me", h.getMe)
	// WebSocket auth uses ?token= or Bearer in the handler (not requireAuth).
	g.GET("/deals/:id/messages/ws", h.getDealMessagesWS)

	secured := g.Group("", h.requireAuth)
	secured.PATCH("/auth/me", h.patchMe)
	secured.GET("/vehicles/mine", h.getMyVehicles)
	secured.POST("/vehicles", h.postVehicle)
	secured.PATCH("/vehicles/:id", h.patchVehicle)
	secured.POST("/vehicles/:id/unpublish", h.unpublishVehicle)
	secured.POST("/vehicles/:id/publish", h.publishVehicle)
	secured.POST("/auth/avatar", h.postAvatar)
	secured.POST("/vehicles/:id/photo", h.postVehiclePhoto)
	registerDealRoutes(secured, h)
	registerDisputeRoutes(secured, h)

	mod := secured.Group("", h.requireModerator)
	mod.GET("/moderation/vehicles", h.listModerationVehicles)
	mod.GET("/moderation/rejection-reasons", h.listRejectionReasons)
	mod.POST("/moderation/vehicles/:id/approve", h.approveModerationVehicle)
	mod.POST("/moderation/vehicles/:id/reject", h.rejectModerationVehicle)

	admin := secured.Group("", h.requireAdmin)
	admin.GET("/admin/users", h.listAdminUsers)
	admin.PATCH("/admin/users/:id/roles", h.patchAdminUserRoles)
	admin.GET("/admin/audit-log", h.listStaffAudit)
}
