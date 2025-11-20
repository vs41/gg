// package models

// import (
// 	"encoding/json"
// 	"fmt"
// 	"sync"
// 	"time"

// 	"github.com/gofiber/fiber/v2/log"
// 	"github.com/gofiber/websocket/v2"
// 	"github.com/pion/rtcp"
// 	"github.com/pion/rtp"
// 	"github.com/pion/webrtc/v4"
// )

// // --- Global Variables ---
// var (
// 	listLock        sync.RWMutex
// 	gameConnections = map[string]map[string][]peerConnectionState{}
// 	gameTracks      = map[string]map[string]map[string]*webrtc.TrackLocalStaticRTP{}
// )

// // --- Structs ---
// type websocketMessage struct {
// 	Event string `json:"event"`
// 	Data  string `json:"data"`
// }

// type peerConnectionState struct {
// 	peerConnection *webrtc.PeerConnection
// 	websocket      *threadSafeWriter
// }

// // --- Peer Connection Helpers ---

// func addTrack(gameID, teamID string, t *webrtc.TrackRemote) *webrtc.TrackLocalStaticRTP {
// 	listLock.Lock()
// 	defer func() {
// 		listLock.Unlock()
// 		signalPeerConnectionsForGameTeam(gameID, teamID)
// 	}()

// 	if _, ok := gameTracks[gameID]; !ok {
// 		gameTracks[gameID] = map[string]map[string]*webrtc.TrackLocalStaticRTP{}
// 	}
// 	if _, ok := gameTracks[gameID][teamID]; !ok {
// 		gameTracks[gameID][teamID] = map[string]*webrtc.TrackLocalStaticRTP{}
// 	}

// 	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
// 	if err != nil {
// 		panic(err)
// 	}

// 	gameTracks[gameID][teamID][t.ID()] = trackLocal
// 	return trackLocal
// }

// func removeTrack(gameID, teamID string, t *webrtc.TrackLocalStaticRTP) {
// 	listLock.Lock()
// 	defer func() {
// 		listLock.Unlock()
// 		signalPeerConnectionsForGameTeam(gameID, teamID)
// 	}()
// 	delete(gameTracks[gameID][teamID], t.ID())
// }

// func signalPeerConnectionsForGameTeam(gameID, teamID string) {
// 	listLock.Lock()
// 	defer listLock.Unlock()

// 	attemptSync := func() (tryAgain bool) {
// 		pcs := gameConnections[gameID][teamID]
// 		tracks := gameTracks[gameID][teamID]
// 		fmt.Println("The size of the connection:", len(pcs), "track:", len(tracks), "GameID:", gameID, "TeamID:", teamID)

// 		for i := range pcs {
// 			pc := pcs[i].peerConnection
// 			ws := pcs[i].websocket

// 			if pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
// 				pcs = append(pcs[:i], pcs[i+1:]...)
// 				return true
// 			}

// 			existingSenders := map[string]bool{}
// 			for _, sender := range pc.GetSenders() {
// 				if sender.Track() == nil {
// 					continue
// 				}
// 				existingSenders[sender.Track().ID()] = true
// 				if _, ok := tracks[sender.Track().ID()]; !ok {
// 					if err := pc.RemoveTrack(sender); err != nil {
// 						return true
// 					}
// 				}
// 			}

// 			for _, receiver := range pc.GetReceivers() {
// 				if receiver.Track() != nil {
// 					existingSenders[receiver.Track().ID()] = true
// 				}
// 			}

// 			for trackID := range tracks {
// 				if _, ok := existingSenders[trackID]; !ok {
// 					if _, err := pc.AddTrack(tracks[trackID]); err != nil {
// 						return true
// 					}
// 				}
// 			}

// 			offer, err := pc.CreateOffer(nil)
// 			if err != nil || pc.SetLocalDescription(offer) != nil {
// 				return true
// 			}

// 			offerJSON, err := json.Marshal(offer)
// 			if err != nil {
// 				log.Errorf("Failed to marshal offer to json: %v", err)
// 				return true
// 			}

// 			if err = ws.WriteJSON(&websocketMessage{
// 				Event: "offer",
// 				Data:  string(offerJSON),
// 			}); err != nil {
// 				return true
// 			}
// 		}
// 		return tryAgain
// 	}

// 	for syncAttempt := 0; ; syncAttempt++ {
// 		if syncAttempt == 25 {
// 			go func() {
// 				time.Sleep(time.Second * 3)
// 				signalPeerConnectionsForGameTeam(gameID, teamID)
// 			}()
// 			return
// 		}
// 		if !attemptSync() {
// 			break
// 		}
// 	}
// }

// func DispatchAllKeyFrames() {
// 	listLock.Lock()
// 	defer listLock.Unlock()

// 	for _, teamMap := range gameConnections {
// 		for _, pcs := range teamMap {
// 			for _, pcState := range pcs {
// 				for _, receiver := range pcState.peerConnection.GetReceivers() {
// 					if receiver.Track() != nil {
// 						_ = pcState.peerConnection.WriteRTCP([]rtcp.Packet{
// 							&rtcp.PictureLossIndication{MediaSSRC: uint32(receiver.Track().SSRC())},
// 						})
// 					}
// 				}
// 			}
// 		}
// 	}
// }

// // --- Fiber WebSocket Handler ---

// func WebsocketHandler(c *websocket.Conn) {
// 	gameID := c.Query("gameID")
// 	teamID := c.Query("teamID")
// 	if gameID == "" || teamID == "" {
// 		c.WriteMessage(websocket.TextMessage, []byte("gameID and teamID required"))
// 		c.Close()
// 		return
// 	}

// 	tsWriter := &threadSafeWriter{Conn: c}

// 	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
// 	if err != nil {
// 		log.Errorf("Failed to create PeerConnection: %v", err)
// 		return
// 	}
// 	defer pc.Close()

// 	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio} {
// 		pc.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
// 			Direction: webrtc.RTPTransceiverDirectionRecvonly,
// 		})
// 	}

// 	listLock.Lock()
// 	if _, ok := gameConnections[gameID]; !ok {
// 		gameConnections[gameID] = map[string][]peerConnectionState{}
// 	}
// 	gameConnections[gameID][teamID] = append(gameConnections[gameID][teamID], peerConnectionState{pc, tsWriter})
// 	listLock.Unlock()

// 	pc.OnICECandidate(func(i *webrtc.ICECandidate) {
// 		if i == nil {
// 			return
// 		}
// 		candidateJSON, err := json.Marshal(i.ToJSON())
// 		if err == nil {
// 			tsWriter.WriteJSON(&websocketMessage{
// 				Event: "candidate",
// 				Data:  string(candidateJSON),
// 			})
// 		}
// 	})

// 	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
// 		log.Infof("Connection state: %s", s)
// 		switch s {
// 		case webrtc.PeerConnectionStateFailed:
// 			pc.Close()
// 		case webrtc.PeerConnectionStateClosed:
// 			signalPeerConnectionsForGameTeam(gameID, teamID)
// 		}
// 	})

// 	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
// 		log.Infof("Got remote track: Kind=%s, ID=%s", t.Kind(), t.ID())
// 		trackLocal := addTrack(gameID, teamID, t)
// 		defer removeTrack(gameID, teamID, trackLocal)

// 		buf := make([]byte, 1500)
// 		rtpPkt := &rtp.Packet{}
// 		for {
// 			n, _, err := t.Read(buf)
// 			if err != nil {
// 				return
// 			}
// 			if err := rtpPkt.Unmarshal(buf[:n]); err != nil {
// 				log.Errorf("Failed to unmarshal RTP: %v", err)
// 				return
// 			}
// 			rtpPkt.Extension = false
// 			rtpPkt.Extensions = nil
// 			if err := trackLocal.WriteRTP(rtpPkt); err != nil {
// 				return
// 			}
// 		}
// 	})

// 	signalPeerConnectionsForGameTeam(gameID, teamID)

// 	message := &websocketMessage{}
// 	for {
// 		_, raw, err := c.ReadMessage()
// 		if err != nil {
// 			log.Errorf("Read message failed: %v", err)
// 			return
// 		}

// 		if err := json.Unmarshal(raw, &message); err != nil {
// 			log.Errorf("Failed to unmarshal message: %v", err)
// 			return
// 		}

// 		switch message.Event {
// 		case "candidate":
// 			cand := webrtc.ICECandidateInit{}
// 			if err := json.Unmarshal([]byte(message.Data), &cand); err == nil {
// 				pc.AddICECandidate(cand)
// 			}
// 		case "answer":
// 			answer := webrtc.SessionDescription{}
// 			if err := json.Unmarshal([]byte(message.Data), &answer); err == nil {
// 				pc.SetRemoteDescription(answer)
// 			}
// 		default:
// 			log.Errorf("Unknown message: %+v", message)
// 		}
// 	}
// }

// // --- Thread Safe Writer ---

// type threadSafeWriter struct {
// 	*websocket.Conn
// 	sync.Mutex
// }

// func (t *threadSafeWriter) WriteJSON(v any) error {
// 	t.Lock()
// 	defer t.Unlock()
// 	return t.Conn.WriteJSON(v)
// }

package sever

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2/log"
	"github.com/gofiber/websocket/v2"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// --- Global Variables ---
var (
	listLock        sync.RWMutex
	gameConnections = map[string]map[string][]peerConnectionState{}

	// 🔹 Modified: now stores username + track
	gameTracks            = map[string]map[string]map[string]userTrack{}
	trackToPeerConnection = make(map[string]map[string]map[*webrtc.TrackLocalStaticRTP]bool)
	mapForPeerConnection  = make(map[*webrtc.PeerConnection]string)
)

// 🔹 Added struct for track info
type userTrack struct {
	Username string
	Track    *webrtc.TrackLocalStaticRTP
}

// --- Structs ---
type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
	User  string `json:"user,omitempty"` // 🔹 Added: who the message came from
}

type peerConnectionState struct {
	peerConnection *webrtc.PeerConnection
	websocket      *threadSafeWriter
}

// --- Peer Connection Helpers ---

// 🔹 Updated: addTrack now takes username
func addTrack(gameID, teamID, username string, t *webrtc.TrackRemote, pc *webrtc.PeerConnection) *webrtc.TrackLocalStaticRTP {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnectionsForGameTeam(gameID, teamID, username)
	}()

	if _, ok := gameTracks[gameID]; !ok {
		gameTracks[gameID] = map[string]map[string]userTrack{}
	}
	if _, ok := gameTracks[gameID][teamID]; !ok {
		gameTracks[gameID][teamID] = map[string]userTrack{}
	}

	// ✅ Initialize trackToPeerConnection properly
	if _, ok := trackToPeerConnection[gameID]; !ok {
		trackToPeerConnection[gameID] = make(map[string]map[*webrtc.TrackLocalStaticRTP]bool)
	}
	if _, ok := trackToPeerConnection[gameID][teamID]; !ok {
		trackToPeerConnection[gameID][teamID] = make(map[*webrtc.TrackLocalStaticRTP]bool)
	}

	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
	if err != nil {
		panic(err)
	}

	gameTracks[gameID][teamID][t.ID()] = userTrack{
		Username: username,
		Track:    trackLocal,
	}

	// ✅ Now it's safe to assign
	trackToPeerConnection[gameID][teamID][trackLocal] = false

	return trackLocal
}

// 🔹 Updated: removeTrack still works the same, only map type changed
func removeTrack(gameID, teamID string, t *webrtc.TrackLocalStaticRTP, username string) {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnectionsForGameTeam(gameID, teamID, username)
	}()
	delete(gameTracks[gameID][teamID], t.ID())
}
func signalPeerConnectionsForGameTeam(gameID, teamID, username string) {
	listLock.Lock()
	defer listLock.Unlock()

	fmt.Println("[INFO] Start the process for username:", username)

	attemptSync := func() (tryAgain bool) {
		pcs := gameConnections[gameID][teamID]
		tracks := gameTracks[gameID][teamID]

		fmt.Printf("[INFO] Connections count: %d, Tracks count: %d, GameID: %s, TeamID: %s\n",
			len(pcs), len(tracks), gameID, teamID)

		for i := 0; i < len(pcs); i++ {
			fmt.Printf("[INFO] Processing connection index %d for user %s\n", i, username)

			pc := pcs[i].peerConnection
			ws := pcs[i].websocket

			fmt.Printf("[DEBUG] PeerConnection state: %s\n", pc.ConnectionState().String())
			if pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				fmt.Println("[WARN] PeerConnection is closed, removing from list")
				pcs = append(pcs[:i], pcs[i+1:]...)
				gameConnections[gameID][teamID] = pcs
				fmt.Println("[INFO] Updated connections list after removal:", gameConnections[gameID][teamID])
				return true
			}

			existingSenders := map[string]bool{}

			// ----- existing senders -----
			fmt.Println("[DEBUG] Checking existing senders...")
			for _, sender := range pc.GetSenders() {
				if sender.Track() == nil {
					continue
				}
				existingSenders[sender.Track().ID()] = true

				if _, ok := tracks[sender.Track().ID()]; !ok {
					fmt.Printf("[INFO] Removing sender track ID %s as it no longer exists in tracks\n", sender.Track().ID())
					if err := pc.RemoveTrack(sender); err != nil {
						fmt.Println("[ERROR] Failed to remove track:", err)
						return true
					}
				}
			}

			// ----- existing receivers -----
			fmt.Println("[DEBUG] Checking existing receivers...")
			for _, receiver := range pc.GetReceivers() {
				if receiver.Track() != nil {
					existingSenders[receiver.Track().ID()] = true
					fmt.Printf("[DEBUG] Receiver track ID: %s\n", receiver.Track().ID())
				}
			}

			// ----- add missing tracks -----
			fmt.Println("[DEBUG] Adding missing tracks...")
			newUsers := map[string]bool{} // users whose tracks were newly added

			// if len(existingSenders) == len(tracks) {
			// 	return false
			// }

			for trackID, trackInfo := range tracks {
				if _, ok := existingSenders[trackID]; !ok {
					fmt.Printf("[INFO] Adding track ID %s for username %s\n", trackID, trackInfo.Username)

					if _, err := pc.AddTrack(trackInfo.Track); err != nil {
						fmt.Println("[ERROR] Failed to add track:", err)
						return true
					}

					newUsers[trackInfo.Username] = true
				} else {
					fmt.Printf("[DEBUG] Track ID %s already exists in PeerConnection\n", trackID)
				}
			}

			// ----- always send at least one offer -----
			if len(newUsers) == 0 {
				newUsers[username] = true // ensure at least one offer gets sent
			}

			offer, err := pc.CreateOffer(nil)
			if err != nil {
				fmt.Println("[ERROR] Failed to create offer:", err)
				return true
			}

			if err := pc.SetLocalDescription(offer); err != nil {
				fmt.Println("[ERROR] Failed to set local description:", err)
				return true
			}

			offerJSON, err := json.Marshal(offer)
			if err != nil {
				fmt.Println("[ERROR] Failed to marshal offer to JSON:", err)
				return true
			}

			// ----- send offers per user -----
			for remoteUser := range newUsers {
				fmt.Printf("[DEBUG] Creating offer for PeerConnection (user: %s)\n", remoteUser)

				fmt.Println("[DEBUG] Preparing WebSocket message...")
				fmt.Printf("[INFO] Sending offer for user: %s\n", remoteUser)

				if err := ws.WriteJSON(&websocketMessage{
					Event: "offer",
					Data:  string(offerJSON),
					User:  remoteUser,
				}); err != nil {
					fmt.Println("[ERROR] Failed to send offer over WebSocket:", err)
					return true
				}

				fmt.Println("[INFO] Offer successfully sent for user:", remoteUser)
			}
		}

		return false
	}

	// ----- retry loop -----
	for syncAttempt := 0; ; syncAttempt++ {
		fmt.Printf("[INFO] Sync attempt #%d for user %s\n", syncAttempt+1, username)

		if syncAttempt == 25 {
			fmt.Println("[WARN] Max sync attempts reached, retrying in 3 seconds...")
			go func() {
				time.Sleep(3 * time.Second)
				signalPeerConnectionsForGameTeam(gameID, teamID, username)
			}()
			return
		}

		if !attemptSync() {
			fmt.Println("[INFO] Sync successful, exiting attempt loop")
			fmt.Println("                                                               ")
			fmt.Println("                                                               ")
			fmt.Println("                                                               ")
			fmt.Println("                                                               ")
			fmt.Println("                                                               ")
			break
		} else {
			fmt.Println("[INFO] Retry required due to previous error or closed connection")
		}
	}

	fmt.Println("[INFO] Completed signaling process for user:", username)
}

func DispatchAllKeyFrames() {
	listLock.Lock()
	defer listLock.Unlock()

	for _, teamMap := range gameConnections {
		for _, pcs := range teamMap {
			for _, pcState := range pcs {
				for _, receiver := range pcState.peerConnection.GetReceivers() {
					if receiver.Track() != nil {
						_ = pcState.peerConnection.WriteRTCP([]rtcp.Packet{
							&rtcp.PictureLossIndication{MediaSSRC: uint32(receiver.Track().SSRC())},
						})
					}
				}
			}
		}
	}
}

// --- Fiber WebSocket Handler ---

func WebsocketHandler(c *websocket.Conn) {
	gameID := c.Query("gameID")
	teamID := c.Query("teamID")
	username := c.Query("username") // 🔹 Added username from query

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
	mapForPeerConnection[pc] = username
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
		if err == nil {
			tsWriter.WriteJSON(&websocketMessage{
				Event: "candidate",
				Data:  string(candidateJSON),
				User:  username, // 🔹 send username with ICE candidate too
			})
		}
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Infof("Connection state: %s", s)
		switch s {
		case webrtc.PeerConnectionStateFailed:
			pc.Close()
		case webrtc.PeerConnectionStateClosed:
			signalPeerConnectionsForGameTeam(gameID, teamID, username)
		}
	})

	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Infof("Got remote track from user %s: Kind=%s, ID=%s", username, t.Kind(), t.ID())
		trackLocal := addTrack(gameID, teamID, username, t, pc) // 🔹 Pass username
		defer removeTrack(gameID, teamID, trackLocal, username)

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

	signalPeerConnectionsForGameTeam(gameID, teamID, username)

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
			if err := json.Unmarshal([]byte(message.Data), &cand); err == nil {
				pc.AddICECandidate(cand)
			}
		case "answer":
			answer := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &answer); err == nil {
				pc.SetRemoteDescription(answer)
			}
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
