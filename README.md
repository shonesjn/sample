# Node 2 - Distributed Compute Node

This package contains the standalone edge compute node software to be run on **Node 2 (Physical Jetson Nano)**.

As requested, this folder is fully decoupled and **does NOT contain**:
- Scheduler
- Hierarchical DQN
- Plot generation
- CSV logging
- Telemetry management

It contains only the high-performance frame receiver, H264/JPEG decoder, YOLO inference engine (TensorRT / PyTorch), and the HTTP metrics endpoint to report local statistics back to the scheduler on Node 1.

---

## Folder Structure

```
node2/
├── cmd/
│   └── compute/
│       ├── edge-compute-yolo/
│       │   ├── main.py                   # Python YOLO inference script
│       │   ├── yolo11n.pt                # PyTorch fallback weights
│       │   └── yolo26n.engine            # High-performance TensorRT engine
│       ├── main.go                       # Go compute node gRPC server
│       └── mock_yolo.py                  # Mock YOLO script (for CPU testing)
├── proto/                                # Compiled gRPC Protobuf files
├── go.mod                                # Go modules configuration
└── go.sum
```

---

## Ports & Communication Configuration

The compute node runs two main communication loops:
1. **gRPC Frame Stream** (Incoming from Node 1 Scheduler): Port `9995`
2. **HTTP Metrics Server** (Polled by Node 1 Scheduler): Port `9994` (automatically computed as `gRPC port - 1`)
3. **Local YOLO IPC** (Between Go server and Python YOLO):
   - TCP Frame Stream: Port `5000`
   - Go gRPC Inference Tracker: Port `5005`

---

## Deployment & Execution Commands

### Step 1: Install Dependencies
Ensure you have Go (1.20+) and Python 3 installed. Install the Python dependencies:
```bash
pip install opencv-python ultralytics grpcio protobuf numpy
```

### Step 2: Start Python YOLO Inference Service
On the Jetson Nano, launch the Python YOLO engine (loading the TensorRT engine for optimal GPU execution):
```bash
python cmd/compute/edge-compute-yolo/main.py --tcp-port 5000 --tracker-port 5005 --model cmd/compute/edge-compute-yolo/yolo26n.engine
```

### Step 3: Start Go Compute Node Server
In a separate terminal, compile and run the Go compute server, specifying port `9995`:
```bash
go run cmd/compute/main.go -port 9995 -udp-port 5000 -tracker-port 5005
```

Once running, the Scheduler on Node 1 will automatically open a gRPC stream to this node, schedule video frames to it, and poll its metrics on `http://<node-2-ip>:9994/metrics`.
