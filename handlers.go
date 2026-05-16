package main

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type api struct {
	db *pgxpool.Pool
}

type vehicleRow struct {
	ID               int32   `json:"id"`
	Title            string  `json:"title"`
	City             string  `json:"city"`
	Class            string  `json:"class"`
	PricePerDayCents int32   `json:"pricePerDayCents"`
	PricePerDay      float64 `json:"pricePerDay"`
	Rating           float64 `json:"rating"`
	OwnerUserID      *int64  `json:"ownerUserId,omitempty"`
}

type createVehicleRequest struct {
	Title       string   `json:"title"`
	City        string   `json:"city"`
	Class       string   `json:"class"`
	PricePerDay float64  `json:"pricePerDay"`
	Rating      *float64 `json:"rating"`
}

func scanVehicle(v *vehicleRow, owner *sql.NullInt64, row interface {
	Scan(...any) error
}) error {
	return row.Scan(&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, owner)
}

func (a *api) getVehicles(c *echo.Context) error {
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT id, title, city, class, price_per_day_cents, rating, owner_user_id
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
		if err := rows.Scan(&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &owner); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		v.PricePerDay = float64(v.PricePerDayCents) / 100
		if owner.Valid {
			oid := owner.Int64
			v.OwnerUserID = &oid
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
	var v vehicleRow
	var owner sql.NullInt64
	err = a.db.QueryRow(ctx, `
		SELECT id, title, city, class, price_per_day_cents, rating, owner_user_id
		FROM vehicles WHERE id = $1
	`, id).Scan(&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &owner)
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
	return c.JSON(http.StatusOK, v)
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

	ctx := c.Request().Context()
	var v vehicleRow
	var owner sql.NullInt64
	err := a.db.QueryRow(ctx, `
		INSERT INTO vehicles (title, city, class, price_per_day_cents, rating, owner_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, title, city, class, price_per_day_cents, rating, owner_user_id
	`, title, city, class, cents, rating, uid).Scan(&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating, &owner)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23503" {
			// FK violation: the user from the JWT no longer exists (e.g. DB was reset).
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "session_invalid"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	v.PricePerDay = float64(v.PricePerDayCents) / 100
	if owner.Valid {
		oid := owner.Int64
		v.OwnerUserID = &oid
	}
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
	registerDealRoutes(secured, h)
}
