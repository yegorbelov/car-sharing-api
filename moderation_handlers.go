package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

const vehicleStatusRejected = "rejected"

var allowedRejectionReasons = map[string]string{
	"incorrect_photos":       "Photos do not match the vehicle",
	"misleading_description": "Misleading or incomplete description",
	"incomplete_info":        "Missing required information",
	"prohibited_content":     "Prohibited content",
	"other":                  "Other",
}

type rejectListingRequest struct {
	ReasonCode string `json:"reasonCode"`
	Note       string `json:"note"`
}

func (a *api) listModerationVehicles(c *echo.Context) error {
	uid := authUserID(c)
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT `+vehicleListSelectSQL+`
		FROM vehicles
		WHERE listing_status = $1 AND (owner_user_id IS NULL OR owner_user_id <> $2)
		ORDER BY created_at ASC, id ASC
	`, vehicleStatusPendingModeration, uid)
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

func (a *api) approveModerationVehicle(c *echo.Context) error {
	return a.moderateVehicle(c, true, rejectListingRequest{})
}

func (a *api) rejectModerationVehicle(c *echo.Context) error {
	var req rejectListingRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	req.ReasonCode = strings.ToLower(strings.TrimSpace(req.ReasonCode))
	if _, ok := allowedRejectionReasons[req.ReasonCode]; !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_reason_code"})
	}
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 500 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "note_too_long"})
	}
	return a.moderateVehicle(c, false, req)
}

func (a *api) moderateVehicle(c *echo.Context, approve bool, rejectReq rejectListingRequest) error {
	uid := authUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	ctx := c.Request().Context()
	var ownerID sql.NullInt64
	err = a.db.QueryRow(ctx, `
		SELECT owner_user_id FROM vehicles WHERE id = $1 AND listing_status = $2
	`, id, vehicleStatusPendingModeration).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if ownerID.Valid && ownerID.Int64 == uid {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "cannot_moderate_own_listing"})
	}

	newStatus := vehicleStatusPublished
	moderationNote := ""
	action := "vehicle_approved"
	details := map[string]any{"vehicleId": id}
	if !approve {
		newStatus = vehicleStatusRejected
		action = "vehicle_rejected"
		label := allowedRejectionReasons[rejectReq.ReasonCode]
		moderationNote = label
		if rejectReq.Note != "" {
			moderationNote = label + ": " + rejectReq.Note
		}
		details["reasonCode"] = rejectReq.ReasonCode
		details["note"] = rejectReq.Note
	}

	row := a.db.QueryRow(ctx, `
		UPDATE vehicles SET listing_status = $1, moderation_note = $2
		WHERE id = $3 AND listing_status = $4
		RETURNING `+vehicleReturningCols+`
	`, newStatus, moderationNote, id, vehicleStatusPendingModeration)
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
	if err := a.insertStaffAudit(ctx, uid, action, "vehicle", id, details); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, v)
}

func (a *api) listRejectionReasons(c *echo.Context) error {
	type reason struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	out := make([]reason, 0, len(allowedRejectionReasons))
	for code, label := range allowedRejectionReasons {
		out = append(out, reason{Code: code, Label: label})
	}
	return c.JSON(http.StatusOK, out)
}
