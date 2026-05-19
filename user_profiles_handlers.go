package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

type userProfilePublic struct {
	ID          int64        `json:"id"`
	FullName    string       `json:"fullName"`
	AvatarURL   string       `json:"avatarUrl"`
	MemberSince string       `json:"memberSince"`
	IsHost      bool         `json:"isHost"`
	IsRenter    bool         `json:"isRenter"`
	Rating      float64      `json:"rating"`
	ReviewCount int32        `json:"reviewCount"`
	Listings    []vehicleRow `json:"listings"`
}

func (a *api) listPublishedVehiclesForOwner(ctx context.Context, ownerID int64) ([]vehicleRow, error) {
	rows, err := a.db.Query(ctx, `
		SELECT `+vehicleListSelectSQL+`
		FROM vehicles
		WHERE owner_user_id = $1 AND listing_status = $2
		ORDER BY created_at DESC, id DESC
	`, ownerID, vehicleStatusPublished)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []vehicleRow
	for rows.Next() {
		v, _, err := scanVehicleRow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []vehicleRow{}
	}
	return list, nil
}

func (a *api) getUserProfile(c *echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var p userProfilePublic
	var isHost, isRenter bool
	var rating sql.NullFloat64
	var reviewCount sql.NullInt64
	err = a.db.QueryRow(ctx, `
		SELECT
			u.id,
			u.full_name,
			u.avatar_url,
			u.created_at::text,
			EXISTS(SELECT 1 FROM vehicles v WHERE v.owner_user_id = u.id),
			EXISTS(SELECT 1 FROM rental_deals d WHERE d.renter_id = u.id),
			CASE WHEN COALESCE(SUM(v.review_count), 0) > 0
				THEN SUM(v.rating * v.review_count) / SUM(v.review_count)
				ELSE NULL END,
			COALESCE(SUM(v.review_count), 0)::int
		FROM app_users u
		LEFT JOIN vehicles v ON v.owner_user_id = u.id AND v.listing_status = $2
		WHERE u.id = $1
		GROUP BY u.id, u.full_name, u.avatar_url, u.created_at
	`, id, vehicleStatusPublished).Scan(
		&p.ID, &p.FullName, &p.AvatarURL, &p.MemberSince, &isHost, &isRenter,
		&rating, &reviewCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	p.IsHost = isHost
	p.IsRenter = isRenter
	if rating.Valid {
		p.Rating = rating.Float64
	}
	if reviewCount.Valid {
		p.ReviewCount = int32(reviewCount.Int64)
	}

	listings, err := a.listPublishedVehiclesForOwner(ctx, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	p.Listings = listings

	return c.JSON(http.StatusOK, p)
}
