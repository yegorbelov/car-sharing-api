package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type api struct {
	db *pgxpool.Pool
}

type healthResponse struct {
	Status   string         `json:"status"`
	Database databaseInfo `json:"database"`
}

type databaseInfo struct {
	Up bool `json:"up"`
}

type vehicleRow struct {
	ID                int32   `json:"id"`
	Title             string  `json:"title"`
	City              string  `json:"city"`
	Class             string  `json:"class"`
	PricePerDayCents  int32   `json:"pricePerDayCents"`
	PricePerDay       float64 `json:"pricePerDay"`
	Rating            float64 `json:"rating"`
}

type bookingRow struct {
	ID         int32   `json:"id"`
	VehicleID  *int32  `json:"vehicleId,omitempty"`
	RenterName string  `json:"renterName"`
	Status     string  `json:"status"`
	StartDate  *string `json:"startDate,omitempty"`
	EndDate    *string `json:"endDate,omitempty"`
	CreatedAt  string  `json:"createdAt"`
}

func (a *api) getHealth(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()

	err := a.db.Ping(ctx)
	up := err == nil

	return c.JSON(http.StatusOK, healthResponse{
		Status: "ok",
		Database: databaseInfo{
			Up: up,
		},
	})
}

func (a *api) getVehicles(c *echo.Context) error {
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT id, title, city, class, price_per_day_cents, rating
		FROM vehicles
		ORDER BY id
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []vehicleRow
	for rows.Next() {
		var v vehicleRow
		if err := rows.Scan(&v.ID, &v.Title, &v.City, &v.Class, &v.PricePerDayCents, &v.Rating); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		v.PricePerDay = float64(v.PricePerDayCents) / 100
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

func (a *api) getBookings(c *echo.Context) error {
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT id, vehicle_id, renter_name, status,
		       start_date::text, end_date::text, created_at::text
		FROM bookings
		ORDER BY id
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []bookingRow
	for rows.Next() {
		var b bookingRow
		var start, end sql.NullString
		var vid sql.NullInt32
		if err := rows.Scan(&b.ID, &vid, &b.RenterName, &b.Status, &start, &end, &b.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if vid.Valid {
			v := vid.Int32
			b.VehicleID = &v
		}
		if start.Valid {
			s := start.String
			b.StartDate = &s
		}
		if end.Valid {
			s := end.String
			b.EndDate = &s
		}
		list = append(list, b)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if list == nil {
		list = []bookingRow{}
	}
	return c.JSON(http.StatusOK, list)
}

func registerAPIRoutes(e *echo.Echo, pool *pgxpool.Pool) {
	h := &api{db: pool}
	g := e.Group("/api/v1")
	g.GET("/health", h.getHealth)
	g.GET("/vehicles", h.getVehicles)
	g.GET("/bookings", h.getBookings)
	g.POST("/auth/register", h.postRegister)
	g.POST("/auth/login", h.postLogin)
	g.GET("/auth/me", h.getMe)
}
