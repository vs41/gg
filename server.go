// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

var (
	indexTemplate = &template.Template{}

	listLock sync.RWMutex
	//addr     = flag.String("addr", ":8080", "http service address")
	// Nested maps: gameID -> teamID -> connections/tracks
	gameConnections = map[string]map[string][]peerConnectionState{}
	gameTracks      = map[string]map[string]map[string]*webrtc.TrackLocalStaticRTP{}

	log = logging.NewDefaultLoggerFactory().NewLogger("sfu-fiber")
)

type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

type peerConnectionState struct {
	peerConnection *webrtc.PeerConnection
	websocket      *threadSafeWriter
}

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

		wsURL := "wss://" + c.Hostname() + "/websocket?gameID=" + gameID + "&teamID=" + teamID

		var buf bytes.Buffer
		if err := indexTemplate.Execute(&buf, wsURL); err != nil {
			return err
		}

		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.Send(buf.Bytes())
	})

	// WebSocket handler
	app.Get("/websocket", websocket.New(websocketHandler))

	// Periodically request keyframes
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			dispatchAllKeyFrames()
		}
	}()

	tlsCert := "cert.pem"
	tlsKey := "key.pem"
	port := os.Getenv("PORT")
	if port == "" {
		port = "10000"
	}

	if err := app.ListenTLS("https://127.0.0.1:"+port, tlsCert, tlsKey); err != nil {
		log.Errorf("Failed to start Fiber server: %v", err)
	}
}

// --- Peer Connection Helpers ---

func addTrack(gameID, teamID string, t *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnectionsForGameTeam(gameID, teamID)
	}()

	if _, ok := gameTracks[gameID]; !ok {
		gameTracks[gameID] = map[string]map[string]*webrtc.TrackLocalStaticRTP{}
	}
	if _, ok := gameTracks[gameID][teamID]; !ok {
		gameTracks[gameID][teamID] = map[string]*webrtc.TrackLocalStaticRTP{}
	}

	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
	if err != nil {
		panic(err)
	}

	gameTracks[gameID][teamID][t.ID()] = trackLocal
	return trackLocal
}

func removeTrack(gameID, teamID string, t *webrtc.TrackLocalStaticRTP) {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnectionsForGameTeam(gameID, teamID)
	}()
	delete(gameTracks[gameID][teamID], t.ID())
}

func signalPeerConnectionsForGameTeam(gameID, teamID string) {
	listLock.Lock()
	defer listLock.Unlock()
	attemptSync := func() (tryAgain bool) {
		pcs := gameConnections[gameID][teamID]
		tracks := gameTracks[gameID][teamID]
		fmt.Println("The size of the connection: ", len(pcs), " track: ", len(tracks))
		for i := range pcs {
			pc := pcs[i].peerConnection
			ws := pcs[i].websocket
			if pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				pcs = append(pcs[:i], pcs[i+1:]...)

				return true // We modified the slice, start from the beginning
			}
			existingSenders := map[string]bool{}
			for _, sender := range pc.GetSenders() {
				if sender.Track() == nil {
					continue
				}
				existingSenders[sender.Track().ID()] = true
				if _, ok := tracks[sender.Track().ID()]; !ok {
					//pc.RemoveTrack(sender)
					if err := pc.RemoveTrack(sender); err != nil {
						return true
					}
				}
			}

			for _, receiver := range pc.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			for trackID := range tracks {
				if _, ok := existingSenders[trackID]; !ok {
					//pc.AddTrack(tracks[trackID])
					if _, err := pc.AddTrack(tracks[trackID]); err != nil {
						return true
					}
				}
			}

			offer, err := pc.CreateOffer(nil)
			if err != nil {
				return true
			}

			if err = pc.SetLocalDescription(offer); err != nil {
				return true
			}

			//pc.SetLocalDescription(offer)

			offerJSON, err := json.Marshal(offer)
			if err != nil {
				log.Errorf("Failed to marshal offer to json: %v", err)

				return true
			}

			// ws.WriteJSON(&websocketMessage{
			// 	Event: "offer",
			// 	Data:  string(offerJSON),
			// })

			if err = ws.WriteJSON(&websocketMessage{
				Event: "offer",
				Data:  string(offerJSON),
			}); err != nil {
				return true
			}

		}
		return tryAgain
	}

	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			// Release the lock and attempt a sync in 3 seconds. We might be blocking a RemoveTrack or AddTrack
			go func() {
				time.Sleep(time.Second * 3)
				signalPeerConnectionsForGameTeam(gameID, teamID)
			}()

			return
		}

		if !attemptSync() {
			break
		}
	}
}

func dispatchAllKeyFrames() {
	listLock.Lock()
	defer listLock.Unlock()

	for _, teamMap := range gameConnections {
		for _, pcs := range teamMap {
			for _, pcState := range pcs {
				for _, receiver := range pcState.peerConnection.GetReceivers() {
					if receiver.Track() == nil {
						continue
					}
					_ = pcState.peerConnection.WriteRTCP([]rtcp.Packet{
						&rtcp.PictureLossIndication{MediaSSRC: uint32(receiver.Track().SSRC())},
					})
				}
			}
		}
	}
}

// --- Fiber WebSocket Handler ---

func websocketHandler(c *websocket.Conn) {
	gameID := c.Query("gameID")
	teamID := c.Query("teamID")
	if gameID == "" || teamID == "" {
		c.WriteMessage(websocket.TextMessage, []byte("gameID and teamID required"))
		c.Close()
		return
	}

	tsWriter := &threadSafeWriter{Conn: c}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		log.Errorf("Failed to create PeerConnection: %v", err)
		return
	}
	defer pc.Close()

	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio} {
		pc.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
	}

	listLock.Lock()
	if _, ok := gameConnections[gameID]; !ok {
		gameConnections[gameID] = map[string][]peerConnectionState{}
	}
	gameConnections[gameID][teamID] = append(gameConnections[gameID][teamID], peerConnectionState{pc, tsWriter})
	listLock.Unlock()

	pc.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		candidateJSON, err := json.Marshal(i.ToJSON())
		if err != nil {
			tsWriter.WriteJSON(&websocketMessage{
				Event: "Error in  the OnICECandidate",
				Data:  string(candidateJSON),
			})
		}
		tsWriter.WriteJSON(&websocketMessage{
			Event: "candidate",
			Data:  string(candidateJSON),
		})
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Infof("Connection state: %s", s)
		// if s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateFailed {
		// 	signalPeerConnectionsForGameTeam(gameID, teamID)
		// }

		log.Infof("Connection state change: %s", pc)

		switch s {
		case webrtc.PeerConnectionStateFailed:
			if err := pc.Close(); err != nil {
				log.Errorf("Failed to close PeerConnection: %v", err)
			}
		case webrtc.PeerConnectionStateClosed:
			signalPeerConnectionsForGameTeam(gameID, teamID)
		default:
		}

	})

	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Infof("Got remote track: Kind=%s, ID=%s", t.Kind(), t.ID())
		trackLocal := addTrack(gameID, teamID, t)
		defer removeTrack(gameID, teamID, trackLocal)

		buf := make([]byte, 1500)
		rtpPkt := &rtp.Packet{}
		for {
			n, _, err := t.Read(buf)
			if err != nil {
				return
			}
			if err := rtpPkt.Unmarshal(buf[:n]); err != nil {
				log.Errorf("Failed to unmarshal RTP: %v", err)
				return
			}
			rtpPkt.Extension = false
			rtpPkt.Extensions = nil
			if err := trackLocal.WriteRTP(rtpPkt); err != nil {
				return
			}
		}
	})

	pc.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		log.Infof("ICE state: %s", is)
	})

	signalPeerConnectionsForGameTeam(gameID, teamID)

	message := &websocketMessage{}
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			log.Errorf("Read message failed: %v", err)
			return
		}

		if err := json.Unmarshal(raw, &message); err != nil {
			log.Errorf("Failed to unmarshal message: %v", err)
			return
		}

		switch message.Event {
		case "candidate":
			cand := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &cand); err != nil {
				log.Errorf("Failed to unmarshal candidate: %v", err)
				return
			}
			pc.AddICECandidate(cand)
		case "answer":
			answer := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &answer); err != nil {
				log.Errorf("Failed to unmarshal answer: %v", err)
				return
			}
			pc.SetRemoteDescription(answer)
		default:
			log.Errorf("Unknown message: %+v", message)
		}
	}
}

// --- Thread Safe Writer ---

type threadSafeWriter struct {
	*websocket.Conn
	sync.Mutex
}

func (t *threadSafeWriter) WriteJSON(v any) error {
	t.Lock()
	defer t.Unlock()
	return t.Conn.WriteJSON(v)
}
