package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BuzzingTaz/fw-edge-apps/internal/scheduler"
	pb "github.com/BuzzingTaz/fw-edge-apps/proto"
	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

var (
	nodeAddrs = map[string]string{
		"node1": "localhost:9997",
		"node2": "localhost:9995",
		"node3": "localhost:9993",
	}
	globalScheduler *scheduler.Scheduler

	// Per-client state instead of global
	clientStates   = make(map[string]*clientState)
	clientStatesMu sync.RWMutex

	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	csvFile *os.File
	logChan = make(chan string, 5000) // Increased buffer

	// Config flags
	windowSize    = flag.Int("window-size", 1, "Window size for batch scheduling (1=per-frame)")
	enableWindow  = flag.Bool("enable-window", false, "Enable window-based scheduling")
	schedulerPort = flag.String("port", ":9998", "Scheduler WebSocket/gRPC multiplexed port")
	node1Addr     = flag.String("node1-addr", "localhost:9997", "gRPC address of compute Node 1")
	node2Addr        = flag.String("node2-addr", "localhost:9995", "gRPC address of compute Node 2")
	node3Addr        = flag.String("node3-addr", "localhost:9993", "gRPC address of compute Node 3")
	dqnAddr          = flag.String("dqn-addr", "http://localhost:5010", "HTTP address of the DQN sidecar service")
	forcedPolicyFlag = flag.Int("policy", -1, "Force scheduling policy (-1=Auto/DQN, 0=Node1, 1=RoundRobin, 2=Dynamic Balancer)")
)

type clientState struct {
	userID    string
	writeChan chan interface{}
	done      chan struct{}
	conn      *websocket.Conn
	streamID  string
	once      sync.Once
}

func (cs *clientState) Close() {
	cs.once.Do(func() {
		if cs.conn != nil {
			cs.conn.Close()
		}
		close(cs.writeChan)
	})
}

// Node connection management
var (
	nodeConns         = make(map[string]*grpc.ClientConn)
	activeStreams     = make(map[string]grpc.BidiStreamingClient[pb.RTPPacket, pb.InferenceResult])
	streamsMu         sync.RWMutex
	connMu            sync.RWMutex
	connectingNodes   = make(map[string]bool)
	connectingNodesMu sync.Mutex
)

func getMetricsURL(addr string) string {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Sprintf("http://%s:9996/metrics", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Sprintf("http://%s:9996/metrics", host)
	}
	return fmt.Sprintf("http://%s:%d/metrics", host, port-1)
}

func calculateQoS(m scheduler.NodeMetrics) float64 {
	qosL := math.Exp(-m.Latency / 50.0)
	qosQ := 1.0 - (m.Queue / 5.0)
	if qosQ < 0.0 {
		qosQ = 0.0
	}
	qosA := 1.0
	if m.CPU > 95.0 {
		qosA = 0.0
	}
	qosTH := m.Throughput / 300.0
	if qosTH > 1.0 {
		qosTH = 1.0
	}
	if qosTH < 0.0 {
		qosTH = 0.0
	}
	qosT := (85.0 - m.Temperature) / (85.0 - 45.0)
	if qosT > 1.0 {
		qosT = 1.0
	}
	if qosT < 0.0 {
		qosT = 0.0
	}
	return 0.25*qosL + 0.20*qosQ + 0.20*qosA + 0.20*qosTH + 0.15*qosT
}

func ensureNodeStreams() {
	streamsMu.Lock()
	defer streamsMu.Unlock()

	for nodeName, addr := range nodeAddrs {
		if _, exists := activeStreams[nodeName]; exists && activeStreams[nodeName] != nil {
			continue
		}

		connectingNodesMu.Lock()
		if connectingNodes[nodeName] {
			connectingNodesMu.Unlock()
			continue
		}
		connectingNodes[nodeName] = true
		connectingNodesMu.Unlock()

		// Connect asynchronously per node to prevent one offline node from blocking others
		go func(node string, targetAddr string) {
			defer func() {
				connectingNodesMu.Lock()
				delete(connectingNodes, node)
				connectingNodesMu.Unlock()
			}()

			// Close old connection
			connMu.Lock()
			if oldConn, exists := nodeConns[node]; exists && oldConn != nil {
				oldConn.Close()
				delete(nodeConns, node)
			}
			connMu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			conn, err := grpc.DialContext(ctx, targetAddr,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithKeepaliveParams(keepalive.ClientParameters{
					Time:                30 * time.Second,
					Timeout:             10 * time.Second,
					PermitWithoutStream: true,
				}),
				grpc.WithBlock(),
			)
			cancel()
			if err != nil {
				slog.Error("Failed to connect to node", "node", node, "addr", targetAddr, "err", err)
				return
			}

			connMu.Lock()
			nodeConns[node] = conn
			connMu.Unlock()

			client := pb.NewComputeStreamClient(conn)
			stream, err := client.StreamVideo(context.Background())
			if err != nil {
				slog.Error("Failed to open stream to node", "node", node, "addr", targetAddr, "err", err)
				conn.Close()
				return
			}

			streamsMu.Lock()
			activeStreams[node] = stream
			streamsMu.Unlock()

			if globalScheduler != nil {
				globalScheduler.ResetInFlight(node)
			}

			slog.Info("Established gRPC stream to node", "node", node, "addr", targetAddr)
			go ReadInferenceData(stream, node)
		}(nodeName, addr)
	}
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	slog.Info("WebSocket handler called")

	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ws/"), "/")
	userID := pathParts[0]

	if userID == "" {
		http.Error(w, "userID is required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "err", err)
		return
	}

	// Per-client write channel
	writeChan := make(chan interface{}, 2000) // Larger buffer for multi-stream high throughput
	done := make(chan struct{})

	// Writer goroutine - ONLY goroutine that calls WriteJSON for this connection
	go func() {
		defer close(done)
		for msg := range writeChan {
			if err := conn.WriteJSON(msg); err != nil {
				slog.Error("WebSocket write error", "err", err, "userID", userID)
				return
			}
			sendMs := time.Now().UnixMilli()
			if res, ok := msg.(*pb.InferenceResult); ok && res != nil && globalScheduler != nil {
				globalScheduler.RecordFrontendSend(res.FrameId, sendMs)
			}
		}
	}()

	// Store client state
	streamID := fmt.Sprintf("stream-%s", userID)
	cs := &clientState{
		userID:    userID,
		writeChan: writeChan,
		done:      done,
		conn:      conn,
		streamID:  streamID,
	}

	clientStatesMu.Lock()
	if oldState, exists := clientStates[streamID]; exists {
		oldState.Close()
		<-oldState.done
	}
	clientStates[streamID] = cs
	clientStatesMu.Unlock()

	slog.Info("WebSocket connection established for user", "userID", userID, "streamID", streamID)

	ensureNodeStreams()

	// Read frames from frontend
	go func() {
		var lastSavedFrameMs int64

		defer func() {
			cs.Close()
			<-done

			clientStatesMu.Lock()
			if current, exists := clientStates[streamID]; exists && current == cs {
				delete(clientStates, streamID)
			}
			clientStatesMu.Unlock()

			if globalScheduler != nil {
				globalScheduler.RemoveStream(streamID)
			}

			slog.Info("WebSocket client disconnected", "userID", userID)
		}()

		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Error("WebSocket read error", "err", err)
				}
				return
			}

			if messageType != websocket.BinaryMessage {
				slog.Warn("Received non-binary message, skipping")
				continue
			}

			slog.Debug("Received JPEG frame", "size", len(data), "userID", userID)

			nowMs := time.Now().UnixMilli()

			// Save incoming JPEG frames to 'received_frames' folder so user can visually inspect them
			if nowMs-lastSavedFrameMs >= 1500 {
				lastSavedFrameMs = nowMs
				saveDir := "received_frames"
				_ = os.MkdirAll(saveDir, 0755)
				filename := fmt.Sprintf("%s/frame_%s_%d.jpg", saveDir, userID, nowMs)
				latestName := fmt.Sprintf("%s/latest_%s.jpg", saveDir, userID)
				if err := os.WriteFile(filename, data, 0644); err == nil {
					_ = os.WriteFile(latestName, data, 0644)
					slog.Info("Saved received JPEG frame to folder", "file", filename, "bytes", len(data))
				}
			}

			frame := &scheduler.Frame{
				Data:      data,
				Timestamp: nowMs,
				FrameID:   fmt.Sprintf("%s-%d", streamID, time.Now().UnixNano()),
				StreamID:  streamID,
				StageTS: scheduler.StageTimestamps{
					ClientCaptureMs:    nowMs - 33, // Approximate: frame was captured ~33ms ago at 30fps
					SchedulerReceiveMs: nowMs,
				},
			}
			globalScheduler.ScheduleFrame(frame)
		}
	}()
}

func ReadInferenceData(stream grpc.BidiStreamingClient[pb.RTPPacket, pb.InferenceResult], nodeName string) {
	defer func() {
		streamsMu.Lock()
		delete(activeStreams, nodeName)
		streamsMu.Unlock()
		if globalScheduler != nil {
			globalScheduler.ResetInFlight(nodeName)
		}
	}()

	for {
		inferenceData, err := stream.Recv()
		if err == io.EOF {
			slog.Info("Server closed the inference stream", "node", nodeName)
			return
		}
		if err != nil {
			slog.Error("Error receiving inference data", "node", nodeName, "err", err)
			return
		}

		// Record result latency with all per-stage timestamps
		if globalScheduler != nil && inferenceData.FrameId != "" {
			var stageTS scheduler.StageTimestamps
			if inferenceData.StageTs != nil {
				inferenceData.StageTs.SchedulerReceiveResultMs = time.Now().UnixMilli()

				stageTS = scheduler.StageTimestamps{
					ClientCaptureMs:          inferenceData.StageTs.ClientCaptureMs,
					SchedulerReceiveMs:       inferenceData.StageTs.SchedulerReceiveMs,
					SchedulerDecisionMs:      inferenceData.StageTs.SchedulerDecisionMs,
					ComputeReceiveMs:         inferenceData.StageTs.ComputeReceiveMs,
					UDPSendMs:                inferenceData.StageTs.UdpSendMs,
					PythonReceiveMs:          inferenceData.StageTs.PythonReceiveMs,
					YOLOInferenceStartMs:     inferenceData.StageTs.YoloInferenceStartMs,
					YOLOInferenceEndMs:       inferenceData.StageTs.YoloInferenceEndMs,
					PythonGRPCSendMs:         inferenceData.StageTs.PythonGrpcSendMs,
					ComputeReceiveResultMs:   inferenceData.StageTs.ComputeReceiveResultMs,
					SchedulerReceiveResultMs: inferenceData.StageTs.SchedulerReceiveResultMs,
					FrontendSendMs:           time.Now().UnixMilli(), // Set when we send to frontend
				}
			}
			globalScheduler.RecordResultLatency(inferenceData.FrameId, nodeName, stageTS)
		}

		slog.Info("Received inference results",
			"node", nodeName,
			"timestamp", inferenceData.Timestamp,
			"detections", len(inferenceData.Detections),
			"frame_id", inferenceData.FrameId,
			"stream_id", inferenceData.StreamId)

		// Find the right client to send to by StreamID
		clientStatesMu.RLock()
		cs, ok := clientStates[inferenceData.StreamId]
		var targetCh chan interface{}
		if ok {
			targetCh = cs.writeChan
		}
		clientStatesMu.RUnlock()

		if targetCh != nil {
			select {
			case targetCh <- inferenceData:
			default:
				slog.Warn("WebSocket/gRPC write channel full, dropping result", "node", nodeName, "streamID", inferenceData.StreamId)
				if globalScheduler != nil {
					globalScheduler.IncrementDroppedFrames()
				}
			}
		} else {
			slog.Warn("No client state found for streamID", "streamID", inferenceData.StreamId, "node", nodeName)
		}
	}
}

func init() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
}

func main() {
	flag.Parse()

	// Configure node addresses from flags
	nodeAddrs["node1"] = *node1Addr
	nodeAddrs["node2"] = *node2Addr
	nodeAddrs["node3"] = *node3Addr

	nodes := []string{"node1", "node2", "node3"}

	winConfig := scheduler.WindowConfig{
		Enabled:    *enableWindow,
		WindowSize: *windowSize,
	}

	globalScheduler = scheduler.NewScheduler(nodes, *dqnAddr, winConfig)
	globalScheduler.ForcedPolicy = *forcedPolicyFlag

	// Load stream overrides for benchmarking
	overrides := loadBenchmarkOverrides()
	if len(overrides) > 0 {
		globalScheduler.SetStreamOverrides(overrides)
	}

	var err error
	csvFile, err = os.OpenFile("scheduler_metrics.csv", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		slog.Error("Failed to open metrics CSV file", "err", err)
	} else {
		// Expanded CSV header with per-stage latency columns, GPU, CPU, and Memory metrics for 3 nodes
		csvFile.WriteString("timestamp,cpu,memory,temperature,queue,latency,throughput,power,selected_policy,selected_node,qos,dqn_inference_ms,exploration_ms,exploitation_ms,scheduler_decision_ms,total_frames,dropped_frames,scheduler_queue_len,node1_frames,node2_frames,node3_frames,node1_throughput,node2_throughput,node3_throughput,avg_inference_latency,policy0_frames,policy1_frames,policy2_frames,policy3_frames,end_to_end_latency_ms,fps,scheduler_utilization,s1_client_to_sched_ms,s2_sched_decision_ms,s3_sched_to_compute_ms,s4_compute_to_python_ms,s5_python_decode_ms,s6_yolo_inference_ms,s7_python_to_compute_ms,s8_compute_to_sched_ms,s9_sched_to_frontend_ms,node1_gpu,node2_gpu,node3_gpu,node1_cpu,node2_cpu,node3_cpu,node1_memory,node2_memory,node3_memory\n")

		go func() {
			for logLine := range logChan {
				if csvFile != nil {
					csvFile.WriteString(logLine)
				}
			}
		}()
	}

	// Non-blocking dispatch with frame drop policy
	globalScheduler.OnDispatch = func(node scheduler.Node, f *scheduler.Frame) {
		globalScheduler.IncrementInFlight(string(node))

		// Non-blocking send with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		maxRetries := 2
		for attempt := 0; attempt < maxRetries; attempt++ {
			streamsMu.RLock()
			stream, exists := activeStreams[string(node)]
			streamsMu.RUnlock()

			if !exists || stream == nil {
				slog.Warn("No active stream, attempting reconnect", "node", string(node), "attempt", attempt+1)
				ensureNodeStreams()
				select {
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					globalScheduler.DecrementInFlight(string(node))
					globalScheduler.IncrementDroppedFrames()
					return
				}
				continue
			}

			// Send with frame metadata and per-stage timestamps
			pkt := &pb.RTPPacket{
				Data:     f.Data,
				FrameId:  f.FrameID,
				StreamId: f.StreamID,
			}

			// Mark compute receive time just before sending
			f.StageTS.ComputeReceiveMs = time.Now().UnixMilli()

			// Convert stage timestamps
			pkt.StageTs = &pb.StageTimestamps{
				ClientCaptureMs:          f.StageTS.ClientCaptureMs,
				SchedulerReceiveMs:       f.StageTS.SchedulerReceiveMs,
				SchedulerDecisionMs:      f.StageTS.SchedulerDecisionMs,
				ComputeReceiveMs:         f.StageTS.ComputeReceiveMs,
				UdpSendMs:                f.StageTS.UDPSendMs,
				PythonReceiveMs:          f.StageTS.PythonReceiveMs,
				YoloInferenceStartMs:     f.StageTS.YOLOInferenceStartMs,
				YoloInferenceEndMs:       f.StageTS.YOLOInferenceEndMs,
				PythonGrpcSendMs:         f.StageTS.PythonGRPCSendMs,
				ComputeReceiveResultMs:   f.StageTS.ComputeReceiveResultMs,
				SchedulerReceiveResultMs: f.StageTS.SchedulerReceiveResultMs,
				FrontendSendMs:           f.StageTS.FrontendSendMs,
			}

			err := stream.Send(pkt)
			if err == nil {
				return
			}

			slog.Error("Failed to dispatch frame, removing stream", "node", string(node), "err", err, "attempt", attempt+1)
			streamsMu.Lock()
			delete(activeStreams, string(node))
			streamsMu.Unlock()

			ensureNodeStreams()
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
				globalScheduler.DecrementInFlight(string(node))
				globalScheduler.IncrementDroppedFrames()
				return
			}
		}

		slog.Error("Failed to dispatch frame after retries, dropping", "node", string(node), "frame_id", f.FrameID)
		globalScheduler.DecrementInFlight(string(node))
		globalScheduler.IncrementDroppedFrames()
	}

	// Metrics polling goroutine
	go func() {
		client := &http.Client{Timeout: 200 * time.Millisecond}
		for {
			for _, node := range nodes {
				addr := nodeAddrs[node]
				url := getMetricsURL(addr)
				resp, err := client.Get(url)
				if err != nil {
					// Mark node as offline/degraded
					globalScheduler.UpdateNodeMetrics(node, scheduler.NodeMetrics{
						CPU:         99.0,
						Memory:      99.0,
						Temperature: 85.0,
						Queue:       99.0,
						Latency:     9999.0,
						Throughput:  0.0,
						Power:       15.0,
					})
					continue
				}
				var m scheduler.NodeMetrics
				if err := json.NewDecoder(resp.Body).Decode(&m); err == nil {
					globalScheduler.UpdateNodeMetrics(node, m)
				}
				resp.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Telemetry logging goroutine with per-stage latency
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		var lastTotalFrames int64
		var lastTime = time.Now()

		for range ticker.C {
			if globalScheduler == nil {
				continue
			}

			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			lastTime = now

			globalScheduler.StatsMu.RLock()
			total := globalScheduler.TotalFrames
			n1 := globalScheduler.NodeFrames["node1"]
			n2 := globalScheduler.NodeFrames["node2"]
			n3 := globalScheduler.NodeFrames["node3"]
			p0 := globalScheduler.PolicyFrames[0]
			p1 := globalScheduler.PolicyFrames[1]
			p2 := globalScheduler.PolicyFrames[2]
			p3 := globalScheduler.PolicyFrames[3]
			dqnDur := globalScheduler.DqnInferenceTime
			decDur := globalScheduler.DecisionTime
			exploreDur := globalScheduler.ExplorationTime
			exploitDur := globalScheduler.ExploitationTime
			selNode := globalScheduler.LastSelectedNode
			selPol := globalScheduler.LastSelectedPolicy
			dropped := globalScheduler.GetDroppedFrames()
			queueLen := globalScheduler.GetSchedulerQueueLen()
			globalScheduler.StatsMu.RUnlock()

			if selNode == "" {
				selNode = "node1"
			}

			m, exists := globalScheduler.GetNodeMetrics(selNode)
			if !exists {
				continue
			}

			// Calculate FPS
			fps := float64(total-lastTotalFrames) / elapsed
			lastTotalFrames = total

			// Get per-node throughput
			m1, _ := globalScheduler.GetNodeMetrics("node1")
			m2, _ := globalScheduler.GetNodeMetrics("node2")
			m3, _ := globalScheduler.GetNodeMetrics("node3")

			qos := calculateQoS(m)
			ts := time.Now().Unix()

			// End-to-end latency proxy
			var e2eLatency float64
			globalScheduler.StatsMu.RLock()
			e2eLatency = dqnDur + decDur + m.Latency
			globalScheduler.StatsMu.RUnlock()

			// Scheduler utilization
			var utilization float64
			if total > 0 {
				utilization = float64(total-dropped) / float64(total) * 100.0
			}

			// Get per-stage latency averages
			avgStages := globalScheduler.GetAvgStageLatency()

			slog.Info("Metrics Telemetry",
				"time", ts,
				"node", selNode,
				"policy", selPol,
				"cpu", fmt.Sprintf("%.1f%%", m.CPU),
				"mem", fmt.Sprintf("%.1f%%", m.Memory),
				"temp", fmt.Sprintf("%.1fC", m.Temperature),
				"queue", m.Queue,
				"lat", fmt.Sprintf("%.1fms", m.Latency),
				"tp", fmt.Sprintf("%.1ffps", m.Throughput),
				"power", fmt.Sprintf("%.2fW", m.Power),
				"qos", fmt.Sprintf("%.3f", qos),
				"dqn_ms", fmt.Sprintf("%.2fms", dqnDur),
				"decision_ms", fmt.Sprintf("%.2fms", decDur),
				"explore_ms", fmt.Sprintf("%.2fms", exploreDur),
				"exploit_ms", fmt.Sprintf("%.2fms", exploitDur),
				"total_frames", total,
				"dropped_frames", dropped,
				"scheduler_queue", queueLen,
				"fps", fmt.Sprintf("%.1f", fps),
				"utilization", fmt.Sprintf("%.1f%%", utilization),
				"s1_client_to_sched", fmt.Sprintf("%.1f", avgStages.Stage1_ClientToScheduler),
				"s2_sched_decision", fmt.Sprintf("%.1f", avgStages.Stage2_SchedulerDecision),
				"s3_sched_to_compute", fmt.Sprintf("%.1f", avgStages.Stage3_SchedulerToCompute),
				"s4_compute_to_python", fmt.Sprintf("%.1f", avgStages.Stage4_ComputeToPython),
				"s5_python_decode", fmt.Sprintf("%.1f", avgStages.Stage5_PythonDecode),
				"s6_yolo_inference", fmt.Sprintf("%.1f", avgStages.Stage6_YOLOInference),
				"s7_python_to_compute", fmt.Sprintf("%.1f", avgStages.Stage7_PythonToCompute),
				"s8_compute_to_sched", fmt.Sprintf("%.1f", avgStages.Stage8_ComputeToScheduler),
				"s9_sched_to_frontend", fmt.Sprintf("%.1f", avgStages.Stage9_SchedulerToFrontend),
				"e2e_ms", fmt.Sprintf("%.1f", avgStages.TotalEndToEnd),
			)

			logLine := fmt.Sprintf("%d,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%d,%s,%.4f,%.2f,%.2f,%.2f,%.2f,%d,%d,%d,%d,%d,%d,%.2f,%.2f,%.2f,%.2f,%d,%d,%d,%d,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f\n",
				ts, m.CPU, m.Memory, m.Temperature, m.Queue, m.Latency, m.Throughput, m.Power,
				selPol, selNode, qos, dqnDur, exploreDur, exploitDur, decDur,
				total, dropped, queueLen, n1, n2, n3,
				m1.Throughput, m2.Throughput, m3.Throughput, m.Latency,
				p0, p1, p2, p3, e2eLatency, fps, utilization,
				avgStages.Stage1_ClientToScheduler,
				avgStages.Stage2_SchedulerDecision,
				avgStages.Stage3_SchedulerToCompute,
				avgStages.Stage4_ComputeToPython,
				avgStages.Stage5_PythonDecode,
				avgStages.Stage6_YOLOInference,
				avgStages.Stage7_PythonToCompute,
				avgStages.Stage8_ComputeToScheduler,
				avgStages.Stage9_SchedulerToFrontend,
				m1.GPU, m2.GPU, m3.GPU,
				m1.CPU, m2.CPU, m3.CPU,
				m1.Memory, m2.Memory, m3.Memory,
			)

			select {
			case logChan <- logLine:
			default:
				slog.Warn("CSV log channel full, dropping log line")
			}
		}
	}()

	// Set up multiplexed h2c server for WebSocket and gRPC
	gServer := grpc.NewServer()
	pb.RegisterSchedulerStreamServer(gServer, &schedulerGRPCServer{})

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", wsHandler)

	mainHandler := h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			gServer.ServeHTTP(w, r)
		} else {
			mux.ServeHTTP(w, r)
		}
	}), &http2.Server{})

	slog.Info("Server starting (multiplexing gRPC + WebSocket)", "port", *schedulerPort)
	go func() {
		if err := http.ListenAndServe(*schedulerPort, mainHandler); err != nil {
			slog.Error("Failed to start server", "err", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("Shutdown signal received. Cleaning up...")

	// Graceful shutdown
	close(logChan)
	if csvFile != nil {
		csvFile.Close()
	}

	// Close all client connections
	clientStatesMu.Lock()
	for _, cs := range clientStates {
		close(cs.writeChan)
		if cs.conn != nil {
			cs.conn.Close()
		}
	}
	clientStatesMu.Unlock()

	// Close all streams
	streamsMu.Lock()
	for name := range activeStreams {
		delete(activeStreams, name)
	}
	streamsMu.Unlock()

	connMu.Lock()
	for name, conn := range nodeConns {
		if conn != nil {
			conn.Close()
		}
		delete(nodeConns, name)
	}
	connMu.Unlock()

	if globalScheduler != nil {
		globalScheduler.CloseAllConnections()
	}

	slog.Info("Exiting.")
}

type schedulerGRPCServer struct {
	pb.UnimplementedSchedulerStreamServer
}

func (s *schedulerGRPCServer) StreamFrames(stream pb.SchedulerStream_StreamFramesServer) error {
	slog.Info("gRPC client connected to StreamFrames")

	var clientStateRegistered bool
	var streamID string
	var userID string
	var writeChan chan interface{}
	var done chan struct{}

	defer func() {
		if clientStateRegistered {
			clientStatesMu.Lock()
			delete(clientStates, streamID)
			clientStatesMu.Unlock()

			if globalScheduler != nil {
				globalScheduler.RemoveStream(streamID)
			}
			slog.Info("gRPC client stream closed", "streamID", streamID)
		}
	}()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			slog.Error("gRPC StreamFrames recv error", "err", err)
			return err
		}

		if !clientStateRegistered {
			streamID = req.StreamId
			parts := strings.Split(streamID, "-")
			if len(parts) >= 2 {
				userID = parts[1]
			} else {
				userID = streamID
			}

			writeChan = make(chan interface{}, 500)
			done = make(chan struct{})

			// Writer goroutine for gRPC client
			go func(ch chan interface{}, d chan struct{}) {
				defer close(d)
				for msg := range ch {
					infRes, ok := msg.(*pb.InferenceResult)
					if !ok {
						continue
					}
					if infRes.StageTs != nil {
						infRes.StageTs.FrontendSendMs = time.Now().UnixMilli()
					}
					if err := stream.Send(infRes); err != nil {
						slog.Error("gRPC StreamFrames send error", "err", err, "streamID", streamID)
						return
					}
				}
			}(writeChan, done)

			cs := &clientState{
				userID:     userID,
				writeChan:  writeChan,
				done:       done,
				streamID:   streamID,
			}

			clientStatesMu.Lock()
			clientStates[streamID] = cs
			clientStatesMu.Unlock()

			clientStateRegistered = true
			slog.Info("gRPC client state registered", "streamID", streamID, "userID", userID)

			ensureNodeStreams()
		}

		// Process frame
		nowMs := time.Now().UnixMilli()
		captureMs := req.CaptureTimestampMs
		if captureMs == 0 {
			captureMs = nowMs - 33
		}
		frame := &scheduler.Frame{
			Data:      req.JpegData,
			Timestamp: nowMs,
			FrameID:   req.FrameId,
			StreamID:  req.StreamId,
			StageTS: scheduler.StageTimestamps{
				ClientCaptureMs:    captureMs,
				SchedulerReceiveMs: nowMs,
			},
		}
		globalScheduler.ScheduleFrame(frame)
	}
}

func loadBenchmarkOverrides() map[string]string {
	overrides := make(map[string]string)
	data, err := os.ReadFile("benchmark_config.json")
	if err != nil {
		return overrides
	}
	var config struct {
		Streams []struct {
			StreamID     string `json:"stream_id"`
			AssignedNode string `json:"assigned_node"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		slog.Error("Failed to parse benchmark_config.json for overrides", "err", err)
		return overrides
	}
	for _, s := range config.Streams {
		if s.AssignedNode != "" && s.AssignedNode != "dqn" {
			overrides[s.StreamID] = s.AssignedNode
		}
	}
	slog.Info("Loaded stream overrides", "count", len(overrides))
	return overrides
}