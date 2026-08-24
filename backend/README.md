# ASET: Adaptive Multi-Node Edge Offloading & Real-Time Inference Scheduler

ASET (Adaptive Systems Edge Technology) is a high-performance, real-time distributed edge offloading and deep learning inference scheduling framework. Designed for heterogeneous Jetson edge hardware topologies, ASET dynamically balances multi-stream YOLO video inference workloads across local and remote Jetson compute nodes using Reinforcement Learning (DQN) and adaptive scoring algorithms.

---

##  System Architecture & Topology

The system consists of a multi-stream web/CLI client, a central Go-based ASET Scheduler, a Python Deep Q-Network (DQN) RL decision sidecar, and heterogeneous Jetson compute nodes running pipelined TensorRT YOLO engines.

```
                         +-----------------------------------+
                         |  Multi-Stream Frontend (Next.js)  |
                         |     or  Go Benchmark (gRPC)       |
                         +-----------------+-----------------+
                                           | WebSocket / gRPC
                                           v
                         +-----------------------------------+
                         |    ASET Scheduler Server (Go)     |  <---> DQN Sidecar (PyTorch :5010)
                         |           Port :9998              |
                         +--------+--------+--------+--------+
                                  |        |        |
                         +--------+        |        +--------+
                         | gRPC            | gRPC            | gRPC
                         v                 v                 v
                +-----------------+  +-----------------+  +-----------------+
                | Node 1 (Orin)   |  | Node 2 (Nano)   |  | Node 3 (Remote) |
                |  Port :9997     |  |  Port :9995     |  |  Port :9993     |
                | TensorRT FP16   |  | PyTorch/TRT     |  | TensorRT FP16   |
                +-----------------+  +-----------------+  +-----------------+
```

### Hardware Specifications & Cluster Capacity
- **Node 1 (Jetson Orin Nano):** Local primary node running TensorRT FP16 YOLO (`~35–55 FPS` capacity, `0.5 ms` local IPC latency).
- **Node 2 (Jetson Nano Seeed):** Secondary edge node (`~12.5–16.0 FPS` capacity, IP `100.79.63.9`).
- **Node 3 (Remote Jetson Orin):** Secondary high-capacity node (`~35.0 FPS` capacity over network).
- **Total Aggregate System Throughput:** **`~86.0 FPS`** across all 3 nodes simultaneously.

---

##  Key Features

1. **Decoupled Asynchronous DQN Scheduler:** Zero-latency (`0.01 ms`) scheduling decision thread decoupled from sidecar HTTP polling via background goroutines.
2. **Pipelined Decoding & CUDA Execution:** ARM CPU decodes Frame $N+1$ via OpenCV in parallel with GPU CUDA TensorRT execution on Frame $N$, dropping GPU-visible latency to `~15 ms`.
3. **Multi-Policy Offloading Support:**
   - **Policy 0:** Local Node 1 Routing (Ultra-low `0.5 ms` latency).
   - **Policy 1:** Round-Robin Distribution.
   - **Policy 2:** Adaptive Dynamic Score Balancer (evaluates queue depth, GPU utilization, latency, CPU, and temperature).
   - **Policy 3:** Fallback Offloading when primary node queue $\ge 15$ frames.
4. **Multi-Stream Frontend (`fw-edge-frontend-wiz`):** Next.js 14 / React dashboard running 20 real-time video stream tiles with GPU texture extraction (`createImageBitmap`), 12ms per-stream staggered timers, and lockstep socket backpressure control.
5. **Stage-by-Stage Latency Profiling (9 Stages):** Real-time measurement of client-to-scheduler, decision, network transfer, JPEG decode, CUDA inference, and return delivery overheads.

---

##  Prerequisites & Environment Setup

- **Go:** 1.21+
- **Python:** 3.10+ with PyTorch, CUDA, TensorRT, OpenCV (`cv2`), `ultralytics`
- **Node.js:** 18+ (for frontend dashboard)
- **Jetson JetPack:** 5.x / 6.x with CUDA support

---

##  Step-by-Step Execution Commands

### Step 1: Start the DQN RL Sidecar Service
On Node 1, launch the PyTorch DQN decision sidecar:

```bash
cd cmd/scheduler
python dqn_service.py
```
*(Runs on `http://localhost:5010`)*

---

### Step 2: Start the Compute Nodes

#### On Node 1 (Jetson Orin - Local Compute):
```bash
cd cmd/compute/edge-compute-yolo
python main.py --port 9997 --engine yolo11n.engine
```

#### On Node 2 (Jetson Nano - Compute):
```bash
cd cmd/compute/edge-compute-yolo
python main.py --port 9995 --engine yolo11n.engine
```

#### On Node 3 (Remote Orin - Compute):
```bash
cd cmd/compute/edge-compute-yolo
python main.py --port 9993 --engine yolo11n.engine
```

---

### Step 3: Start the ASET Scheduler Server

On Node 1, launch the core Go scheduler:

```bash
# Standard Auto / DQN RL Mode (Default)
go run ./cmd/scheduler -node1-addr localhost:9997 -node2-addr localhost:9995 -node3-addr localhost:9993

# Forced Multi-Node Load Balancing Mode (Policy 2 for max 86 FPS throughput)
go run ./cmd/scheduler -node1-addr localhost:9997 -node2-addr localhost:9995 -node3-addr localhost:9993 -policy 2
```
*(Multiplexed WebSocket & gRPC server starts on port `:9998`)*

---

### Step 4: Launch the Client (Choose Frontend UI or Native CLI Benchmark)

#### Option A: Web-Based Multi-Stream Dashboard (Frontend)
```bash
cd fw-edge-frontend-wiz
npm install
npm run dev
```
Open browser at `http://localhost:3000/multi-stream`, upload video files, and click **"Start All Streams"**.

#### Option B: Native Multi-Threaded gRPC Benchmark CLI
To run high-throughput benchmarks without browser DOM overhead:

```bash
# Run 20 streams at 4.3 FPS target rate for 120 seconds using real JPEG dataset frames
go run ./cmd/benchmark -config benchmark_config.json -fps 4.3 -duration 120 -dataset ./data/coco_val
```

---

##  Analytics & Plot Generation

Generate telemetry dashboards and stage overhead breakdown bar charts from `scheduler_metrics.csv`:

```bash
# Generate 4-Panel Timeline & Stream Capacity vs. Drop Rate Plots
python plot_timeline.py

# Generate 9-Stage Offloading Overhead Breakdown Chart
python plot_overheads.py
```

Generated plot artifacts:
- `node_selection_and_resource_timeline.png`
- `stream_capacity_vs_drop_rate.png`
- `overhead_breakdown.png`

---

##  Repository Structure

```
.
├── cmd/
│   ├── scheduler/          # Go ASET Scheduler server & Python DQN sidecar (dqn_service.py)
│   ├── compute/            # Edge compute engines (YOLO TensorRT / OpenCV pipelined decode)
│   └── benchmark/          # Native multi-stream gRPC load generator (main.go)
├── internal/
│   └── scheduler/          # Core scheduler logic, state vector construction, & policy scoring
├── fw-edge-frontend-wiz/   # Next.js 14 multi-stream browser visualization laboratory
├── plot_timeline.py        # Telemetry dashboard & capacity plotting script
├── plot_overheads.py       # 9-Stage latency overhead breakdown generator
└── README.md
```

---

## 📝 License & Citation

Developed for distributed edge AI research and real-time multi-node video inference optimization.
