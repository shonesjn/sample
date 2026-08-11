package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pbcompute "github.com/BuzzingTaz/fw-edge-apps/proto"
	pb "github.com/BuzzingTaz/fw-edge-apps/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var (
	port        = flag.Int("port", 9997, "The server port")
	udpPort     = flag.Int("udp-port", 5000, "The UDP port to dial")
	trackerPort = flag.Int("tracker-port", 5005, "The gRPC tracker server port")
)

// --- Per-stream result routing --------------------------------------
type streamResult struct {
	frameID            string
	boxes              []*pb.BoundingBox
	// Python-side timestamps (from YoloInferenceResult)
	pythonReceiveMs    int64   // Repurposed from res.Timestamp
	yoloStartMs        int64   // res.YoloStartMs
	yoloEndMs          int64   // res.YoloEndMs
	inferenceLatencyMs float32 // res.InferenceLatencyMs
}

var (
	pendingResults   = make(map[string]chan streamResult)
	pendingResultsMu sync.RWMutex
)

// --- Metrics (atomic for queue, mutex for rare latency writes) ------
var (
	metricQueue     int32
	metricLat       float64
	metricLatMu     sync.Mutex
	completionTimes []time.Time
	completionMu    sync.Mutex
)

type frameRecvInfo struct {
	Time      time.Time
	StageTs   *pb.StageTimestamps
	UdpSendMs int64
	StreamId  string
}

// Per-frame receive times -- KEYED BY FRAME_ID, NOT TIMESTAMP
type frameRecvTime struct {
	mu    sync.Mutex
	times map[string]frameRecvInfo  // frame_id -> receive info
}

func newFrameRecvTime() *frameRecvTime {
	return &frameRecvTime{times: make(map[string]frameRecvInfo)}
}

func (s *frameRecvTime) Store(frameID string, info frameRecvInfo) {
	s.mu.Lock()
	s.times[frameID] = info
	s.mu.Unlock()
}

func decrementMetricQueue() {
	for {
		old := atomic.LoadInt32(&metricQueue)
		if old <= 0 {
			atomic.StoreInt32(&metricQueue, 0)
			break
		}
		if atomic.CompareAndSwapInt32(&metricQueue, old, old-1) {
			break
		}
	}
}

func (s *frameRecvTime) LoadAndDelete(frameID string) (frameRecvInfo, bool) {
	s.mu.Lock()
	info, ok := s.times[frameID]
	if ok {
		delete(s.times, frameID)
	}
	s.mu.Unlock()
	return info, ok
}

func (s *frameRecvTime) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, info := range s.times {
		if now.Sub(info.Time) > maxAge {
			delete(s.times, id)
			decrementMetricQueue()
		}
	}
}

// --- Hardware helpers -----------------------------------------------
func getTemperature() float64 {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 42.0
	}
	tempStr := strings.TrimSpace(string(data))
	tempVal, err := strconv.ParseFloat(tempStr, 64)
	if err != nil {
		return 42.0
	}
	return tempVal / 1000.0
}

func getMemory() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 30.0
	}
	var memTotal, memAvailable float64
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			memTotal, _ = strconv.ParseFloat(fields[1], 64)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			memAvailable, _ = strconv.ParseFloat(fields[1], 64)
		}
	}
	if memTotal == 0 {
		return 30.0
	}
	return ((memTotal - memAvailable) / memTotal) * 100.0
}

func getCPU() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 15.0
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Scan()
	fields := strings.Fields(scanner.Text())
	var total float64
	for i := 1; i < len(fields); i++ {
		val, _ := strconv.ParseFloat(fields[i], 64)
		total += val
	}
	idle, _ := strconv.ParseFloat(fields[4], 64)
	return (1.0 - (idle / total)) * 100.0
}

func readSysfsFloat(path string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func getPower() float64 {
	const (
		ina3221VoltagePath = "/sys/devices/platform/bus@0/c240000.i2c/i2c-1/1-0040/hwmon/hwmon1/in1_input"
		ina3221CurrentPath = "/sys/devices/platform/bus@0/c240000.i2c/i2c-1/1-0040/hwmon/hwmon1/curr1_input"
	)
	voltageMV, vOK := readSysfsFloat(ina3221VoltagePath)
	currentMA, iOK := readSysfsFloat(ina3221CurrentPath)
	if vOK && iOK {
		return (voltageMV * currentMA) / 1_000_000.0
	}
	cpu := getCPU()
	return 5.0 + 5.0*(cpu/100.0)
}

func getGPU() float64 {
	gpuPaths := []string{
		"/sys/devices/gpu.0/load",
		"/sys/class/devfreq/57000000.gpu/load",
		"/sys/class/devfreq/17000000.gpu/load",
		"/sys/devices/platform/host1x/57000000.gpu/devfreq/57000000.gpu/load",
	}

	for _, path := range gpuPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		valStr := strings.TrimSpace(string(data))
		if strings.Contains(valStr, "@") {
			parts := strings.Split(valStr, "@")
			valStr = parts[0]
		}
		if strings.Contains(valStr, "/") {
			parts := strings.Split(valStr, "/")
			num, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			den, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil && den > 0 {
				val := (num / den) * 100.0
				if val > 0 {
					return val
				}
			}
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err == nil {
			if val > 100.0 {
				val = val / 10.0
			}
			if val > 0 {
				return val
			}
		}
	}

	q := atomic.LoadInt32(&metricQueue)
	if q > 0 {
		return math.Min(95.0, 30.0+float64(q)*15.0)
	}
	return 0.0
}

// --- Metrics HTTP server --------------------------------------------
func startMetricsServer(grpcPort int) {
	metricsPort := grpcPort - 1
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		q := atomic.LoadInt32(&metricQueue)

		completionMu.Lock()
		cutoff := time.Now().Add(-5 * time.Second)
		idx := 0
		for idx < len(completionTimes) && completionTimes[idx].Before(cutoff) {
			idx++
		}
		completionTimes = completionTimes[idx:]
		tp := float64(len(completionTimes)) / 5.0
		completionMu.Unlock()

		metricLatMu.Lock()
		lat := metricLat
		metricLatMu.Unlock()

		metrics := map[string]float64{
			"cpu":         getCPU(),
			"gpu":         getGPU(),
			"memory":      getMemory(),
			"temperature": getTemperature(),
			"queue":       float64(q),
			"latency":     lat,
			"throughput":  tp,
			"power":       getPower(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics)
	})
	log.Printf("Metrics HTTP server starting on :%d", metricsPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", metricsPort), nil); err != nil {
		log.Fatalf("Failed to start metrics server: %v", err)
	}
}

// --- Inference Tracker gRPC (receives results from Python) ----------
type inferenceServer struct {
	pbcompute.UnimplementedInferenceTrackerServer
}

func (s *inferenceServer) StreamResults(stream pbcompute.InferenceTracker_StreamResultsServer) error {
	log.Println("Python inference client connected to gRPC stream!")

	for {
		res, err := stream.Recv()
		if err == io.EOF {
			log.Println("Python client cleanly closed the gRPC stream.")
			return stream.SendAndClose(&pbcompute.Ack{Received: true})
		}
		if err != nil {
			log.Printf("gRPC stream error (Python likely disconnected): %v", err)
			return err
		}

		log.Printf("Received %d bounding boxes, frame_id=%s, inference_latency=%.2fms",
			len(res.Boxes), res.FrameId, res.InferenceLatencyMs)

		// Convert YoloBoundingBox to pb.BoundingBox
		var boxes []*pb.BoundingBox
		for _, box := range res.Boxes {
			boxes = append(boxes, &pb.BoundingBox{
				X:          uint64(box.X),
				Y:          uint64(box.Y),
				Dx:         uint64(box.W),
				Dy:         uint64(box.H),
				Label:      box.ClassLabel,
				Confidence: box.Confidence,
			})
		}

		// -- ROUTE result to the correct stream by frame_id --
		pendingResultsMu.RLock()
		ch, ok := pendingResults[res.FrameId]
		pendingResultsMu.RUnlock()

		if ok {
			select {
			case ch <- streamResult{
				frameID:            res.FrameId,
				boxes:              boxes,
				pythonReceiveMs:    res.Timestamp,          // Repurposed: was capture time, now = python receive
				yoloStartMs:        res.YoloStartMs,
				yoloEndMs:          res.YoloEndMs,
				inferenceLatencyMs: res.InferenceLatencyMs,
			}:
			case <-time.After(5 * time.Second):
				log.Printf("Timeout delivering result for frame_id=%s", res.FrameId)
			}
		} else {
			log.Printf("No pending stream for frame_id=%s (stream may have closed)", res.FrameId)
		}
	}
}

// --- Scheduler gRPC (receives frames from scheduler) ----------------
type computeStreamServer struct {
	pb.UnimplementedComputeStreamServer
}

func (*computeStreamServer) StreamVideo(stream grpc.BidiStreamingServer[pb.RTPPacket, pb.InferenceResult]) error {
	log.Println("========== StreamVideo ENTERED ==========")

	// Connect to Python TCP server
	var conn net.Conn
	var err error
	for attempts := 0; attempts < 30; attempts++ {
		conn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", *udpPort))
		if err == nil {
			break
		}
		log.Printf("Waiting for Python TCP server on port %d... (attempt %d/30)", *udpPort, attempts+1)
		time.Sleep(1 * time.Second)
	}
	if err != nil {
		log.Printf("Failed to connect to Python TCP server after 30 attempts: %v", err)
		return fmt.Errorf("python not available: %w", err)
	}
	// Explicitly disable Nagle's algorithm on the Go side to remove loopback buffering delays
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}
	defer conn.Close()
	log.Printf("TCP connection established to localhost:%d (TCP_NODELAY enabled)", *udpPort)

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	errChan := make(chan error, 2)
	var wg sync.WaitGroup

	streamID := fmt.Sprintf("stream-%d", time.Now().UnixNano())
	recvTimes := newFrameRecvTime()   // frame_id -> time.Time
	resultCh := make(chan streamResult, 32)

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recvTimes.Cleanup(1 * time.Second)
			}
		}
	}()

	// -- Receiver: gets JPEG from scheduler -> forwards to Python --
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			frame, err := stream.Recv()
			if err == io.EOF {
				log.Println("Scheduler closed stream")
				errChan <- nil
				return
			}
			if err != nil {
				log.Printf("Recv error: %v", err)
				errChan <- err
				return
			}

			// log.Printf("Received frame from scheduler: %d bytes, frame_id=%s, stream_id=%s",
			// 	len(frame.Data), frame.FrameId, frame.StreamId)

			// Register this frame_id to route results back to THIS stream
			pendingResultsMu.Lock()
			pendingResults[frame.FrameId] = resultCh
			pendingResultsMu.Unlock()

			// Build combined packet: [frame_id_len:4][frame_id:N][jpeg_size:4][jpeg_data:M]
			frameIDBytes := []byte(frame.FrameId)
			frameIDLen := uint32(len(frameIDBytes))

			packet := make([]byte, 8+len(frameIDBytes)+len(frame.Data))
			binary.BigEndian.PutUint32(packet[0:4], frameIDLen)
			copy(packet[4:4+len(frameIDBytes)], frameIDBytes)
			binary.BigEndian.PutUint32(packet[4+len(frameIDBytes):8+len(frameIDBytes)], uint32(len(frame.Data)))
			copy(packet[8+len(frameIDBytes):], frame.Data)

			_, err = conn.Write(packet)
			if err != nil {
				log.Printf("TCP write failed: %v", err)
				errChan <- err
				return
			}

			// log.Printf("Forwarded JPEG frame: %d bytes, frame_id=%s", len(frame.Data), frame.FrameId)

			// Store receive time keyed by frame_id (NOT timestamp) and capture TCP send time
			recvTimes.Store(frame.FrameId, frameRecvInfo{
				Time:      time.Now(),
				StageTs:   frame.StageTs,
				UdpSendMs: time.Now().UnixMilli(),
				StreamId:  frame.StreamId,
			})
			atomic.AddInt32(&metricQueue, 1)
		}
	}()

	// -- Sender: receives results from Python -> sends back to scheduler --
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()

		log.Println("=== SENDER GOROUTINE STARTED ===")

		for {
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			case res, ok := <-resultCh:
				if !ok {
					errChan <- nil
					return
				}

				// Compute latency using frame_id lookup (NOT timestamp)
				var originalStageTS *pb.StageTimestamps
				var udpSendMs int64
				var streamID string
				computeRecvResultMs := time.Now().UnixMilli()
				if recvInfo, ok := recvTimes.LoadAndDelete(res.frameID); ok {
					lat := float64(time.Since(recvInfo.Time).Milliseconds())
					metricLatMu.Lock()
					if metricLat == 0 {
						metricLat = lat
					} else {
						metricLat = 0.9*metricLat + 0.1*lat
					}
					metricLatMu.Unlock()
					// log.Printf("Frame %s total compute latency: %.2fms (EMA: %.2fms)", res.frameID, lat, metricLat)

					originalStageTS = recvInfo.StageTs
					if originalStageTS != nil {
						originalStageTS.ComputeReceiveMs = recvInfo.Time.UnixMilli()
					}
					udpSendMs = recvInfo.UdpSendMs
					streamID = recvInfo.StreamId
				} else {
					log.Printf("Warning: No receive info found for frame_id=%s", res.frameID)
				}

				// Estimate python_grpc_send_ms as yolo_end_ms + 2ms (post-processing)
				pythonGrpcSendMs := res.yoloEndMs + 2

				// Build StageTimestamps for return journey propagating all original timestamps
				var returnStageTS *pb.StageTimestamps
				if originalStageTS != nil {
					returnStageTS = originalStageTS
				} else {
					returnStageTS = &pb.StageTimestamps{}
				}
				returnStageTS.UdpSendMs = udpSendMs
				returnStageTS.PythonReceiveMs = res.pythonReceiveMs
				returnStageTS.YoloInferenceStartMs = res.yoloStartMs
				returnStageTS.YoloInferenceEndMs = res.yoloEndMs
				returnStageTS.PythonGrpcSendMs = pythonGrpcSendMs
				returnStageTS.ComputeReceiveResultMs = computeRecvResultMs

				// Build result with per-stage timestamps propagated back
				err := stream.Send(&pb.InferenceResult{
					Timestamp:          uint64(time.Now().UnixMilli()),
					ProcessingStatus:   0,
					Detections:         res.boxes,
					FrameId:            res.frameID,
					StageTs:            returnStageTS,
					InferenceLatencyMs: res.inferenceLatencyMs,
					StreamId:           streamID,
				})

				if err != nil {
					log.Printf("Error sending inference data to scheduler: %v", err)
					errChan <- err
					return
				}

				if ok {
					decrementMetricQueue()
				}

				completionMu.Lock()
				completionTimes = append(completionTimes, time.Now())
				completionMu.Unlock()
			}
		}
	}()

	err = <-errChan
	cancel()

	// Clean up all pending results belonging to this stream
	pendingResultsMu.Lock()
	for k, ch := range pendingResults {
		if ch == resultCh {
			delete(pendingResults, k)
		}
	}
	pendingResultsMu.Unlock()

	close(resultCh)
	wg.Wait()
	log.Printf("Stream %s closed cleanly", streamID)
	return err
}

func startComputeGRPCServer() {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *trackerPort))
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %d: %v", *trackerPort, err)
	}

	s := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	pbcompute.RegisterInferenceTrackerServer(s, &inferenceServer{})

	log.Printf("Go compute gRPC server listening on :%d", *trackerPort)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve gRPC: %v", err)
	}
}

func startSchedulerGRPCServer() {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterComputeStreamServer(s, &computeStreamServer{})

	log.Printf("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func main() {
	flag.Parse()

	go startComputeGRPCServer()
	go startSchedulerGRPCServer()
	go startMetricsServer(*port)

	select {}
}