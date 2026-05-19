package main

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

type vehicleReviewRow struct {
	ID         int64   `json:"id"`
	AuthorName string  `json:"authorName"`
	Rating     float64 `json:"rating"`
	Body       string  `json:"body"`
	CreatedAt  string  `json:"createdAt"`
}

func (a *api) getVehicleReviews(c *echo.Context) error {
	vehicleID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || vehicleID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var exists bool
	if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vehicles WHERE id = $1)`, vehicleID).Scan(&exists); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "server_error"})
	}
	if !exists {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
	}

	rows, err := a.db.Query(ctx, `
		SELECT id, author_name, rating, body, created_at::text
		FROM vehicle_reviews
		WHERE vehicle_id = $1
		ORDER BY created_at DESC
	`, vehicleID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []vehicleReviewRow
	for rows.Next() {
		var r vehicleReviewRow
		if err := rows.Scan(&r.ID, &r.AuthorName, &r.Rating, &r.Body, &r.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if list == nil {
		list = []vehicleReviewRow{}
	}
	return c.JSON(http.StatusOK, list)
}

func seedVehicleReviews(ctx context.Context, tx pgx.Tx, vehicleID int32, baseRating float64) error {
	type sample struct {
		author string
		rating float64
		body   string
		daysAgo int
	}
	samples := []sample{
		{"Alex M.", baseRating, "Clean car, easy pickup. Would rent again.", 12},
		{"Sofia K.", baseRating - 0.1, "Smooth ride around the city. Owner was responsive.", 28},
		{"Denis P.", baseRating + 0.05, "Exactly as described. Fuel level was full at handover.", 45},
		{"Elena R.", baseRating, "Good value for the class. Minor wear inside but fine overall.", 60},
		{"Igor V.", baseRating - 0.2, "Quick booking and return. GPS worked well.", 75},
		{"Maria L.", baseRating + 0.1, "Comfortable for a weekend trip. Recommend.", 90},
		{"Pavel S.", baseRating, "No issues during the rental. Clear instructions from owner.", 110},
		{"Nina T.", baseRating - 0.05, "Punctual handover, car handled highways nicely.", 130},
	}

	for _, s := range samples {
		if s.rating < 1 {
			s.rating = 1
		}
		if s.rating > 5 {
			s.rating = 5
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO vehicle_reviews (vehicle_id, author_name, rating, body, created_at)
			VALUES ($1, $2, $3, $4, NOW() - ($5::int * INTERVAL '1 day'))
		`, vehicleID, s.author, s.rating, s.body, s.daysAgo)
		if err != nil {
			return err
		}
	}

	_, err := tx.Exec(ctx, `
		UPDATE vehicles v
		SET review_count = (
			SELECT COUNT(*)::int FROM vehicle_reviews r WHERE r.vehicle_id = v.id
		),
		rating = (
			SELECT COALESCE(AVG(r.rating)::real, v.rating)
			FROM vehicle_reviews r
			WHERE r.vehicle_id = v.id
		)
		WHERE v.id = $1
	`, vehicleID)
	return err
}
