package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/BuzzingTaz/fw-edge-apps/internal/clientif"
	"github.com/BuzzingTaz/fw-edge-apps/internal/eventsingest"
)

var clientsManager *clientif.ClientsManager
var natsURL = "nats://localhost:4222"

// NEW: Flags for gRPC migration
var (
	schedulerURL      = flag.String("scheduler-url", "ws://localhost:9998", "URL of the scheduler WebSocket server")
	schedulerGRPCAddr = flag.String("scheduler-grpc", "localhost:9998", "gRPC address of the scheduler (for frame streaming)")
	useGRPC           = flag.Bool("use-grpc", false, "Use gRPC instead of WebSocket for client->scheduler frame streaming")
)

// tsToTaskIDMap maps frame timestamps to task IDs for telemetry
var (
	tsToTaskIDMap = make(map[uint64]string)
	tsToTaskIDMu  sync.RWMutex
)

func initiateHandler(w http.ResponseWriter, r *http.Request) {
	protocol := r.PathValue("protocol")
	if !clientif.IsValidProtocol(protocol) {
		http.Error(w, "Invalid protocol", http.StatusBadRequest)
		return
	}

	userID := r.PathValue("userID")
	if userID == "" {
		http.Error(w, "userID is required", http.StatusBadRequest)
		return
	}

	client, err := clientsManager.CreateClient(userID, protocol)
	if err != nil {
		slog.Error("Failed to create client", "err", err)
		http.Error(w, "Failed to create client: "+err.Error(), http.StatusInternalServerError)
		return
	}
	client.SchedulerURL = *schedulerURL

	// NEW: Support both WebSocket and gRPC connections to scheduler
	if *useGRPC {
		// gRPC path: frames go via gRPC, results come back via gRPC
		err = client.ConnectSchedulerGRPC(*schedulerGRPCAddr)
		if err != nil {
			slog.Error("Failed to connect to scheduler via gRPC", "err", err)
			http.Error(w, "Failed to connect to scheduler gRPC: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Start gRPC result listener
		go client.ListenSchedulerGRPC()
	} else {
		// Legacy WebSocket path
		err = client.ConnectScheduler()
		if err != nil {
			slog.Error("Failed to connect to scheduler", "err", err)
			http.Error(w, "Failed to connect to scheduler: "+err.Error(), http.StatusInternalServerError)
			return
		}
		go client.ListenScheduler()
	}

	client.SchedulerListenerHandler = func(message clientif.ProcessedDataMessage) {
		// NEW: Use frame_id for task tracking instead of timestamp
		taskID := ""
		tsToTaskIDMu.RLock()
		if existingTask, ok := tsToTaskIDMap[message.Timestamp]; ok {
			taskID = existingTask
		}
		tsToTaskIDMu.RUnlock()

		if taskID == "" && message.FrameID != "" {
			// Fallback: generate task ID from frame_id if not in map
			taskID = message.FrameID
		}

		if taskID != "" {
			eventsingest.TransmitMeasureEvent(time.Now(), client.UserID, taskID, "clientif_results_reached", map[string]string{
				"timestamp":            strconv.FormatUint(message.Timestamp, 10),
				"frame_id":             message.FrameID,
				"stream_id":            message.StreamID,
				"node_name":            message.NodeName,
				"inference_latency_ms": strconv.FormatFloat(message.InferenceLatencyMs, 'f', 2, 64),
			})
		}

		slog.Debug("Received processed data from scheduler",
			"userID", client.UserID,
			"Frame Timestamp", message.Timestamp,
			"frame_id", message.FrameID,
			"stream_id", message.StreamID,
			"node_name", message.NodeName)

		if err := clientif.SendDataToClient(client, message); err != nil {
			slog.Error("Failed to send inference data over WebRTC data channel", "error", err)
		}

		if taskID != "" {
			eventsingest.TransmitMeasureEvent(time.Now(), client.UserID, taskID, "clientif_results_sent", map[string]string{
				"timestamp": strconv.FormatUint(message.Timestamp, 10),
				"frame_id":  message.FrameID,
			})
		}
	}

	if protocol == "webrtc" {
		slog.Info("Initiating WebRTC connection for ", "userID", client.UserID, "useGRPC", *useGRPC)
		HandleWebRTC(client, w, r)
	}
}

func init() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	clientsManager = clientif.NewClientsManager()
}

func main() {
	flag.Parse()
	defer clientsManager.CloseAll()

	err := eventsingest.Initialize(natsURL, "clientif_events")
	if err != nil {
		slog.Warn("Failed to init events ingest client, telemetry will not be tracked", "err", err)
	}
	defer eventsingest.Close()

	http.HandleFunc("/initiate/{protocol}/{userID}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		initiateHandler(w, r)
	})

	go func() {
		slog.Info("Starting clientif server", "port", ":9999", "useGRPC", *useGRPC, "schedulerGRPC", *schedulerGRPCAddr)
		if err := http.ListenAndServe(":9999", nil); err != nil {
			slog.Error("Server failed", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	fmt.Println("Shutdown signal received. Exiting.")
}
