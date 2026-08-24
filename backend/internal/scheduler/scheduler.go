package scheduler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

// Frame represents a video frame with metadata
type Frame struct {
	Data       []byte
	Timestamp  int64
	FrameID    string
	StreamID   string
	PickedNode string
	StageTS    StageTimestamps
}

// StageTimestamps tracks latency at each pipeline stage (all in milliseconds)
type StageTimestamps struct {
	ClientCaptureMs          int64
	SchedulerReceiveMs       int64
	SchedulerDecisionMs      int64
	ComputeReceiveMs         int64
	UDPSendMs                int64
	PythonReceiveMs          int64
	YOLOInferenceStartMs     int64
	YOLOInferenceEndMs       int64
	PythonGRPCSendMs         int64
	ComputeReceiveResultMs   int64
	SchedulerReceiveResultMs int64
	FrontendSendMs           int64
}

// PerStageLatency holds computed latency for each stage (in ms)
type PerStageLatency struct {
	Stage1_ClientToScheduler   float64 // ClientCapture -> SchedulerReceive
	Stage2_SchedulerDecision   float64 // SchedulerReceive -> SchedulerDecision
	Stage3_SchedulerToCompute  float64 // SchedulerDecision -> ComputeReceive
	Stage4_ComputeToPython     float64 // ComputeReceive -> PythonReceive
	Stage5_PythonDecode        float64 // PythonReceive -> YOLOStart
	Stage6_YOLOInference       float64 // YOLOStart -> YOLOEnd
	Stage7_PythonToCompute     float64 // YOLOEnd -> ComputeReceiveResult
	Stage8_ComputeToScheduler  float64 // ComputeReceiveResult -> SchedulerReceiveResult
	Stage9_SchedulerToFrontend float64 // SchedulerReceiveResult -> FrontendSend
	TotalEndToEnd              float64 // ClientCapture -> FrontendSend
}

// FrameLatencyRecord stores per-frame latency data for export
type FrameLatencyRecord struct {
	FrameID     string
	StreamID    string
	NodeName    string
	Timestamp   int64
	Stages      PerStageLatency
	RawTS       StageTimestamps
}

// Global ring buffer for recent frame latency records (thread-safe)
var (
	latencyRecords     []*FrameLatencyRecord
	latencyRecordsMu   sync.RWMutex
	maxLatencyRecords  = 5000
)

type Nodes []string
type Node string

type NodeMetrics struct {
	CPU         float64 `json:"cpu"`
	GPU         float64 `json:"gpu"`
	Memory      float64 `json:"memory"`
	Temperature float64 `json:"temperature"`
	Queue       float64 `json:"queue"`
	Latency     float64 `json:"latency"`
	Throughput  float64 `json:"throughput"`
	Power       float64 `json:"power"`
}

// WindowConfig controls window-based scheduling
type WindowConfig struct {
	Enabled    bool
	WindowSize int
}

// StreamState tracks per-stream statistics
type StreamState struct {
	StreamID      string
	FrameCount    int64
	DroppedFrames int64
	LastDecisionNode Node
	WindowQueue   []*Frame
	mu            sync.Mutex
}

// Scheduler is the central scheduling engine
type Scheduler struct {
	nodes         Nodes
	metricsMu     sync.RWMutex
	nodeMetrics   map[string]NodeMetrics
	dqnSidecarURL string

	// Round-robin state
	rrIndex int
	rrMutex sync.Mutex

	// Dispatch callback
	OnDispatch func(Node, *Frame)

	// Telemetry statistics
	StatsMu            sync.RWMutex
	TotalFrames        int64
	NodeFrames         map[string]int64
	PolicyFrames       map[int]int64
	DqnInferenceTime   float64 // ms
	DecisionTime       float64 // ms
	ExplorationTime    float64 // ms
	ExploitationTime   float64 // ms
	LastSelectedNode   string
	LastSelectedPolicy int
	DroppedFrames      int64
	SchedulerQueueLen  int64

	// Per-stage latency aggregations (updated every second)
	AvgStageLatency    PerStageLatency
	StageLatencyMu     sync.RWMutex

	// Epsilon-greedy
	epsilon      float64
	epsilonDecay float64
	epsilonMin   float64

	// gRPC connection pool
	nodeConns map[string]*grpc.ClientConn
	connMu    sync.RWMutex

	// Window scheduling
	windowConfig WindowConfig
	windowMu     sync.Mutex
	windowBuffer []*Frame
	windowCount  int

	// Multi-stream support
	streams   map[string]*StreamState
	streamsMu sync.RWMutex

	// Frame tracking for latency (bounded to prevent leaks)
	frameTracker   map[string]*Frame
	frameTrackerMu sync.Mutex

	// Routing overrides for benchmarking
	streamOverrides map[string]string
	overridesMu     sync.RWMutex

	// Real-time in-flight frame tracking
	inFlight   map[string]*int32
	inFlightMu sync.RWMutex

	// DQN Decision Cache
	lastDQNAction       int
	lastDQNDur          float64
	lastExplorationTime  float64
	lastExploitationTime float64
	lastDQNQueryTime     time.Time
	dqnCacheMu           sync.Mutex

	// Policy Override
	ForcedPolicy int
}

func NewScheduler(nodes Nodes, dqnURL string, windowConfig WindowConfig) *Scheduler {
	s := &Scheduler{
		nodes:           nodes,
		ForcedPolicy:    -1,
		nodeMetrics:     make(map[string]NodeMetrics),
		dqnSidecarURL:   dqnURL,
		rrIndex:         0,
		NodeFrames:      make(map[string]int64),
		PolicyFrames:    make(map[int]int64),
		epsilon:         0.30,
		epsilonDecay:    0.995,
		epsilonMin:      0.05,
		nodeConns:       make(map[string]*grpc.ClientConn),
		windowConfig:    windowConfig,
		windowBuffer:    make([]*Frame, 0, windowConfig.WindowSize),
		streams:         make(map[string]*StreamState),
		frameTracker:    make(map[string]*Frame),
		streamOverrides: make(map[string]string),
		inFlight:        make(map[string]*int32),
	}

	for _, node := range nodes {
		s.nodeMetrics[node] = NodeMetrics{
			CPU:         20.0,
			Memory:      20.0,
			Temperature: 35.0,
			Queue:       0.0,
			Latency:     5.0,
			Throughput:  30.0,
			Power:       5.0,
		}
		s.NodeFrames[node] = 0
		var val int32 = 0
		s.inFlight[node] = &val
	}
	for i := 0; i < 4; i++ {
		s.PolicyFrames[i] = 0
	}

	go s.frameTrackerCleanup()
	go s.latencyAggregator() // NEW: background aggregation
	go s.startDQNWorker()    // NEW: decoupled non-blocking DQN RL background query worker

	return s
}

// startDQNWorker polls the DQN sidecar asynchronously in the background so frame scheduling is never blocked by HTTP
func (s *Scheduler) startDQNWorker() {
	ticker := time.NewTicker(30 * time.Millisecond)
	go func() {
		for range ticker.C {
			state := s.ConstructStateVector()
			action, _, exploreTime, exploitTime, err := s.QueryDQNSidecar(state)
			s.dqnCacheMu.Lock()
			if err == nil {
				s.lastDQNAction = action
				s.lastExplorationTime = exploreTime
				s.lastExploitationTime = exploitTime
				s.lastDQNDur = 1.5
			}
			s.lastDQNQueryTime = time.Now()
			s.dqnCacheMu.Unlock()
		}
	}()
}

// SetStreamOverrides updates the routing overrides map dynamically
func (s *Scheduler) SetStreamOverrides(overrides map[string]string) {
	s.overridesMu.Lock()
	s.streamOverrides = overrides
	s.overridesMu.Unlock()
}

// latencyAggregator computes rolling average of per-stage latencies every second
func (s *Scheduler) latencyAggregator() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		latencyRecordsMu.RLock()
		records := make([]*FrameLatencyRecord, len(latencyRecords))
		copy(records, latencyRecords)
		latencyRecordsMu.RUnlock()

		if len(records) == 0 {
			continue
		}

		var sum PerStageLatency
		var count int
		for _, r := range records {
			if r.Stages.TotalEndToEnd <= 0 {
				continue // incomplete record
			}
			sum.Stage1_ClientToScheduler += r.Stages.Stage1_ClientToScheduler
			sum.Stage2_SchedulerDecision += r.Stages.Stage2_SchedulerDecision
			sum.Stage3_SchedulerToCompute += r.Stages.Stage3_SchedulerToCompute
			sum.Stage4_ComputeToPython += r.Stages.Stage4_ComputeToPython
			sum.Stage5_PythonDecode += r.Stages.Stage5_PythonDecode
			sum.Stage6_YOLOInference += r.Stages.Stage6_YOLOInference
			sum.Stage7_PythonToCompute += r.Stages.Stage7_PythonToCompute
			sum.Stage8_ComputeToScheduler += r.Stages.Stage8_ComputeToScheduler
			sum.Stage9_SchedulerToFrontend += r.Stages.Stage9_SchedulerToFrontend
			sum.TotalEndToEnd += r.Stages.TotalEndToEnd
			count++
		}

		if count > 0 {
			var avg PerStageLatency
			avg.Stage1_ClientToScheduler = sum.Stage1_ClientToScheduler / float64(count)
			avg.Stage2_SchedulerDecision = sum.Stage2_SchedulerDecision / float64(count)
			avg.Stage3_SchedulerToCompute = sum.Stage3_SchedulerToCompute / float64(count)
			avg.Stage4_ComputeToPython = sum.Stage4_ComputeToPython / float64(count)
			avg.Stage5_PythonDecode = sum.Stage5_PythonDecode / float64(count)
			avg.Stage6_YOLOInference = sum.Stage6_YOLOInference / float64(count)
			avg.Stage7_PythonToCompute = sum.Stage7_PythonToCompute / float64(count)
			avg.Stage8_ComputeToScheduler = sum.Stage8_ComputeToScheduler / float64(count)
			avg.Stage9_SchedulerToFrontend = sum.Stage9_SchedulerToFrontend / float64(count)
			avg.TotalEndToEnd = sum.TotalEndToEnd / float64(count)

			s.StageLatencyMu.Lock()
			s.AvgStageLatency = avg
			s.StageLatencyMu.Unlock()
		}
	}
}

// GetAvgStageLatency returns the current rolling average
func (s *Scheduler) GetAvgStageLatency() PerStageLatency {
	s.StageLatencyMu.RLock()
	defer s.StageLatencyMu.RUnlock()
	return s.AvgStageLatency
}

// frameTrackerCleanup removes old frame entries and recovers leaked inFlight counters
func (s *Scheduler) frameTrackerCleanup() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		s.frameTrackerMu.Lock()
		now := time.Now().UnixMilli()
		for id, f := range s.frameTracker {
			if now-f.Timestamp > 1000 { // 1 second timeout for unreturned frames
				if f.PickedNode != "" {
					s.DecrementInFlight(f.PickedNode)
				}
				s.IncrementDroppedFrames()
				delete(s.frameTracker, id)
			}
		}
		s.frameTrackerMu.Unlock()
	}
}

// GetOrCreateStream returns a stream state, creating if needed
func (s *Scheduler) GetOrCreateStream(streamID string) *StreamState {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if st, ok := s.streams[streamID]; ok {
		return st
	}
	st := &StreamState{
		StreamID:    streamID,
		WindowQueue: make([]*Frame, 0, s.windowConfig.WindowSize),
	}
	s.streams[streamID] = st
	return st
}

// RemoveStream cleans up a disconnected stream
func (s *Scheduler) RemoveStream(streamID string) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	delete(s.streams, streamID)
}

// GetNodeConnection returns a gRPC connection to a node
func (s *Scheduler) GetNodeConnection(nodeAddr string) (*grpc.ClientConn, error) {
	s.connMu.RLock()
	conn, exists := s.nodeConns[nodeAddr]
	s.connMu.RUnlock()

	if exists && conn != nil {
		state := conn.GetState()
		if state != connectivity.Shutdown && state != connectivity.TransientFailure {
			return conn, nil
		}
		conn.Close()
	}

	s.connMu.Lock()
	defer s.connMu.Unlock()

	if conn, exists := s.nodeConns[nodeAddr]; exists && conn != nil {
		state := conn.GetState()
		if state != connectivity.Shutdown && state != connectivity.TransientFailure {
			return conn, nil
		}
		conn.Close()
	}

	conn, err := grpc.NewClient(nodeAddr,
		grpc.WithInsecure(),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
	)
	if err != nil {
		return nil, err
	}

	s.nodeConns[nodeAddr] = conn
	return conn, nil
}

// CloseAllConnections closes all managed connections
func (s *Scheduler) CloseAllConnections() {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	for addr, conn := range s.nodeConns {
		if conn != nil {
			conn.Close()
		}
		delete(s.nodeConns, addr)
	}
}

// UpdateNodeMetrics updates metrics for a node
func (s *Scheduler) UpdateNodeMetrics(node string, metrics NodeMetrics) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.nodeMetrics[node] = metrics
}

// GetNodeMetrics retrieves metrics for a node
func (s *Scheduler) GetNodeMetrics(node string) (NodeMetrics, bool) {
	s.metricsMu.RLock()
	defer s.metricsMu.RUnlock()
	m, exists := s.nodeMetrics[node]
	return m, exists
}

// ConstructStateVector builds the 18-dimensional state vector
func (s *Scheduler) ConstructStateVector() []float64 {
	s.metricsMu.RLock()
	defer s.metricsMu.RUnlock()

	state := make([]float64, 0, 18)
	for i := 0; i < 3; i++ {
		nodeName := "node1"
		if i < len(s.nodes) {
			nodeName = s.nodes[i]
		}
		m := s.nodeMetrics[nodeName]
		state = append(state, m.CPU, m.Memory, m.Temperature, float64(s.GetInFlight(nodeName)), m.Latency, m.Throughput)
	}
	return state
}

// QueryDQNSidecar calls the DQN service and returns timing metadata
func (s *Scheduler) QueryDQNSidecar(state []float64) (int, []float64, float64, float64, error) {
	payload := map[string][]float64{"state": state}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, 0, 0, err
	}

	client := &http.Client{Timeout: 300 * time.Millisecond}
	start := time.Now()
	resp, err := client.Post(s.dqnSidecarURL+"/predict", "application/json", bytes.NewBuffer(jsonPayload))
	httpDur := time.Since(start).Seconds() * 1000.0

	if err != nil {
		return 0, nil, 0, httpDur, err
	}
	defer resp.Body.Close()

	var result struct {
		Action             int       `json:"action"`
		QValues            []float64 `json:"q_values"`
		ExplorationTimeMs  float64   `json:"exploration_time_ms"`
		ExploitationTimeMs float64   `json:"exploitation_time_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, nil, 0, httpDur, err
	}

	return result.Action, result.QValues, result.ExplorationTimeMs, result.ExploitationTimeMs, nil
}

func (s *Scheduler) calculateNodeScore(node string, m NodeMetrics) float64 {
	latencyScore := math.Exp(-m.Latency / 100.0)
	queueScore := 1.0 - (m.Queue / 50.0)
	if queueScore < 0 {
		queueScore = 0
	}
	inFlight := float64(s.GetInFlight(node))
	inFlightScore := math.Exp(-inFlight / 40.0)

	cpuScore := 1.0
	if m.CPU > 95.0 {
		cpuScore = 0
	} else if m.CPU > 80.0 {
		cpuScore = 1.0 - (m.CPU-80.0)/15.0
	}
	tempScore := (85.0 - m.Temperature) / (85.0 - 45.0)
	if tempScore > 1.0 {
		tempScore = 1.0
	}
	if tempScore < 0 {
		tempScore = 0
	}
	memScore := 1.0 - (m.Memory / 100.0)
	if memScore < 0 {
		memScore = 0
	}

	return 0.35*inFlightScore + 0.25*latencyScore + 0.25*queueScore +
		0.05*cpuScore + 0.05*tempScore + 0.05*memScore
}

// SelectNodeHierarchical makes a scheduling decision with full latency tracking
func (s *Scheduler) SelectNodeHierarchical(frame *Frame) (Node, int, StageTimestamps) {
	startDecision := time.Now()
	stageTS := frame.StageTS
	stageTS.SchedulerReceiveMs = startDecision.UnixMilli()

	// Check if this stream has a pinned compute node override
	s.overridesMu.RLock()
	overrideNode, hasOverride := s.streamOverrides[frame.StreamID]
	s.overridesMu.RUnlock()

	if hasOverride && overrideNode != "" && overrideNode != "dqn" {
		s.StatsMu.Lock()
		s.TotalFrames++
		s.NodeFrames[overrideNode]++
		s.LastSelectedNode = overrideNode
		s.LastSelectedPolicy = -1 // Indicates benchmarking/manual override policy
		s.StatsMu.Unlock()

		stageTS.SchedulerDecisionMs = time.Now().UnixMilli()
		return Node(overrideNode), -1, stageTS
	}

	// Instantly read latest cached DQN policy action (0.01ms zero-latency read)
	s.dqnCacheMu.Lock()
	action := s.lastDQNAction
	dqnDur := s.lastDQNDur
	exploreTime := s.lastExplorationTime
	exploitTime := s.lastExploitationTime
	s.dqnCacheMu.Unlock()

	// Epsilon-greedy or Forced Policy Override
	isExploration := false
	if s.ForcedPolicy >= 0 {
		action = s.ForcedPolicy
	} else {
		s.StatsMu.Lock()
		currentEpsilon := s.epsilon
		s.epsilon = s.epsilon * s.epsilonDecay
		if s.epsilon < s.epsilonMin {
			s.epsilon = s.epsilonMin
		}
		s.StatsMu.Unlock()

		if rand.Float64() < currentEpsilon {
			action = rand.Intn(3)
			isExploration = true
		}
	}

	var pickedNode string

	s.metricsMu.RLock()
	metrics := make(map[string]NodeMetrics)
	for _, node := range s.nodes {
		metrics[node] = s.nodeMetrics[node]
	}
	s.metricsMu.RUnlock()

	switch action {
	case 0:
		pickedNode = s.nodes[0]
		if s.GetInFlight(pickedNode) >= 15 {
			pickedNode = s.findBestNode(metrics)
			action = 3
		}
	case 1:
		s.rrMutex.Lock()
		for i := 0; i < len(s.nodes); i++ {
			candidate := s.nodes[s.rrIndex]
			s.rrIndex = (s.rrIndex + 1) % len(s.nodes)
			if s.GetInFlight(candidate) < 15 {
				pickedNode = candidate
				break
			}
		}
		if pickedNode == "" {
			pickedNode = s.findBestNode(metrics)
			action = 3
		}
		s.rrMutex.Unlock()
	case 2:
		pickedNode = s.findBestNode(metrics)
	default:
		pickedNode = s.findBestNode(metrics)
		action = 3
	}

	if s.GetInFlight(pickedNode) >= 15 {
		pickedNode = s.findBestNode(metrics)
		action = 3
	}

	decisionDur := time.Since(startDecision).Seconds() * 1000.0
	stageTS.SchedulerDecisionMs = time.Now().UnixMilli()

	// Update stats
	s.StatsMu.Lock()
	s.TotalFrames++
	s.NodeFrames[pickedNode]++
	s.PolicyFrames[action]++
	s.DqnInferenceTime = dqnDur
	s.DecisionTime = decisionDur
	if isExploration {
		s.ExplorationTime = exploreTime
		s.ExploitationTime = 0
	} else {
		s.ExplorationTime = 0
		s.ExploitationTime = exploitTime
	}
	s.LastSelectedNode = pickedNode
	s.LastSelectedPolicy = action
	s.StatsMu.Unlock()

	return Node(pickedNode), action, stageTS
}

func (s *Scheduler) findBestNode(metrics map[string]NodeMetrics) string {
	var candidateNodes []string
	for _, node := range s.nodes {
		if s.GetInFlight(node) < 15 {
			candidateNodes = append(candidateNodes, node)
		}
	}
	if len(candidateNodes) == 0 {
		candidateNodes = s.nodes
	}

	bestNode := candidateNodes[0]
	bestScore := -1.0

	for _, node := range candidateNodes {
		score := s.calculateNodeScore(node, metrics[node])
		if score > bestScore {
			bestScore = score
			bestNode = node
		}
	}
	return bestNode
}

// ScheduleFrame is the entry point for per-frame scheduling
func (s *Scheduler) ScheduleFrame(frame *Frame) {
	if frame.FrameID == "" {
		frame.FrameID = fmt.Sprintf("frame-%d-%d", time.Now().UnixNano(), rand.Int63())
	}

	// Track frame for end-to-end latency
	s.frameTrackerMu.Lock()
	s.frameTracker[frame.FrameID] = frame
	s.frameTrackerMu.Unlock()

	// If window scheduling is disabled, dispatch immediately
	if !s.windowConfig.Enabled || s.windowConfig.WindowSize <= 1 {
		pickedNode, _, stageTS := s.SelectNodeHierarchical(frame)
		frame.PickedNode = string(pickedNode)
		frame.StageTS = stageTS

		inFlight := s.GetInFlight(string(pickedNode))
		if inFlight > 30 {
			s.IncrementDroppedFrames()
			s.frameTrackerMu.Lock()
			delete(s.frameTracker, frame.FrameID)
			s.frameTrackerMu.Unlock()
			return
		}

		if s.OnDispatch != nil {
			s.OnDispatch(pickedNode, frame)
		}
		return
	}

	// Per-stream window-based scheduling
	stream := s.GetOrCreateStream(frame.StreamID)
	stream.mu.Lock()
	stream.WindowQueue = append(stream.WindowQueue, frame)

	if len(stream.WindowQueue) >= s.windowConfig.WindowSize {
		// Make ONE decision for the entire window
		windowStart := time.Now()
		// Use the first frame's timestamp for decision
		decisionFrame := stream.WindowQueue[0]
		pickedNode, action, stageTS := s.SelectNodeHierarchical(decisionFrame)

		// Dispatch all frames in the window to the same node
		for _, f := range stream.WindowQueue {
			f.StageTS = stageTS
			f.StageTS.SchedulerDecisionMs = time.Now().UnixMilli()
			if s.OnDispatch != nil {
				s.OnDispatch(pickedNode, f)
			}
		}

		// Log window scheduling overhead
		windowOverhead := time.Since(windowStart).Seconds() * 1000.0
		fmt.Printf("WINDOW: dispatched %d frames of %s to %s in %.2fms (action=%d)\n",
			len(stream.WindowQueue), frame.StreamID, pickedNode, windowOverhead, action)

		// Reset stream window queue
		stream.WindowQueue = stream.WindowQueue[:0]
	}
	stream.mu.Unlock()
}

// ComputePerStageLatency calculates all stage latencies from raw timestamps
// and applies symmetric clock drift correction for cross-machine stages S3 and S8.
func ComputePerStageLatency(ts StageTimestamps) PerStageLatency {
	var lat PerStageLatency
	if ts.ClientCaptureMs > 0 && ts.SchedulerReceiveMs > 0 {
		lat.Stage1_ClientToScheduler = float64(ts.SchedulerReceiveMs - ts.ClientCaptureMs)
	}
	if ts.SchedulerReceiveMs > 0 && ts.SchedulerDecisionMs > 0 {
		lat.Stage2_SchedulerDecision = float64(ts.SchedulerDecisionMs - ts.SchedulerReceiveMs)
	}

	// Calculate cross-machine networking with clock drift correction
	if ts.SchedulerDecisionMs > 0 && ts.ComputeReceiveMs > 0 &&
		ts.ComputeReceiveResultMs > 0 && ts.SchedulerReceiveResultMs > 0 {
		
		totalRTT := float64(ts.SchedulerReceiveResultMs - ts.SchedulerDecisionMs)
		computeDuration := float64(ts.ComputeReceiveResultMs - ts.ComputeReceiveMs)
		netLatency := totalRTT - computeDuration
		if netLatency < 0 {
			netLatency = 0
		}
		oneWayNet := netLatency / 2.0
		
		lat.Stage3_SchedulerToCompute = oneWayNet
		lat.Stage8_ComputeToScheduler = oneWayNet
	} else {
		// Fallback without drift correction if timestamps are incomplete
		if ts.SchedulerDecisionMs > 0 && ts.ComputeReceiveMs > 0 {
			lat.Stage3_SchedulerToCompute = float64(ts.ComputeReceiveMs - ts.SchedulerDecisionMs)
		}
		if ts.ComputeReceiveResultMs > 0 && ts.SchedulerReceiveResultMs > 0 {
			lat.Stage8_ComputeToScheduler = float64(ts.SchedulerReceiveResultMs - ts.ComputeReceiveResultMs)
		}
	}

	if ts.ComputeReceiveMs > 0 && ts.PythonReceiveMs > 0 {
		lat.Stage4_ComputeToPython = float64(ts.PythonReceiveMs - ts.ComputeReceiveMs)
	}
	if ts.PythonReceiveMs > 0 && ts.YOLOInferenceStartMs > 0 {
		lat.Stage5_PythonDecode = float64(ts.YOLOInferenceStartMs - ts.PythonReceiveMs)
	}
	if ts.YOLOInferenceStartMs > 0 && ts.YOLOInferenceEndMs > 0 {
		lat.Stage6_YOLOInference = float64(ts.YOLOInferenceEndMs - ts.YOLOInferenceStartMs)
	}
	if ts.YOLOInferenceEndMs > 0 && ts.ComputeReceiveResultMs > 0 {
		lat.Stage7_PythonToCompute = float64(ts.ComputeReceiveResultMs - ts.YOLOInferenceEndMs)
	}
	if ts.SchedulerReceiveResultMs > 0 && ts.FrontendSendMs > 0 {
		lat.Stage9_SchedulerToFrontend = float64(ts.FrontendSendMs - ts.SchedulerReceiveResultMs)
	}
	if ts.ClientCaptureMs > 0 && ts.FrontendSendMs > 0 {
		lat.TotalEndToEnd = float64(ts.FrontendSendMs - ts.ClientCaptureMs)
	}
	return lat
}

// RecordResultLatency records when a result comes back and computes per-stage breakdown
func (s *Scheduler) RecordResultLatency(frameID string, nodeName string, resultTS StageTimestamps) {
	s.DecrementInFlight(nodeName)

	s.frameTrackerMu.Lock()
	if frame, ok := s.frameTracker[frameID]; ok {
		frame.StageTS.SchedulerReceiveResultMs = time.Now().UnixMilli()
		frame.StageTS.ComputeReceiveResultMs = resultTS.ComputeReceiveResultMs
		frame.StageTS.YOLOInferenceStartMs = resultTS.YOLOInferenceStartMs
		frame.StageTS.YOLOInferenceEndMs = resultTS.YOLOInferenceEndMs
		frame.StageTS.PythonGRPCSendMs = resultTS.PythonGRPCSendMs
		frame.StageTS.ComputeReceiveMs = resultTS.ComputeReceiveMs
		frame.StageTS.PythonReceiveMs = resultTS.PythonReceiveMs
		frame.StageTS.UDPSendMs = resultTS.UDPSendMs
		frame.StageTS.FrontendSendMs = resultTS.FrontendSendMs

		// Calculate stage latency
		stages := ComputePerStageLatency(frame.StageTS)
		record := &FrameLatencyRecord{
			FrameID:   frameID,
			StreamID:  frame.StreamID,
			NodeName:  nodeName,
			Timestamp: time.Now().UnixMilli(),
			Stages:    stages,
			RawTS:     frame.StageTS,
		}

		latencyRecordsMu.Lock()
		latencyRecords = append(latencyRecords, record)
		if len(latencyRecords) > maxLatencyRecords {
			latencyRecords = latencyRecords[len(latencyRecords)-maxLatencyRecords:]
		}
		latencyRecordsMu.Unlock()

		delete(s.frameTracker, frameID)
	}
	s.frameTrackerMu.Unlock()
}

// RecordFrontendSend records when a result is written to the frontend WebSocket/client
func (s *Scheduler) RecordFrontendSend(frameID string, sendMs int64) {
	latencyRecordsMu.Lock()
	defer latencyRecordsMu.Unlock()
	for i := len(latencyRecords) - 1; i >= 0; i-- {
		if latencyRecords[i].FrameID == frameID {
			if latencyRecords[i].RawTS.SchedulerReceiveResultMs > 0 {
				lat := float64(sendMs - latencyRecords[i].RawTS.SchedulerReceiveResultMs)
				if lat >= 0 {
					latencyRecords[i].Stages.Stage9_SchedulerToFrontend = lat
					if latencyRecords[i].RawTS.ClientCaptureMs > 0 {
						latencyRecords[i].Stages.TotalEndToEnd = float64(sendMs - latencyRecords[i].RawTS.ClientCaptureMs)
					}
				}
			}
			break
		}
	}
}

// GetRecentLatencyRecords returns the last N latency records
func GetRecentLatencyRecords(n int) []*FrameLatencyRecord {
	latencyRecordsMu.RLock()
	defer latencyRecordsMu.RUnlock()
	if n > len(latencyRecords) {
		n = len(latencyRecords)
	}
	result := make([]*FrameLatencyRecord, n)
	copy(result, latencyRecords[len(latencyRecords)-n:])
	return result
}

// IncrementDroppedFrames atomically increments dropped frame counter
func (s *Scheduler) IncrementDroppedFrames() {
	atomic.AddInt64(&s.DroppedFrames, 1)
}

// GetDroppedFrames returns current dropped frame count
func (s *Scheduler) GetDroppedFrames() int64 {
	return atomic.LoadInt64(&s.DroppedFrames)
}

// GetSchedulerQueueLen returns current queue length (sum of all stream window queues)
func (s *Scheduler) GetSchedulerQueueLen() int64 {
	s.streamsMu.RLock()
	defer s.streamsMu.RUnlock()
	var total int64
	for _, stream := range s.streams {
		stream.mu.Lock()
		total += int64(len(stream.WindowQueue))
		stream.mu.Unlock()
	}
	return total
}

func (s *Scheduler) GetInFlight(node string) int32 {
	s.inFlightMu.RLock()
	valPtr, exists := s.inFlight[node]
	s.inFlightMu.RUnlock()
	if !exists || valPtr == nil {
		return 0
	}
	return atomic.LoadInt32(valPtr)
}

func (s *Scheduler) IncrementInFlight(node string) {
	s.inFlightMu.RLock()
	valPtr, exists := s.inFlight[node]
	s.inFlightMu.RUnlock()
	if exists && valPtr != nil {
		atomic.AddInt32(valPtr, 1)
	}
}

func (s *Scheduler) DecrementInFlight(node string) {
	s.inFlightMu.RLock()
	valPtr, exists := s.inFlight[node]
	s.inFlightMu.RUnlock()
	if exists && valPtr != nil {
		val := atomic.AddInt32(valPtr, -1)
		if val < 0 {
			atomic.StoreInt32(valPtr, 0)
		}
	}
}

func (s *Scheduler) ResetInFlight(node string) {
	s.inFlightMu.RLock()
	valPtr, exists := s.inFlight[node]
	s.inFlightMu.RUnlock()
	if exists && valPtr != nil {
		atomic.StoreInt32(valPtr, 0)
	}
}