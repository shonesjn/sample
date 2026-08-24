package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/BuzzingTaz/fw-edge-apps/internal/clientif"
	"github.com/BuzzingTaz/fw-edge-apps/internal/eventsingest"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

func HandleWebRTC(client *clientif.Client, w http.ResponseWriter, r *http.Request) {
	var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "error", err)
		return
	}
	client.ClientConn = clientConn
	slog.Info("Signalling WebSocket connection established for ", "userID", client.UserID)

	go client.ListenWebRTCSignalHandler()

	if err := client.InitializePC(); err != nil {
		slog.Error("Failed to establish PeerConnection", "error", err)
		return
	}
	slog.Info("WebRTC connection Initialized for ", "userID", client.UserID)

	if err := InitializeDataChannel(client); err != nil {
		slog.Error("Failed to create data channel", "error", err)
		return
	}

	if err := InitializeVideoTransceiver(client); err != nil {
		slog.Error("Failed to set OnTrack handler", "error", err)
		return
	}

	if err := client.EstablishConnection(); err != nil {
		slog.Error("Failed to establish WebRTC connection", "error", err)
		return
	}
	slog.Info("WebRTC connection established for ", "userID", client.UserID)
}

func InitializeDataChannel(client *clientif.Client) error {
	dataChannel, err := client.PeerConnection.CreateDataChannel("data", nil)
	if err != nil {
		return err
	}

	client.DataChannel = dataChannel

	dataChannel.OnOpen(func() {
		slog.Info("Data channel opened", "userID", client.UserID)
	})
	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		slog.Debug("Data channel message received", "userID", client.UserID, "message", string(msg.Data))
	})
	dataChannel.OnClose(func() {
		slog.Info("Data channel closed", "userID", client.UserID)
	})

	return nil
}

func InitializeVideoTransceiver(client *clientif.Client) error {
	if _, err := client.PeerConnection.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	); err != nil {
		return err
	}

	client.PeerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		slog.Info("Received remote track",
			"userID", client.UserID,
			"trackID", track.ID(),
			"trackKind", track.Kind().String())

		// PLI ticker to request keyframes
		go func() {
			ticker := time.NewTicker(time.Second * 5)
			defer ticker.Stop()
			for range ticker.C {
				err := client.PeerConnection.WriteRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{
						MediaSSRC: uint32(track.SSRC()),
					},
				})
				if err != nil {
					slog.Warn("Failed to send PLI, stopping", "error", err)
					return
				}
			}
		}()

		for {
			rtpPacket, _, readErr := track.ReadRTP()
			if readErr != nil {
				slog.Error("Failed to read RTP packet", "error", readErr)
				break
			}

			// NEW: Generate unique frame_id per RTP packet
			frameID := fmt.Sprintf("%s-%d-%s",
				client.StreamID,
				rtpPacket.Timestamp,
				uuid.New().String()[:8])

			tsToTaskIDMu.RLock()
			taskID, ok := tsToTaskIDMap[uint64(rtpPacket.Timestamp)]
			tsToTaskIDMu.RUnlock()

			if !ok {
				slog.Info("RTP packet with new timestamp received",
					"timestamp", rtpPacket.Timestamp,
					"frame_id", frameID)
				taskID = uuid.New().String()
				tsToTaskIDMu.Lock()
				tsToTaskIDMap[uint64(rtpPacket.Timestamp)] = taskID
				tsToTaskIDMu.Unlock()

				eventsingest.TransmitMeasureEvent(time.Now(), client.UserID, taskID, "clientif_new_rtp_received", map[string]string{
					"timestamp": strconv.FormatUint(uint64(rtpPacket.Timestamp), 10),
					"frame_id":  frameID,
				})
			}

			if rtpPacket.Marker {
				slog.Debug("RTP packet with Marker bit found",
					"timestamp", rtpPacket.Timestamp,
					"frame_id", frameID)
				eventsingest.TransmitMeasureEvent(time.Now(), client.UserID, taskID, "clientif_marker_rtp_received", map[string]string{
					"timestamp": strconv.FormatUint(uint64(rtpPacket.Timestamp), 10),
					"frame_id":  frameID,
				})
			}

			// NEW: Send via gRPC if enabled, else legacy WebSocket JSON
			if *useGRPC && client.SchedulerGRPCStream != nil {
				captureMs := time.Now().UnixMilli()
				err := client.SendFrameToScheduler(rtpPacket.Payload, captureMs)
				if err != nil {
					slog.Error("Failed to send frame via gRPC", "error", err, "frame_id", frameID)
				}
			} else {
				// Legacy WebSocket path: send raw RTP packet as JSON
				client.SchedulerConn.WriteJSON(rtpPacket)
			}

			slog.Debug("Read RTP packet",
				"timestamp", rtpPacket.Timestamp,
				"seq", rtpPacket.SequenceNumber,
				"marker", rtpPacket.Marker,
				"size", rtpPacket.MarshalSize(),
				"frame_id", frameID)
		}
	})

	return nil
}
