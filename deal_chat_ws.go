package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v5"
)

var dealChatUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func wsAuthUserID(c *echo.Context) (int64, error) {
	if t := c.QueryParam("token"); t != "" {
		return parseAccessToken(t)
	}
	return bearerUserID(c)
}

func (a *api) getDealMessagesWS(c *echo.Context) error {
	uid, err := wsAuthUserID(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	var exists bool
	if err := a.db.QueryRow(c.Request().Context(),
		`SELECT EXISTS(SELECT 1 FROM app_users WHERE id = $1)`, uid,
	).Scan(&exists); err != nil || !exists {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "session_invalid"})
	}

	dealID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || dealID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_id"})
	}
	ok, err := userCanAccessDeal(c.Request().Context(), a.db, dealID, uid)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "server_error"})
	}
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not_found"})
	}

	conn, err := dealChatUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	a.chatHub.register(dealID, conn)
	defer func() {
		a.chatHub.unregister(dealID, conn)
		_ = conn.Close()
	}()

	const (
		pongWait   = 60 * time.Second
		pingPeriod = 45 * time.Second
	)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	return nil
}
