// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package main

import (
	"bytes"
	"os"
	"strconv"
	"text/template"
	"time"
	"webrtc/sever"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/pion/logging"
)

var (
	indexTemplate = &template.Template{}
	log           = logging.NewDefaultLoggerFactory().NewLogger("sfu-fiber")
)

func main() {
	// Read index.html
	indexHTML, err := os.ReadFile("index.html")
	if err != nil {
		panic(err)
	}
	indexTemplate = template.Must(template.New("").Parse(string(indexHTML)))

	app := fiber.New()

	// Serve index.html
	app.Get("/", func(c *fiber.Ctx) error {
		gameID := c.Query("gameID")
		teamID := c.Query("teamID")
		username := c.Query("username")
		unitID := c.Query("unitID")

		wsURL := "wss://" + c.Hostname() + "/websocket?gameID=" + gameID + "&teamID=" + teamID + "&username=" + username + "&unitID=" + unitID + "&radioRange=" + strconv.Itoa(28)

		var buf bytes.Buffer
		if err := indexTemplate.Execute(&buf, wsURL); err != nil {
			return err
		}

		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.Send(buf.Bytes())
	})

	// WebSocket handler
	app.Get("/websocket", websocket.New(sever.WebsocketHandler()))

	// Periodically request keyframes
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			sever.DispatchAllKeyFrames()
		}
	}()

	// tlsCert := "cert.pem"
	// tlsKey := "key.pem"
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// if err := app.ListenTLS(":"+port, tlsCert, tlsKey); err != nil {
	// 	log.Errorf("Failed to start Fiber server: %v", err)
	// }
	if err := app.Listen(":" + port); err != nil {
		log.Errorf("Failed to start Fiber server: %v", err)
	}
}
