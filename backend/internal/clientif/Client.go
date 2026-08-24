package clientif

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	pb "github.com/BuzzingTaz/fw-edge-apps/proto"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client represents a connected user
type Client struct {
	UserID        string
	Protocol      string
	ClientConn    *websocket.Conn
	SchedulerConn *websocket.Conn
	// NEW: gRPC connection to scheduler (replaces WebSocket for frame streaming)
	SchedulerGRPCConn        *grpc.ClientConn
	SchedulerGRPCStream      pb.SchedulerStream_StreamFramesClient
	PeerConnection           *webrtc.PeerConnection
	DataChannel              *webrtc.DataChannel
	SchedulerListenerHandler func(ProcessedDataMessage)
	Mutex                    sync.Mutex
	SchedulerURL             string
	StreamID                 string // NEW: unique stream identifier
}

// WebRTCSignal for signaling handshake
type WebRTCSignal struct {
	Type string                   `json:"type"` // "offer", "answer", "candidate"
	SDP  string                   `json:"sdp,omitempty"`
	ICE  *webrtc.ICECandidateInit `json:"candidate,omitempty"`
}

type ClientWsMessage struct {
	Signal *WebRTCSignal `json:"webrtc_signal,omitempty"`
}

// ProcessedDataMessage now matches the new InferenceResult proto fields
// TODO: migrate fully to protobuf deserialization
type ProcessedDataMessage struct {
	Timestamp        uint64 `json:"timestamp"`
	ProcessingStatus int    `json:"processing_status"`
	Detections       []struct {
		X          int     `json:"x"`
		Y          int     `json:"y"`
		Dx         int     `json:"dx"`
		Dy         int     `json:"dy"`
		Label      string  `json:"label"`
		Confidence float64 `json:"confidence"`
	} `json:"detections"`
	// NEW fields from updated proto
	FrameID            string  `json:"frame_id"`
	InferenceLatencyMs float64 `json:"inference_latency_ms"`
	StreamID           string  `json:"stream_id"`
	NodeName           string  `json:"node_name"`
	// Stage timestamps for end-to-end latency tracking
	StageTS struct {
		ClientCaptureMs          int64 `json:"client_capture_ms"`
		SchedulerReceiveMs       int64 `json:"scheduler_receive_ms"`
		SchedulerDecisionMs      int64 `json:"scheduler_decision_ms"`
		ComputeReceiveMs         int64 `json:"compute_receive_ms"`
		UDPSendMs                int64 `json:"udp_send_ms"`
		PythonReceiveMs          int64 `json:"python_receive_ms"`
		YOLOInferenceStartMs     int64 `json:"yolo_inference_start_ms"`
		YOLOInferenceEndMs       int64 `json:"yolo_inference_end_ms"`
		PythonGRPCSendMs         int64 `json:"python_grpc_send_ms"`
		ComputeReceiveResultMs   int64 `json:"compute_receive_result_ms"`
		SchedulerReceiveResultMs int64 `json:"scheduler_receive_result_ms"`
		FrontendSendMs           int64 `json:"frontend_send_ms"`
	} `json:"stage_ts"`
}

func (client *Client) WriteSignalJSON(v ClientWsMessage) error {
	client.Mutex.Lock()
	defer client.Mutex.Unlock()
	return client.ClientConn.WriteJSON(v)
}

func (client *Client) InitializePC() error {
	var err error
	if client.Protocol != "webrtc" {
		slog.Error("InitiatePC called with unsupported protocol", "protocol", client.Protocol)
		return errors.New("Unsupported protocol: " + client.Protocol)
	}

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	client.PeerConnection, err = webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}

	client.PeerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidate := c.ToJSON()
		signal := ClientWsMessage{
			Signal: &WebRTCSignal{
				Type: "candidate",
				ICE:  &candidate,
			},
		}
		if writeErr := client.WriteSignalJSON(signal); writeErr != nil {
			slog.Error("Failed to send ICE candidate", "error", writeErr)
		} else {
			slog.Info("Sent ICE candidate", "userID", client.UserID)
		}
	})

	client.PeerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		slog.Info("PeerConnection State Change", "state", s.String(), "userID", client.UserID)
	})

	return nil
}

func (client *Client) EstablishConnection() error {
	offer, err := client.PeerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err = client.PeerConnection.SetLocalDescription(offer); err != nil {
		return err
	}

	offerMsg := ClientWsMessage{
		Signal: &WebRTCSignal{
			Type: "offer",
			SDP:  offer.SDP,
		},
	}
	return client.WriteSignalJSON(offerMsg)
}

func (client *Client) ListenWebRTCSignalHandler() {
	var message ClientWsMessage
	for {
		err := client.ClientConn.ReadJSON(&message)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Error("WS closed unexpectedly", "err", err, "userID", client.UserID)
			}
			break
		}

		if message.Signal != nil {
			slog.Info("Received WebRTC signal", "type", message.Signal.Type, "userID", client.UserID)
			messageSignal := message.Signal

			switch messageSignal.Type {
			case "answer":
				slog.Debug("Received answer from client", "userID", client.UserID)
				answer := webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  messageSignal.SDP,
				}
				if err := client.PeerConnection.SetRemoteDescription(answer); err != nil {
					slog.Error("SetRemoteDescription failed", "error", err)
				} else {
					slog.Debug("Set remote description with answer", "userID", client.UserID)
				}
			case "candidate":
				slog.Debug("Received ICE candidate from client", "userID", client.UserID)
				if messageSignal.ICE != nil {
					if err := client.PeerConnection.AddICECandidate(*messageSignal.ICE); err != nil {
						slog.Error("AddICECandidate failed", "error", err)
					} else {
						slog.Debug("Added ICE candidate", "userID", client.UserID)
					}
				} else {
					slog.Error("Received nil ICE candidate", "userID", client.UserID)
				}
			default:
				slog.Warn("Unknown message type", "type", messageSignal.Type)
			}
		}
	}
}

// ConnectScheduler establishes WebSocket connection to scheduler (legacy path)
// For gRPC migration, use ConnectSchedulerGRPC instead
func (client *Client) ConnectScheduler() error {
	url := client.SchedulerURL
	if url == "" {
		url = "ws://localhost:9998"
	}
	schedulerConn, _, err := websocket.DefaultDialer.Dial(url+"/ws/"+client.UserID, nil)
	if err != nil {
		return err
	}
	client.Mutex.Lock()
	client.SchedulerConn = schedulerConn
	client.Mutex.Unlock()

	// Generate unique stream ID for this client session
	client.StreamID = fmt.Sprintf("stream-%s-%s", client.UserID, uuid.New().String()[:8])

	slog.Info("Connected to scheduler WebSocket", "userID", client.UserID, "url", url, "streamID", client.StreamID)
	return nil
}

// ConnectSchedulerGRPC establishes gRPC connection to scheduler (NEW)
// This replaces WebSocket for frame streaming while keeping WebSocket for results
func (client *Client) ConnectSchedulerGRPC(schedulerAddr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, schedulerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to scheduler gRPC: %w", err)
	}

	client.SchedulerGRPCConn = conn
	schedulerClient := pb.NewSchedulerStreamClient(conn)
	stream, err := schedulerClient.StreamFrames(context.Background())
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to open StreamFrames: %w", err)
	}

	client.SchedulerGRPCStream = stream
	client.StreamID = fmt.Sprintf("stream-%s-%s", client.UserID, uuid.New().String()[:8])

	slog.Info("Connected to scheduler gRPC", "userID", client.UserID, "addr", schedulerAddr, "streamID", client.StreamID)
	return nil
}

// SendFrameToScheduler sends a JPEG frame to the scheduler via gRPC (NEW)
func (client *Client) SendFrameToScheduler(jpegData []byte, captureTimestampMs int64) error {
	if client.SchedulerGRPCStream == nil {
		return errors.New("no gRPC stream to scheduler")
	}

	frameID := fmt.Sprintf("%s-%d", client.StreamID, time.Now().UnixNano())

	frame := &pb.ClientFrame{
		JpegData:           jpegData,
		StreamId:           client.StreamID,
		FrameId:            frameID,
		CaptureTimestampMs: captureTimestampMs,
		StageTs: &pb.StageTimestamps{
			ClientCaptureMs: captureTimestampMs,
		},
	}

	return client.SchedulerGRPCStream.Send(frame)
}

// SendDataToClient sends a generic message to the client via the WebRTC data channel
func SendDataToClient[T any](client *Client, message T) error {
	if client.Protocol != "webrtc" {
		return errors.New("Unsupported protocol: " + client.Protocol)
	}
	if client.PeerConnection == nil {
		return errors.New("PeerConnection not established")
	}
	if client.DataChannel == nil {
		return errors.New("DataChannel not established")
	}
	if client.DataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("DataChannel not open")
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return client.DataChannel.SendText(string(jsonData))
}

func (client *Client) ListenScheduler() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for {
		var message ProcessedDataMessage
		err := client.SchedulerConn.ReadJSON(&message)
		if err != nil {
			if errors.Is(err, websocket.ErrCloseSent) || ctx.Err() != nil {
				return
			}
			slog.Error("Failed to read from scheduler", "error", err, "userID", client.UserID)
			time.Sleep(1 * time.Second)
			continue
		}

		handler := client.SchedulerListenerHandler
		if handler == nil {
			slog.Warn("No scheduler listener handler set, skipping message", "userID", client.UserID)
			continue
		}
		handler(message)
	}
}

// ListenSchedulerGRPC listens for inference results from scheduler via gRPC (NEW)
func (client *Client) ListenSchedulerGRPC() {
	if client.SchedulerGRPCStream == nil {
		slog.Error("No gRPC stream available", "userID", client.UserID)
		return
	}

	for {
		result, err := client.SchedulerGRPCStream.Recv()
		if err != nil {
			slog.Error("gRPC recv error from scheduler", "error", err, "userID", client.UserID)
			return
		}

		// Convert protobuf result to ProcessedDataMessage
		msg := ProcessedDataMessage{
			Timestamp:          result.Timestamp,
			ProcessingStatus:   int(result.ProcessingStatus),
			FrameID:            result.FrameId,
			InferenceLatencyMs: float64(result.InferenceLatencyMs),
			StreamID:           result.StreamId,
			NodeName:           result.NodeName,
		}

		// Convert bounding boxes
		for _, box := range result.Detections {
			msg.Detections = append(msg.Detections, struct {
				X          int     `json:"x"`
				Y          int     `json:"y"`
				Dx         int     `json:"dx"`
				Dy         int     `json:"dy"`
				Label      string  `json:"label"`
				Confidence float64 `json:"confidence"`
			}{
				X:          int(box.X),
				Y:          int(box.Y),
				Dx:         int(box.Dx),
				Dy:         int(box.Dy),
				Label:      box.Label,
				Confidence: float64(box.Confidence),
			})
		}

		// Convert stage timestamps
		if result.StageTs != nil {
			msg.StageTS.ClientCaptureMs = result.StageTs.ClientCaptureMs
			msg.StageTS.SchedulerReceiveMs = result.StageTs.SchedulerReceiveMs
			msg.StageTS.SchedulerDecisionMs = result.StageTs.SchedulerDecisionMs
			msg.StageTS.ComputeReceiveMs = result.StageTs.ComputeReceiveMs
			msg.StageTS.UDPSendMs = result.StageTs.UdpSendMs
			msg.StageTS.PythonReceiveMs = result.StageTs.PythonReceiveMs
			msg.StageTS.YOLOInferenceStartMs = result.StageTs.YoloInferenceStartMs
			msg.StageTS.YOLOInferenceEndMs = result.StageTs.YoloInferenceEndMs
			msg.StageTS.PythonGRPCSendMs = result.StageTs.PythonGrpcSendMs
			msg.StageTS.ComputeReceiveResultMs = result.StageTs.ComputeReceiveResultMs
			msg.StageTS.SchedulerReceiveResultMs = result.StageTs.SchedulerReceiveResultMs
			msg.StageTS.FrontendSendMs = result.StageTs.FrontendSendMs
		}

		handler := client.SchedulerListenerHandler
		if handler != nil {
			handler(msg)
		}
	}
}

// Close cleans up all client connections
func (client *Client) Close() {
	if client.SchedulerGRPCStream != nil {
		client.SchedulerGRPCStream.Context().Done()
	}
	if client.SchedulerGRPCConn != nil {
		client.SchedulerGRPCConn.Close()
	}
	if client.SchedulerConn != nil {
		client.SchedulerConn.Close()
	}
	if client.PeerConnection != nil {
		client.PeerConnection.Close()
	}
	if client.ClientConn != nil {
		client.ClientConn.Close()
	}
}
