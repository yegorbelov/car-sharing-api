package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

type patchUserRolesRequest struct {
	IsModerator  *bool `json:"isModerator"`
	IsArbitrator *bool `json:"isArbitrator"`
	IsAdmin      *bool `json:"isAdmin"`
}

type adminUserRow struct {
	ID           int64    `json:"id"`
	Email        string   `json:"email"`
	FullName     string   `json:"fullName"`
	IsAdmin      bool     `json:"isAdmin"`
	IsModerator  bool     `json:"isModerator"`
	IsArbitrator bool     `json:"isArbitrator"`
	Roles        []string `json:"roles"`
}

func (a *api) listAdminUsers(c *echo.Context) error {
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT id, email, full_name, is_admin, is_moderator, is_arbitrator
		FROM app_users
		ORDER BY id
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var list []adminUserRow
	for rows.Next() {
		var u adminUserRow
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.IsAdmin, &u.IsModerator, &u.IsArbitrator); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		u.Roles = buildRoleList(userPublic{
			IsAdmin: u.IsAdmin, IsModerator: u.IsModerator, IsArbitrator: u.IsArbitrator,
		})
		list = append(list, u)
	}
	if list == nil {
		list = []adminUserRow{}
	}
	return c.JSON(http.StatusOK, list)
}

func (a *api) patchAdminUserRoles(c *echo.Context) error {
	actorID := authUserID(c)
	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || targetID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}

	var req patchUserRolesRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_json"})
	}
	if req.IsAdmin == nil && req.IsModerator == nil && req.IsArbitrator == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no_changes"})
	}

	ctx := c.Request().Context()
	var cur adminUserRow
	err = a.db.QueryRow(ctx, `
		SELECT id, email, full_name, is_admin, is_moderator, is_arbitrator
		FROM app_users WHERE id = $1
	`, targetID).Scan(&cur.ID, &cur.Email, &cur.FullName, &cur.IsAdmin, &cur.IsModerator, &cur.IsArbitrator)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	isAdmin := cur.IsAdmin
	isModerator := cur.IsModerator
	isArbitrator := cur.IsArbitrator
	if req.IsAdmin != nil {
		isAdmin = *req.IsAdmin
	}
	if req.IsModerator != nil {
		isModerator = *req.IsModerator
	}
	if req.IsArbitrator != nil {
		isArbitrator = *req.IsArbitrator
	}

	err = a.db.QueryRow(ctx, `
		UPDATE app_users SET is_admin = $1, is_moderator = $2, is_arbitrator = $3
		WHERE id = $4
		RETURNING id, email, full_name, is_admin, is_moderator, is_arbitrator
	`, isAdmin, isModerator, isArbitrator, targetID).Scan(
		&cur.ID, &cur.Email, &cur.FullName, &cur.IsAdmin, &cur.IsModerator, &cur.IsArbitrator,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	cur.Roles = buildRoleList(userPublic{
		IsAdmin: cur.IsAdmin, IsModerator: cur.IsModerator, IsArbitrator: cur.IsArbitrator,
	})

	details := map[string]any{
		"targetUserId": targetID,
		"isAdmin":      isAdmin,
		"isModerator":  isModerator,
		"isArbitrator": isArbitrator,
	}
	if err := a.insertStaffAudit(ctx, actorID, "user_roles_updated", "user", targetID, details); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, cur)
}

func (a *api) listStaffAudit(c *echo.Context) error {
	ctx := c.Request().Context()
	rows, err := a.db.Query(ctx, `
		SELECT l.id, l.actor_user_id, u.full_name, l.action, l.entity_type, l.entity_id,
			l.details::text, l.created_at::text
		FROM staff_audit_log l
		JOIN app_users u ON u.id = l.actor_user_id
		ORDER BY l.created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	type entry struct {
		ID          int64  `json:"id"`
		ActorUserID int64  `json:"actorUserId"`
		ActorName   string `json:"actorName"`
		Action      string `json:"action"`
		EntityType  string `json:"entityType"`
		EntityID    int64  `json:"entityId"`
		Details     string `json:"details"`
		CreatedAt   string `json:"createdAt"`
	}
	var list []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.ActorName, &e.Action, &e.EntityType, &e.EntityID, &e.Details, &e.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		list = append(list, e)
	}
	if list == nil {
		list = []entry{}
	}
	return c.JSON(http.StatusOK, list)
}
