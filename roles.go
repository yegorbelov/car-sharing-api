package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
)

const echoUserRolesKey = "authUserRoles"

type userRoles struct {
	IsAdmin      bool `json:"isAdmin"`
	IsModerator  bool `json:"isModerator"`
	IsArbitrator bool `json:"isArbitrator"`
}

func (r userRoles) staff() bool {
	return r.IsAdmin || r.IsModerator || r.IsArbitrator
}

func (a *api) loadUserRoles(ctx context.Context, userID int64) (userRoles, error) {
	var r userRoles
	err := a.db.QueryRow(ctx, `
		SELECT is_admin, is_moderator, is_arbitrator FROM app_users WHERE id = $1
	`, userID).Scan(&r.IsAdmin, &r.IsModerator, &r.IsArbitrator)
	return r, err
}

func (a *api) scanUserPublic(ctx context.Context, row pgx.Row) (userPublic, error) {
	var u userPublic
	err := row.Scan(
		&u.ID, &u.Email, &u.FullName, &u.AvatarURL,
		&u.IsAdmin, &u.IsModerator, &u.IsArbitrator,
	)
	if err != nil {
		return u, err
	}
	u.Roles = buildRoleList(u)
	return u, nil
}

func buildRoleList(u userPublic) []string {
	var roles []string
	// Owner and renter are contextual; staff roles are explicit in RBAC.
	if u.IsAdmin {
		roles = append(roles, "admin")
	}
	if u.IsModerator {
		roles = append(roles, "moderator")
	}
	if u.IsArbitrator {
		roles = append(roles, "arbitrator")
	}
	if len(roles) == 0 {
		roles = []string{"user"}
	}
	return roles
}

func (a *api) insertStaffAudit(ctx context.Context, actorID int64, action, entityType string, entityID int64, details map[string]any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `
		INSERT INTO staff_audit_log (actor_user_id, action, entity_type, entity_id, details)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`, actorID, action, entityType, entityID, string(raw))
	return err
}

func (a *api) requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return a.requireStaffRole("admin", func(r userRoles) bool { return r.IsAdmin }, next)
}

func (a *api) requireModerator(next echo.HandlerFunc) echo.HandlerFunc {
	return a.requireStaffRole("moderator", func(r userRoles) bool { return r.IsAdmin || r.IsModerator }, next)
}

func (a *api) requireArbitrator(next echo.HandlerFunc) echo.HandlerFunc {
	return a.requireStaffRole("arbitrator", func(r userRoles) bool { return r.IsAdmin || r.IsArbitrator }, next)
}

func (a *api) requireStaffRole(name string, ok func(userRoles) bool, next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		uid := authUserID(c)
		if uid == 0 {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}
		roles, err := a.loadUserRoles(c.Request().Context(), uid)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if !ok(roles) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden", "role": name})
		}
		c.Set(echoUserRolesKey, roles)
		return next(c)
	}
}

func authUserRoles(c *echo.Context) userRoles {
	v, _ := c.Get(echoUserRolesKey).(userRoles)
	return v
}
